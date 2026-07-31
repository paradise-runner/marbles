# marbles (`mb`) — Specification

A focused, CLI-level project/task manager for humans and agents. Multiple
agents share a single store. No memory layer, no issue-type taxonomy, no
arbitrary metadata maps. Just **projects**, **tasks**, **links**,
**comments**, and an **append-only audit trail**.

`marbles` is the long name; `mb` is the binary. This doc is the source of
truth for v1. Anything not listed here is out of scope for v1.

---

## 1. Goals and non-goals

**Goals**

- Replace the sprawling parts of `beads` with a tight, agent-friendly CLI.
- Two entity kinds only: **task** and **project**. Promotion converts one to
  the other without losing history.
- Multi-agent by design: one shared store, asserted identity, audit trail.
- Dependency and structural links: `blocks`, `related`, `parent`/`child`.
- Append-only history; no destructive deletes, ever.

**Non-goals (explicitly excluded to avoid beads-style sprawl)**

- No memory/knowledge store, no episodic or semantic memory.
- No issue-type taxonomy beyond task vs. project.
- No freeform metadata map on items. Schema is fixed and small.
- No milestones, phases, sprints, or tags/labels. (See §9 for revisit rules.)
- No web UI, no sync server, no multi-host replication.
- No destructive delete. Closed items are retained permanently.

---

## 2. Storage and location

- **One global store**, located at `~/.marbles/`. Not per-repo; not checked
  into source control.
  - `~/.marbles/db.sqlite` — the database (SQLite, WAL mode).
  - `~/.marbles/config.toml` — user defaults (default agent, output format).
  - `~/.marbles/identities/` — per-agent credential material (see §8).
- The store is auto-created on first use if absent; `mb init` does it
  explicitly and prints the path.
- **All work items live in a single global namespace.** Scope to a repo or
  working set is done via **projects**, not via store location. A project may
  optionally record a `cwd_hint` (a directory prefix) so commands like
  `mb task ls --here` can scope by current directory, but items are not
  partitioned by filesystem location.

**Concurrency.** SQLite with WAL. Every mutating CLI invocation wraps its
work in a single short transaction with `BEGIN IMMEDIATE`. Optimistic enough
for the scale we care about (a handful of agents on one machine). Conflicts
on `claimed_by` are resolved last-writer-wins but both writes appear in the
audit trail.

---

## 3. Entity model

A single `items` table holds both tasks and projects. Distinguished by a
`kind` column. This makes promotion (§5) a trivial reclassification that
preserves all history.

### 3.1 `items`

| column         | type    | notes |
|----------------|---------|-------|
| `id`           | INTEGER | PRIMARY KEY, autoincrement. **Stable forever; never renumbered.** |
| `kind`         | TEXT    | `'task'` or `'project'`. Mutable *only* by `promote`. |
| `title`        | TEXT    | Required, non-empty. Editable. |
| `body`         | TEXT    | Optional long description. Editable. |
| `status`       | TEXT    | `'open'` or `'closed'`. Default `'open'`. |
| `priority`     | INTEGER | Stored as rank: `0=critical, 1=high, 2=med, 3=low`. Default `2`. |
| `claimed_by`   | TEXT    | Agent handle, or NULL. NULL = unclaimed. |
| `parent_item`  | INTEGER | NULLable FK→items.id. The project this item lives in. Tasks have a parent project (or NULL = inbox). Projects may have a parent project (sub-project) or NULL = top-level. |
| `cwd_hint`      | TEXT    | NULL or a directory prefix recorded at project creation for `--here` scoping. Tasks inherit from their parent project. |
| `created_at`   | INTEGER | unix epoch seconds.
| `created_by`   | TEXT    | agent handle that created it.
| `updated_at`   | INTEGER | unix epoch seconds; bumped on any mutation.
| `closed_at`    | INTEGER | NULL until closed; set when status→closed.

**Invariants**

- `kind` is only mutable via the `promote` command. All other mutations
  reject changing `kind`.
- `status ∈ {open, closed}`. Closing sets `closed_at`; reopening clears it.
- A project's `parent_item`, if set, must point to another project
  (`kind='project'`). A task's `parent_item`, if set, must point to a project.
  NULL parent for a task = **inbox**; NULL parent for a project = top-level.
- No cycles in `parent_item` (enforced on write).
- `claimed_by` is a single value or NULL; there is no separate "assigned"
  field. Claiming sets it; unclaiming clears it. Re-claiming by a different
  agent overwrites (both events logged in audit trail).

### 3.2 `links`

Typed edges between two items. Directional where the relation is asymmetric,
symmetric entries used for symmetric relations.

