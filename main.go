package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

var db *sql.DB
var globalStorePath string

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	// Parse global flags
	jsonOutput := false
	quiet := false
	globalStorePath = ""

	// Extract global flags from args (before subcommand)
	args := os.Args[1:]
	filtered := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "-q", "--quiet":
			quiet = true
		case "--store":
			if i+1 < len(args) {
				globalStorePath = args[i+1]
				i++
			} else {
				fmt.Fprintln(os.Stderr, "error: --store requires a path")
				os.Exit(1)
			}
		case "--help", "-h":
			usage()
			return
		default:
			filtered = append(filtered, args[i])
		}
	}
	args = filtered

	if len(args) == 0 {
		usage()
		return
	}

	out := NewOutput(jsonOutput, quiet)

	cmd := args[0]
	subargs := args[1:]

	// Commands that don't need a DB connection
	switch cmd {
	case "init":
		cmdInit(out, subargs)
		return
	case "help":
		usage()
		return
	}

	// All other commands need the DB
	var err error
	db, err = OpenDB(globalStorePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Alias shortcuts (kind-polymorphic)
	switch cmd {
	case "show":
		cmdShow(out, subargs)
		return
	case "claim":
		cmdClaim(out, subargs)
		return
	case "close":
		cmdClose(out, subargs)
		return
	case "open":
		cmdOpen(out, subargs)
		return
	case "prio":
		cmdPrio(out, subargs)
		return
	case "mv":
		cmdMv(out, subargs)
		return
	case "ls":
		// Default to task ls
		cmdTaskLs(out, subargs)
		return
	case "comment":
		cmdComment(out, subargs)
		return
	case "log":
		cmdLog(out, subargs)
		return
	case "whoami":
		cmdWhoami(out, subargs)
		return
	case "status":
		cmdStatus(out, subargs)
		return
	case "edit":
		cmdEdit(out, subargs)
		return
	}

	// Subcommand routing
	switch cmd {
	case "project":
		routeProject(out, subargs)
	case "task":
		routeTask(out, subargs)
	case "link":
		routeLink(out, subargs)
	case "unlink":
		routeUnlink(out, subargs)
	case "agent":
		routeAgent(out, subargs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `marbles (mb) — project/task manager

Usage:
  mb init                          Initialize store
  mb status                        Store health and stats
  mb ls [flags]                    List tasks (default)
  mb show <id>                     Show item details
  mb claim <id> [--as <agent>]     Claim an item
  mb close <id>                    Close an item
  mb open <id>                     Reopen an item
  mb prio <id> <priority>          Set priority
  mb mv <id> --project <P|--inbox> Move item
  mb edit <id> [--title ...] [--body ...]
  mb comment <id> <text>           Add comment
  mb log [<id>]                    Audit trail
  mb whoami                        Show asserted identity

  mb project ls|new|show|close|open|claim|unclaim|prio|mv|edit [flags]
  mb task ls|new|show|close|open|claim|unclaim|prio|promote|mv|edit [flags]
  mb link <a> <rel> <b>           Create link (rel: blocks, related, parent, child)
  mb link ls [<id>] [--rel <r>]
  mb unlink <a> <rel> <b>
  mb agent register|ls|assert

Global flags:
  --json         Machine-readable JSON output
  --quiet, -q    Errors only
  --store <path> Override store path

Priority: critical (0), high (1), med (2), low (3)
`)
}

// resolveDisplayID returns (id, kind, error) from a kind-prefixed or bare ID string.
func resolveDisplayID(s string) (int64, string, error) {
	s = strings.TrimSpace(s)
	var numStr string
	kind := ""

	if strings.HasPrefix(s, "T") || strings.HasPrefix(s, "t") {
		kind = "task"
		numStr = strings.TrimLeft(s, "Tt")
	} else if strings.HasPrefix(s, "P") || strings.HasPrefix(s, "p") {
		kind = "project"
		numStr = strings.TrimLeft(s, "Pp")
	} else {
		numStr = s
	}

	var id int64
	if _, err := fmt.Sscanf(numStr, "%d", &id); err != nil {
		return 0, "", fmt.Errorf("invalid id: %q", s)
	}
	return id, kind, nil
}

// resolveItem looks up an item by ID and returns it, along with its canonical display ID.
func resolveItem(db *sql.DB, id int64) (*Item, error) {
	return getItem(db, id)
}
