# marbles (`mb`)

A focused, CLI-level project/task manager for **humans and agents**. Multiple
agents share a single store, each asserting its own identity. No memory layer,
no issue-type taxonomy, no arbitrary metadata — just **projects**, **tasks**,
**links**, **comments**, and an **append-only audit trail**.

`marbles` is the long name; `mb` is the binary. The full design lives in
[`SPEC.md`](SPEC.md).

## Why marbles?

- **Two entity kinds only.** Tasks and projects. A task can be *promoted* to a
  project on the same numeric ID — history, comments, and claims survive
  intact.
- **Multi-agent by design.** One shared SQLite store, asserted identity via
  environment fingerprinting and tokens, and an audit trail that records who
  did what, when.
- **Typed links.** `blocks`, `related`, `parent`/`child` — with cycle
  enforcement on the structural ones.
- **No destructive deletes, ever.** Closing is the terminal state; the audit
  trail is append-only.
- **Machine-friendly.** Every command supports `--json` for agents, scripts,
  and CI.

## Concepts

| Concept   | Description                                                                 |
|-----------|-----------------------------------------------------------------------------|
| **Project** | A container for tasks (and optionally sub-projects). `P3`, `P12`, …      |
| **Task**    | A unit of work inside a project — or the **inbox** if it has no project. `T7`, `T12`, … |
| **Link**    | A typed edge between items: `blocks`, `related`, `parent`, `child`.      |
| **Comment** | Immutable notes on an item. To correct, add a new one.                   |
| **Event**   | Append-only audit trail: every mutation is a row.                        |

IDs are stable integers, prefixed by current kind (`T12` / `P3`). Lookups are
kind-tolerant: `T12`, `P12`, or bare `12` all work anywhere an ID is accepted.

## Installation

### 1. Build / install the CLI

```sh
go install github.com/edwardchampion/marbles@latest
```

This puts `mb` in `~/go/bin/mb` (make sure `~/go/bin` is on your `PATH`).
Alternatively, build from a checkout:

```sh
go build -o mb .
```

### 2. Initialize the store

The store at `~/.marbles/` is auto-created on first use; `mb init` does it
explicitly:

```sh
mb init        # creates ~/.marbles/{db.sqlite, config.toml, identities/}
mb status      # store health: path, item counts, asserted agent
```

## Quick start

```sh
# Projects
mb project new "Website Redesign" --claim
mb project ls

# Tasks
mb task new "Write copy for homepage" --project P1 --priority high
mb task new "Fix footer on mobile" --project P1 --blocks T2
mb task ls --project P1

# Workflow
mb claim T2              # alias for mb task claim
mb show T2               # item + links + events + comments
mb comment T2 "blocked on design assets"
mb close T2              # done — kept forever in the audit trail
mb log T2                # per-item audit trail
mb log                   # global audit trail

# Links
mb link T3 blocks T4
mb link ls T3

# Promotion: a task becomes a project, same ID, full history
mb task promote T5       # T5 → P5, still in the same project as a sub-project
```

## CLI overview

```
mb init                          Initialize store
mb status                        Store health and stats
mb ls [flags]                    List tasks (default)
mb show <id>                     Show item details
mb claim <id> [--as <agent>]     Claim an item
mb close <id>                    Close an item
mb open <id>                     Reopen an item
mb prio <id> <priority>          Set priority (critical|high|med|low)
mb mv <id> --project <P|--inbox> Move item
mb edit <id> [--title ...] [--body ...]
mb comment <id> <text>           Add comment ("-" reads stdin)
mb log [<id>]                    Audit trail
mb whoami                        Show asserted identity

mb project ls|new|show|close|open|claim|unclaim|prio|mv|edit [flags]
mb task ls|new|show|close|open|claim|unclaim|prio|promote|mv|edit [flags]
mb link <a> <rel> <b>            rel: blocks|related|parent|child
mb link ls [<id>] [--rel <r>]
mb unlink <a> <rel> <b>
mb agent register|ls|assert

Global flags:
  --json         Machine-readable JSON output
  --quiet, -q    Errors only
  --store <path> Override store path
```

Ergonomics: `mb claim`/`show`/`close`/`open`/`prio`/`mv` are
kind-polymorphic — they dispatch on the item's current kind, so you don't need
to remember whether something is a task or a project. Default listings show
open items only, sorted by priority.

## Identity

There is no auth server. The trust boundary is **filesystem access to
`~/.marbles/`**; within it, marbles *asserts* identity from signals it reads
itself rather than trusting a self-set env var:

- `mb agent register <name>` mints a token, prints it **once**, stores only
  its hash.
- On each invocation marbles computes a **fingerprint** (OS user, parent
  process, harness env-var *presence*, cwd) and matches it against registered
  agents. A unique match asserts automatically — no env vars needed.
- No match → falls back to a presented token (`MB_AGENT_TOKEN` or `--token`).
- Ambiguous → mutating commands refuse until you disambiguate with `--token`
  or `--as`.
- Every mutating event records the asserted actor. If identity can't be
  determined, mutating commands fail with a clear message; reads still work.

## pi extension

A [pi](https://github.com/earendil-works/pi) extension is included
in [`pi-extension/`](pi-extension/README.md) that bridges `mb` into agent
sessions:

- A `marbles` tool the agent calls to pick a project and claim/close/open/
  comment on tasks — `close` can auto `git add && commit && push`.
- A live widget above the editor showing the current project's tasks as
  checkboxes (☐ open, ● working-on, ☑ done), with priorities, claims, and
  blocking markers.
- Auto-registers a `pi` agent and caches its token on first use.

Install with:

```sh
pi install git:github.com/edwardchampion/marbles@v1
```

See [`pi-extension/README.md`](pi-extension/README.md) for details.

## Storage

- `~/.marbles/db.sqlite` — SQLite store (WAL mode). One global namespace; all
  items live here. Not per-repo, not in source control.
- `~/.marbles/config.toml` — defaults (default agent, output format).
- `~/.marbles/identities/` — per-agent credential material.

Scope a working set with **projects**, not store locations. A project may
record a `cwd_hint`, so `mb task ls --here` can scope to your current
directory.

## Design principles

- **Small schema.** One `items` table holds tasks and projects; promotion is a
  one-row reclassification that preserves every ID, comment, and event.
- **Append-only.** No deletes. `closed` is the terminal state; corrections are
  new comments.
- **Scope discipline.** Milestones, sprints, tags, metadata maps, and memory
  features are explicitly out of scope — a new capability must compose from
  the existing five concepts (project, task, link, comment, event) or it
  doesn't get in. See `SPEC.md` §9 for the full anti-sprawl list.

## Development

```sh
go build -o mb .            # build
go test ./...               # run tests (identity, promotion, link cycles)
cd pi-extension && tsc --noEmit -p tsconfig.json   # typecheck the extension
```

## License

MIT