| column      | type    | notes |
|-------------|---------|-------|
| `id`        | INTEGER | PRIMARY KEY. |
| `from_item` | INTEGER | FK→items.id. |
| `to_item`   | INTEGER | FK→items.id. |
| `rel`       | TEXT    | One of `blocks`, `related`, `parent`, `child`. |
| `created_at`| INTEGER | epoch. |
| `created_by`| TEXT    | agent handle. |

Curated relations:

- `blocks` — `from` blocks `to` (i.e. `from` must be done before `to`).
  Asymmetric. CLI sugar: `mb task new ... --blocks T7` creates
  `from=new, rel=blocks, to=T7`.
- `related` — undirected; stored as one row with `rel='related'`, lookup is
  symmetric. Enforced unique unordered pair.
- `parent` / `child` — derived *also* from the `parent_item` column for ease
  of querying, but link rows exist too so the audit trail and `link ls` see
  them. `parent_item` is the canonical source of truth; the `parent`/`child`
  link rows are kept in sync (a trigger or app-level code maintains them on
  every `parent_item` change and on `promote`).

**Uniqueness/constraints**

- `(from_item, to_item, rel)` unique. Re-creating an existing link is a
  no-op (success, idempotent), not an error.
- No self-links. No cycles in `blocks` (enforced; `mb link` rejects a `blocks`
  edge that would introduce a cycle).
- No cycles in `parent`/`child`.

### 3.3 `comments`

| column      | type    | notes |
|-------------|---------|-------|
| `id`        | INTEGER | PRIMARY KEY. |
| `item`      | INTEGER | FK→items.id. |
| `author`    | TEXT    | asserted agent handle (see §8). |
| `body`      | TEXT    | single-line or multi-line; taken raw. |
| `created_at`| INTEGER | epoch. |

Comments are immutable. To correct, add a new comment. (Keeps the audit trail
clean and the schema tiny.)

### 3.4 `events` (audit trail)

Append-only. One row per mutation event. This is the audit trail that `show`
renders.

| column      | type    | notes |
|-------------|---------|-------|
| `id`        | INTEGER | PRIMARY KEY. |
| `item`      | INTEGER | FK→items.id (the affected item; NULLable for global events like agent registration). |
| `actor`     | TEXT    | asserted agent handle. |
| `verb`      | TEXT    | see verbs below. |
| `detail`    | TEXT    | JSON string describing the change (old/new values, link endpoints, etc.). Freeform but machine-parseable. |
| `at`        | INTEGER | epoch. |

**Verbs:** `created`, `closed`, `reopened`, `reprio` (priority change),
`claimed`, `unclaimed`, `reclaimed`, `title_edited`, `body_edited`,
`moved` (`parent_item` change), `promoted` (`kind` task→project), `linked`,
`unlinked`, `commented`, `agent_registered`, `agent_asserted`.

There is no `deleted` verb. Closing is the terminal state.

### 3.5 `agents`

Records registered agents and the credential material needed to assert
identity (see §8). Column details in §8.2.

---

## 4. IDs and display

- IDs are **stable integers** from the `items` table. Prefixed in display by
  current kind: `T12` (task) or `P3` (project).
- **Lookups are kind-tolerant.** Anywhere a command takes `<id>`, it accepts
  `T12`, `P12`, or bare `12`. The number is the identity; the prefix is
  advisory. When a stale `T12` reference resolves to an item that is now a
  project, marbles reports the canonical form (`P12`) so the caller knows it
  was promoted.
- Bare-number lookup is unambiguous because IDs are globally unique across
  tasks and projects (single `items` table).
- Display ordering: integer order ≈ creation order.

---

## 5. Promotion: task → project

Goal: a task can become a project, with its own tasks. The original task
*disappears from task lists* (b in the original brainstorm), yet the audit
trail and comments are fully preserved on the same numeric ID.

Mechanics:

1. `mb task promote <id>` (alias `mb task promote T12`):
   - Asserts target `kind='task'` and `status='open'`.
   - `UPDATE items SET kind='project' WHERE id=...`.
   - If the promoted task had `parent_item=P3`, P12 remains a child of P3 via
     `parent_item` (now a sub-project). The relationship is preserved, not
     dropped — sub-projects are allowed.
   - Writes an `events` row: `verb='promoted'`, `detail={'from':'task','to':'project'}`.
   - Future `mb task ls` no longer lists it; `mb project ls` does.
2. The new project starts empty of tasks. You then `mb task new --project P12`.
3. The project inherits `claimed_by`, `priority`, `cwd_hint`, comments, and
   the entire event history of the former task — all on the same row, so
   nothing is copied or lost.

