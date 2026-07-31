/**
 * Marbles CLI client.
 *
 * Thin wrapper around the `mb` binary that runs commands with JSON output and
 * asserts the marbles agent identity via a token (deterministic — not subject
 * to fingerprint ambiguity). The token is minted on first use by registering a
 * "pi" marbles agent and cached on disk.
 *
 * ## Store path
 *
 * By default all commands operate on `~/.marbles/db.sqlite`.  Set the
 * `MARBLES_STORE` environment variable to an alternate database path for
 * testing (e.g.  `MARBLES_STORE=/tmp/test.db`).  The `--store <path>` flag is
 * transparently added to every `mb` invocation.
 */
import { execFile } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";

const HOME = homedir();
const MARBLES_DIR = path.join(HOME, ".marbles");
const TOKEN_FILE = path.join(MARBLES_DIR, "identities", "pi-agent-token");
export const AGENT_NAME = "pi";

export interface MbItem {
	id: number;
	kind: "task" | "project";
	title: string;
	body?: string;
	status: "open" | "review" | "closed";
	priority: number; // 0..3
	claimed_by?: string | null;
	parent_item?: number | null;
	cwd_hint?: string | null;
	child_count?: number;
	open_count?: number;
	closed_at?: number;
	blocks?: number[];
	blocked_by?: number[];
}

export interface MbShow {
	item: MbItem;
	links: { id: number; from_item: number; to_item: number; rel: string }[];
	comments: { id: number; author: string; body: string; created_at: number }[] | null;
	events: { id: number; actor: string; verb: string; detail: string; at: number }[];
}

export class MbError extends Error {}

// ---------------------------------------------------------------------------
// Binary discovery
// ---------------------------------------------------------------------------
function mbBinary(): string {
	// 1. Check for `mb` relative to this extension's location.
	const selfDir =
		typeof __dirname !== "undefined"
			? __dirname
			: typeof import.meta !== "undefined" && import.meta.dirname
				? import.meta.dirname
				: undefined;
	if (selfDir) {
		const alongside = path.resolve(selfDir, "..", "mb");
		if (existsSync(alongside)) return alongside;
	}
	// 2. Check common install locations for `go install`.
	const candidates = [
		path.join(HOME, "go", "bin", "mb"),
		path.join(HOME, ".local", "bin", "mb"),
		"/usr/local/bin/mb",
		"/opt/homebrew/bin/mb",
	];
	for (const c of candidates) {
		if (c.startsWith("/") && existsSync(c)) return c;
	}
	// 3. Last resort: rely on PATH.
	return "mb";
}

// ---------------------------------------------------------------------------
// Standalone helpers (used by MbClient under the hood)
// ---------------------------------------------------------------------------

/**
 * Run an `mb` command, returning stdout.
 *
 * @param storePath  if non-empty, `--store <path>` is prepended to args.
 */
function run(args: string[], env: NodeJS.ProcessEnv, storePath: string, timeoutMs = 15000): Promise<string> {
	const fullArgs = storePath ? ["--store", storePath, ...args] : args;
	return new Promise((resolve, reject) => {
		execFile(
			mbBinary(),
			fullArgs,
			{ env, timeout: timeoutMs, maxBuffer: 4 * 1024 * 1024 },
			(err, stdout, stderr) => {
				if (err) {
					reject(new MbError((stderr || err.message || "").trim()));
					return;
				}
				resolve(stdout);
			},
		);
	});
}

async function tokenWorks(token: string, storePath: string): Promise<boolean> {
	try {
		const env = { ...process.env, MB_AGENT_TOKEN: token };
		const out = await run(["whoami"], env, storePath, 5000);
		return out.includes("via token");
	} catch {
		return false;
	}
}

/** Parse the created id from stdout like "Created task T2" / "Created project P1". */
export function parseCreatedId(out: string): number | null {
	const m = out.match(/Created (?:task|project) [TP](\d+)/i);
	return m ? Number(m[1]) : null;
}

export function dispID(item: MbItem): string {
	return `${item.kind === "project" ? "P" : "T"}${item.id}`;
}

// ---------------------------------------------------------------------------
// Git helpers
// ---------------------------------------------------------------------------

/**
 * Run git add -A, git commit -m <msg>, and git push in the current working
 * directory.  Returns a summary of what happened.  Throws on hard failures
 * (e.g. not a git repo, push rejected).  "nothing to commit" is treated as
 * a soft no-op, not a failure.
 */
