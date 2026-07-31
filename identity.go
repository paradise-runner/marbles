package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// IdentityInfo holds the computed identity signals and the asserted agent.
type IdentityInfo struct {
	Agent        string       `json:"agent,omitempty"`
	Method       string       `json:"method,omitempty"` // "fingerprint", "token", "env", "ambiguous", "none"
	TokenPresent bool         `json:"token_present,omitempty"`
	Fingerprint  *Fingerprint `json:"fingerprint,omitempty"`
}

// Fingerprint captures environmental signals for agent identification.
type Fingerprint struct {
	OSUser     string   `json:"os_user"`
	ParentProc string   `json:"parent_proc,omitempty"`
	Harnesses  []string `json:"harnesses,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
}

// Known harness env var prefixes (only presence checked, never values).
var harnessEnvPrefixes = []string{
	"CLAUDE_",
	"PI_",
	"CODEX_",
	"AIDER_",
	"OPENAI_",
	"ANTHROPIC_",
	"GITHUB_COPILOT_",
}

// ComputeFingerprint gathers signals from the environment.
func ComputeFingerprint() *Fingerprint {
	fp := &Fingerprint{}

	// OS user
	if u, err := user.Current(); err == nil {
		fp.OSUser = u.Username
	}

	// Parent process (best-effort)
	switch runtime.GOOS {
	case "darwin":
		if ppid := os.Getppid(); ppid > 0 {
			// Try ps to get parent process command
			out, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprintf("%d", ppid)).Output()
			if err == nil {
				fp.ParentProc = strings.TrimSpace(string(out))
			}
		}
	case "linux":
		if ppid := os.Getppid(); ppid > 0 {
			if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", ppid)); err == nil {
				parts := strings.SplitN(string(data), "\x00", 2)
				if len(parts) > 0 {
					fp.ParentProc = strings.TrimSpace(parts[0])
				}
			}
		}
	}

	// Harness env vars (presence only)
	for _, prefix := range harnessEnvPrefixes {
		for _, env := range os.Environ() {
			if strings.HasPrefix(env, prefix) {
				fp.Harnesses = append(fp.Harnesses, prefix)
				break
			}
		}
	}

	// Cwd
	if cwd, err := os.Getwd(); err == nil {
		fp.Cwd = cwd
	}

	return fp
}

// MarshalJSON serializes a Fingerprint, omitting empty fields.
func (fp *Fingerprint) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{}
	if fp.OSUser != "" {
		m["os_user"] = fp.OSUser
	}
	if fp.ParentProc != "" {
		m["parent_proc"] = fp.ParentProc
	}
	if len(fp.Harnesses) > 0 {
		m["harnesses"] = fp.Harnesses
	}
	if fp.Cwd != "" {
		m["cwd"] = fp.Cwd
	}
	return json.Marshal(m)
}

// FingerprintKey returns a canonical key string for matching.
func (fp *Fingerprint) Key() string {
	return fmt.Sprintf("%s|%s|%s", fp.OSUser, fp.ParentProc, strings.Join(fp.Harnesses, ","))
}

// GenerateToken creates a 32-byte random token (base64url encoded).
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns a SHA-256 hash of the token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// VerifyToken checks a token against its hash.
func VerifyToken(token, hash string) bool {
	return HashToken(token) == hash
}

// ResolveIdentity determines the asserted agent for this invocation.
// Returns identity info and an error if no identity can be determined.
func ResolveIdentity(db *sql.DB, tokenOverride string) (*IdentityInfo, error) {
	fp := ComputeFingerprint()
	info := &IdentityInfo{
		Fingerprint: fp,
	}

	// 1. Try token override (--token flag or MB_AGENT_TOKEN env)
	token := tokenOverride
	if token == "" {
		token = os.Getenv("MB_AGENT_TOKEN")
	}
	if token != "" {
		info.TokenPresent = true
		agents, err := listAgents(db)
		if err == nil {
			for _, a := range agents {
				if a.TokenHash != "" && VerifyToken(token, a.TokenHash) {
					info.Agent = a.Name
					info.Method = "token"
					createEvent(db, nil, a.Name, VerbAgentAsserted, `{"method":"token"}`)
					return info, nil
				}
			}
		}
		info.Method = "none"
		return info, fmt.Errorf("token does not match any registered agent")
	}

	// 2. Try fingerprint matching
	fpKey := fp.Key()
	var matches []*Agent
	agents, err := listAgents(db)
	if err == nil {
		for _, a := range agents {
			if a.Fingerprint != "" {
				var afp Fingerprint
				if err := json.Unmarshal([]byte(a.Fingerprint), &afp); err == nil {
					if afp.Key() == fpKey {
						matches = append(matches, a)
					}
				}
			}
		}
	}

	if len(matches) == 1 {
		info.Agent = matches[0].Name
		info.Method = "fingerprint"
		createEvent(db, nil, matches[0].Name, VerbAgentAsserted, `{"method":"fingerprint"}`)
		return info, nil
	}
	if len(matches) > 1 {
		info.Method = "ambiguous"
		// Read commands use lexicographically first, mutating commands refuse
		return info, nil
	}

	// 3. Fall back to MB_AGENT env var (discouraged)
	if envAgent := os.Getenv("MB_AGENT"); envAgent != "" {
		if _, err := getAgent(db, envAgent); err == nil {
			info.Agent = envAgent
			info.Method = "env"
			return info, nil
		}
	}

	info.Method = "none"
	return info, nil
}

// MustGetAgent returns the asserted agent or exits with an error.
// Call this for mutating commands.
func MustGetAgent(info *IdentityInfo) string {
	if info != nil && info.Agent != "" {
		return info.Agent
	}
	method := ""
	if info != nil {
		method = info.Method
	}
	switch method {
	case "ambiguous":
		fmt.Fprintf(os.Stderr, "error: ambiguous identity (multiple agents match this fingerprint). Use --token or --as to disambiguate.\n")
	default:
		// Covers "none" and an unset method (defensive — ResolveIdentity is
		// expected to always return a non-nil info now, but guard anyway).
		fmt.Fprintf(os.Stderr, "error: cannot determine agent identity. Run `mb agent register`, use --token, or set MB_AGENT.\n")
	}
	os.Exit(1)
	return ""
}