Because the row's integer ID is unchanged, every prior `events` and
`comments` row already references it correctly. There is no migration step.
Promotion is reversible in principle (`mb project demote` is **not** in v1 —
flagged for later — because demoting a non-empty project would orphan its
tasks).

---

## 6. CLI surface (v1)

Conventions:
- `% mb <command>` runs a command. Subcommands grouped under `project`,
  `task`, `agent`, `link`.
- Global flags: `--json` (machine output, agents/CI), `--quiet`/`-q`
  (errors-only), `--store <path>` (override `~/.marbles/db.sqlite`).
- Default output is human-readable tables; `--json` emits deterministic JSON.
- "mine" filters use the currently asserted agent (§8).

### 6.1 Store / setup

```
mb init                 # ensure ~/.marbles/ exists and db created; print path
mb status               # store health: path, item counts, asserted agent
```

### 6.2 Projects

```
mb project ls [--open] [--closed] [--mine] [--claimed] [--unclaimed]
              [--parent P] [--top]
              [--sort created|priority|title]
              [--here]
mb project new "Title" [--body ...] [--priority low|med|high|critical]
              [--parent P] [--claim] [--cwd .]
mb project show <id>            # item + links + recent events + comments
mb project close <id>
mb project open <id>
mb project claim <id> [--as <agent>]   # alias: mb claim <id>
mb project unclaim <id>
mb project prio <id> <low|med|high|critical>
mb project mv <id> --parent <P>   # reparent a sub-project (orphan → top via --top)
mb project edit <id> [--title ...] [--body ...]
```

### 6.3 Tasks

```
mb task ls [--project P] [--open] [--closed] [--mine] [--claimed] [--unclaimed]
           [--sort created|priority|title]
           [--blocks-by T] [--blocks T] [--here]
mb task new "Title" [--project P] [--body ...]
            [--priority low|med|high|critical]
            [--blocks T] [--blocked-by T] [--related T]
            [--claim] [--as <agent>]
mb task show <id>              # item + links (incl blocking/blocked-by) + recent events + comments
mb task close <id>
mb task open <id>
mb task claim <id> [--as <agent>]        # alias: mb claim <id>
mb task unclaim <id>
mb task prio <id> <low|med|high|critical>
mb task promote <id>          # task → project on same ID; see §5
mb task mv <id> --project <P> # move to a different project; --inbox clears
mb task edit <id> [--title ...] [--body ...]
```

### 6.4 Links

```
mb link <a> <rel> <b>          # rel ∈ blocks|related|parent|child
                              # e.g. mb link T4 blocks T7
mb link ls [<id>] [--rel <r>]  # links touching <id>, or all if omitted
mb unlink <a> <rel> <b>
```

Notes:
- `parent`/`child` links are maintained in sync with `items.parent_item`; you
  may set them via `mb link` for symmetry, but `parent_item` is canonical.

### 6.5 Comments and audit

```
mb comment <id> "text"        # may read from stdin if "text" is "-"
mb log [<id>]                 # audit trail; global if no id, per-item if id
                              # use --json for full event detail
```

### 6.6 Identity

```
mb whoami                     # asserted agent + how derived (fingerprint|token|ambiguous)
mb agent register <name>      # mint token, print plaintext once, store hash
mb agent ls                   # known agents and their fingerprint hints
mb agent assert --token <t>   # validate token and set asserted agent for this invocation
```

(Identity details in §8.)

### 6.7 Aliases and ergonomics

- `mb claim <id>` works for both tasks and projects (reads `kind`).
- `mb show T12` is an alias for `mb task show`/`mb project show` based on the
  item's current kind.
- `mb ls` defaults to `mb task ls` (the common case).
- `mb close <id>`, `mb open <id>`, `mb prio <id> <p>`, `mb mv <id> ...` —
  kind-polymorphic shortcuts dispatched on the item's current kind.

---

## 7. Listing, sorting, defaults

- Default `ls` shows open items only; `--closed` adds closed; `--open` is a
  no-op default flag for explicitness. There is no `--all`; combine
  `--open --closed` mentally via `--all` if we add it later. For v1, `--closed`
  means "include closed"; `--open` means "include open" (default-on).
- Default sort: `priority` (critical first, then high, med, low), ties broken
  by `created` ascending.
- Projects list shows child-task counts and total-open-count.
- Task list shows `claimed_by` (or `-`), `priority`, `blocks`/`blocked-by`
  markers (► blocked-by X, ◄ blocks Y) and project id in a column.
- `--here` scopes to items whose `cwd_hint` (project) or parent project's
  `cwd_hint` is a prefix of the current working directory.

---

## 8. Identity and fingerprinting