export async function gitCommitAndPush(commitMessage: string): Promise<string> {
	const cwd = process.cwd();
	const lines: string[] = [];

	// -- git add -A --------------------------------------------------------
	await new Promise<void>((resolve, reject) => {
		execFile("git", ["add", "-A"], { cwd, timeout: 30_000 }, (err, _stdout, stderr) => {
			if (err) reject(new Error(`git add failed: ${(stderr || err.message).trim()}`));
			else resolve();
		});
	});
	lines.push("✓ git add -A");

	// -- git commit --------------------------------------------------------
	const commitOut = await new Promise<string>((resolve, reject) => {
		execFile("git", ["commit", "-m", commitMessage], { cwd, timeout: 30_000 }, (err, stdout, stderr) => {
			const combined = ((stdout ?? "") + (stderr ?? "")).trim();
			if (err) {
				// "nothing to commit" is a soft no-op
				if (/nothing to commit/i.test(combined)) {
					resolve("(nothing to commit — already clean)");
				} else {
					reject(new Error(`git commit failed: ${combined || err.message}`));
				}
			} else {
				resolve(combined.split("\n")[0] ?? "committed");
			}
		});
	});
	lines.push(`✓ git commit: ${commitOut}`);

	// -- git push ----------------------------------------------------------
	const pushOut = await new Promise<string>((resolve, reject) => {
		execFile("git", ["push"], { cwd, timeout: 30_000 }, (err, stdout, stderr) => {
			const combined = ((stdout ?? "") + (stderr ?? "")).trim();
			if (err) reject(new Error(`git push failed: ${combined || err.message}`));
			else resolve(combined.split("\n")[0] ?? "pushed");
		});
	});
	lines.push(`✓ git push: ${pushOut}`);

	return lines.join("\n");
}

// ---------------------------------------------------------------------------
// MbClient
// ---------------------------------------------------------------------------

export class MbClient {
	private token: string | null = null;
	private storePath: string;

	/**
	 * @param storePath  optional database path (overrides MARBLES_STORE env).
	 *                   Falls back to process.env.MARBLES_STORE, then default.
	 */
	constructor(storePath?: string) {
		this.storePath = storePath ?? process.env.MARBLES_STORE ?? "";
	}

	/** The store path this client was configured with (empty = default). */
	get store(): string {
		return this.storePath;
	}

	// -- Identity bootstrap --------------------------------------------------

	async init(): Promise<void> {
		if (this.token) return;
		this.token = await this.ensureToken();
	}

	private async ensureToken(): Promise<string> {
		if (existsSync(TOKEN_FILE)) {
			const tok = readFileSync(TOKEN_FILE, "utf8").trim();
			if (tok && (await tokenWorks(tok, this.storePath))) return tok;
		}
		mkdirSync(path.dirname(TOKEN_FILE), { recursive: true });
		const baseEnv = { ...process.env };
		const out = await run(["agent", "register", AGENT_NAME], baseEnv, this.storePath);
		const lines = out.split("\n").map((l) => l.trim()).filter(Boolean);
		const token = lines[lines.length - 1] ?? "";
		if (!token) throw new MbError("could not parse token from `mb agent register` output");
		writeFileSync(TOKEN_FILE, token, { mode: 0o600 });
		return token;
	}

	// -- Low-level helpers ---------------------------------------------------

	private env(): NodeJS.ProcessEnv {
		return { ...process.env, MB_AGENT_TOKEN: this.token ?? "", MB_AGENT: AGENT_NAME };
	}

	async json<T>(args: string[]): Promise<T> {
		await this.init();
		const out = await run(["--json", ...args], this.env(), this.storePath);
		const trimmed = out.trim();
		if (!trimmed) return [] as unknown as T;
		try {
			return JSON.parse(trimmed) as T;
		} catch (e) {
			throw new MbError(`bad JSON from \`mb ${args.join(" ")}\`: ${(e as Error).message}`);
		}
	}

	async exec(args: string[]): Promise<string> {
		await this.init();
		return run(args, this.env(), this.storePath);
	}

	// -- Public API ----------------------------------------------------------

	listProjects(): Promise<MbItem[]> {
		return this.json<MbItem[]>(["project", "ls", "--closed"]);
	}

	listTasks(project: number): Promise<MbItem[]> {
		// Return all tasks (open + review + closed) so the widget can decide
		// which subset to render.
		return this.json<MbItem[]>(["task", "ls", "--project", `P${project}`, "--review", "--closed"]);
	}

	show(id: number): Promise<MbShow> {
		return this.json<MbShow>(["show", String(id)]);
	}

	async claim(id: number): Promise<string> {
		return this.exec(["claim", `T${id}`]);
	}
	async close(id: number): Promise<string> {
		return this.exec(["close", String(id)]);
	}
	async open(id: number): Promise<string> {
		return this.exec(["open", String(id)]);
	}
	async newTask(title: string, project?: number): Promise<string> {
		const args = ["task", "new", title];
		if (project !== undefined) args.push("--project", `P${project}`);
		return this.exec(args);
	}
	async newProject(title: string): Promise<string> {
		return this.exec(["project", "new", title, "--claim"]);
	}
	async comment(id: number, body: string): Promise<string> {
		return this.exec(["comment", String(id), body]);
	}
}
