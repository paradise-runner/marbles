# marbles — pi extension

Bridges the [`mb`](../../SPEC.md) (marbles) CLI task/project manager into a pi agent session.

## What it does

- Registers a **`marbles`** tool the LLM can call: `list_projects`,
  `set_project`, `current`, `list_tasks`, `claim`, `close`, `open`, `review`,
  `comment`, `new_task`, `new_project`. `close` accepts an optional
  `commit_message` — when provided, the extension automatically runs
  `git add -A && git commit -m "..." && git push` so the agent doesn't need
  to shell out for git. The agent uses this instead of shelling out to
  `mb`, so the UI tracks progress.
- Renders a **widget above the editor** showing the current project and its
  tasks as checkboxes:
  - `☑` done, `●` working-on (claimed by pi), `☐` open unclaimed
  - priority, `@agent` claims, `► blocked by` / `◄ blocks` markers
- Shows a **footer status** line: `◧ P1 Website Redesign  2/5`
- **Auto-refreshes** after `marbles` tool calls, after `mb` shell calls, and at
  the end of every turn.
- Injects a short context message each turn coaching the agent to claim before
  working and close when done.
- The selected project is **persisted** to the session, so it survives
  `/resume` and forks.
- `/marbles` command lets you pick the tracked project interactively.

## Setup

### 1. Install the `mb` CLI

```sh
go install github.com/edwardchampion/marbles@latest
```

This puts `mb` in `~/go/bin/mb`. Make sure `~/go/bin` is on your `PATH`, or
the extension will find it there automatically.

### 2. Install the pi extension

**Option A — from the repo (recommended):**

```sh
pi install git:github.com/edwardchampion/marbles@v1
```

**Option B — from a local checkout:**

```sh
cd ~/git/marbles
pi install .
```

**Option C — auto-discovery (development):**

If you have the repo cloned, symlink it into pi's extension directory:

```sh
ln -s ~/git/marbles/pi-extension ~/.pi/agent/extensions/marbles
```

Then `/reload` in pi.

### 3. First use

On the first `marbles` tool call, the extension auto-registers a marbles agent
named `pi` and caches its token at `~/.marbles/identities/pi-agent-token`.
All `mb` calls assert identity via `MB_AGENT_TOKEN`, so attribution is
deterministic.

## Type-check (optional)

```sh
cd ~/git/marbles/pi-extension
tsc --noEmit -p tsconfig.json
```

## Files in this directory

- `index.ts` — extension logic (tool, widget, lifecycle hooks)
- `client.ts` — `mb` CLI JSON client + identity bootstrap
- `tsconfig.json` — typecheck config (uses the globally-installed pi packages)

## How the `mb` binary is found

`client.ts` looks for `mb` in this order:

1. **Alongside the extension** — when installed as a pi package from the git
   repo, the binary sits at the repo root (`../mb` relative to this directory).
2. **Common install locations** — `~/go/bin/mb`, `~/.local/bin/mb`,
   `/usr/local/bin/mb`, `/opt/homebrew/bin/mb`.
3. **PATH lookup** — last resort; relies on `mb` being on the shell `PATH`.