### 8.1 Threat model (honest)

There is no auth server. Anyone with read access to `~/.marbles/` can fabricate
or steal credential material. The trust boundary is **filesystem access to
`~/.marbles/`**. Within that boundary, the goal is that marbles *asserts*
identity from signals it reads itself, rather than trusting a self-set
environment variable. This prevents accidental cross-agent contamination (the
common failure mode) but not a malicious filesystem-capable actor.

### 8.2 Registration: `mb agent register <name>`

- Mints a 32-byte random token, base64url.
- Prints the plaintext token **once**.
- Stores in `agents`:
  - `name` (unique)
  - `token_hash` (argon2id or sha256-salted; never store plaintext)
  - `fingerprint` (a JSON profile the agent presents; see 8.3), optional
  - `created_at`, `created_by`

### 8.3 Fingerprint (marbles-asserted)

When invoked, marbles computes a **fingerprint** from the calling environment
without trusting the caller:

- OS user: `getuid()` / `whoami`.
- Real parent process: best-effort read of the parent PID's command line
  (`/proc/<ppid>/cmdline` on Linux; `ps`-based on macOS).
- Harness sentinel env vars: presence of `CLAUDE_*`, `PI_*`, `CODEX_*`,
  `AIDER_*`, etc. (Names keyed to known agent harnesses; list extensible in
  config.) **Only presence/absence is read, never values.**
- `cwd` at invocation.

A registered agent's `fingerprint` is the set of these signals that identify
it. On each invocation marbles computes the same signals and matches against
registered agents:

- **Unique match** → asserted automatically. No env var, no token needed.
  `mb whoami` reports `asserted via fingerprint`.
- **No match** → marbles falls back to a presented token
  (`MB_AGENT_TOKEN` env or `--token`). Verifies against `token_hash`.
  `whoami` reports `asserted via token`.
- **Ambiguous match** (multiple agents share a fingerprint) → marbles refuses
  any mutating command and instructs the caller to disambiguate with
  `--token` or `--as`. Read commands default to the lexicographically-first
  candidate but warn. (This is rare in practice; resolved by giving agents
  distinct fingerprints at registration.)

### 8.4 `MB_AGENT` (legacy/optional)

The bare env var `MB_AGENT=<name>` is honored **only** when its value matches
a registered agent *and* no fingerprint match exists and no token is
presented — i.e. as a last-resort hint, never an override. We document it as
discouraged.

### 8.5 Attribution

Every mutating event records `actor` = the asserted handle. If assertion
fails entirely (no match, no token, no `MB_AGENT`), mutating commands exit
non-zero with a clear message ("cannot determine agent identity; run `mb agent register` / `--token` / set `MB_AGENT`"). Read commands still work
unattributed.

---

## 9. Scope discipline (anti-sprawl rules)

To prevent the beads drift back, the following are **forbidden in v1** and
require a written spec amendment to add:

- Any new entity kind beyond task and project.
- Any freeform/per-item metadata map or key-value payload.
- Memory/notes-as-knowledge-store features.
- Milestones, phases, sprints, iterations, releases as entities.
- Tags or labels (use `related` links or project grouping instead).
- Destructive deletes (`close` only).
- Demoting a non-empty project back to a task.
- A second store/multi-workspace/multi-host sync.
- Authentication beyond the §8 model.

When a new capability seems needed, the bar is: it must compose from the
existing five concepts (project, task, link, comment, event). If it needs a
sixth, it is out of scope.

---

## 10. Open questions deferred to after v1

- Demotion (project → task) once we agree on child-orphaning rules.
- A genuine `archive` flag to hard-hide closed items from default listings
  (today `--closed` shows all closed; archive would add a third state).
- Per-agent先进集体 "in progress" view and notifications on claim
  contention.
- Concurrent-edit resolution beyond last-writer-wins (e.g. advisory locking
  on long-running agent sessions).
- IPC for long-running daemon mode (today every invocation is a fresh
  process; fine for our scale).

---

## 11. Implementation notes (non-normative)

- Reference implementation language: TBD (Rust or Go recommended for single
  static binary + good SQLite bindings; Python acceptable for v0).
- SQLite pragmas: `journal_mode=WAL`, `foreign_keys=ON`,
  `busy_timeout=5000`.
- Schema migrations: a lightweight `schema_version` table with
  forward-only additive migrations. No destructive migrations in v1.
- Fingerprint reading must degrade gracefully (read failures → token path,
  never crash).
- All CLI output via a thin formatting layer so `--json` is a flag flip, not
  a parallel code path.
- Tests: golden-file tests for `--json` output; table tests for promotion,
  link-cycle rejection, and identity assertion paths.