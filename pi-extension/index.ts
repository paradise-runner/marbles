/**
 * Marbles extension for pi.
 *
 * Bridges the `mb` (marbles) CLI task/project manager into a pi agent session:
 *
 *  - Registers a `marbles` tool the LLM can call to pick a working project and
 *    to claim / close / open / comment on tasks without leaving the session.
 *  - Renders a persistent TUI widget above the editor showing the current
 *    project and its tasks with checkbox status (☐ open, ● working-on, ☑ done),
 *    plus a footer status summarising progress.
 *  - Auto-refreshes the widget after marbles tool calls and at the end of each
 *    turn, so the UI tracks the agent's progress in real time.
 *
 * Identity: a "pi" marbles agent is auto-registered on first use and its token
 * is cached at ~/.marbles/identities/pi-agent-token. All `mb` invocations
 * include MB_AGENT_TOKEN so attribution is deterministic.
 */

import { StringEnum } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionContext, Theme } from "@earendil-works/pi-coding-agent";
import { Text, truncateToWidth } from "@earendil-works/pi-tui";
import { Type } from "typebox";
import {
	AGENT_NAME,
	type MbItem,
	dispID,
	MbError,
	MbClient,
	parseCreatedId,
	gitCommitAndPush,
} from "./client.ts";

const STATE_CUSTOM_TYPE = "marbles-state";

interface MarblesState {
	currentProject?: number;
}

function priorityLabel(p: number): string {
	return ["critical", "high", "med", "low"][p] ?? `p${p}`;
}

/**
 * Build the (already-themed) widget lines for the current project + tasks.
 *
 * Shows at most **4** task rows so the widget stays compact:
 *   - up to 3 **active** tasks (open or review — claimed-by-me first, then
 *     priority, then age), so the visible set iterates down as work progresses
 *   - 1 **most-recently-closed** task (evidence of recent progress)
 *   - if fewer than 3 active, shows more closed to fill the 4 slots
 *   - "… N more" line appended if the project has more tasks than shown
 *
 * @internal exported for testing
 */
export function buildProjectLines(theme: Theme, project: MbItem, tasks: MbItem[]): string[] {
	const done = tasks.filter((t) => t.status === "closed").length;
	const total = tasks.length;
	const working = tasks.filter((t) => t.status === "open" && t.claimed_by === AGENT_NAME).length;
	const awaiting = tasks.filter((t) => t.status === "review").length;

	const lines: string[] = [];
	const header =
		theme.fg("accent", "◧ marbles") +
		" " + theme.fg("muted", "•") + " " +
		theme.fg("accent", dispID(project)) + " " + theme.bold(project.title) +
		"  " + theme.fg("success", `${done}/${total} done`) +
		(working ? "  " + theme.fg("accent", `●${working} working`) : "") +
		(awaiting ? "  " + theme.fg("warning", `◷${awaiting} review`) : "");
	lines.push(header);
	lines.push(theme.fg("borderMuted", "─".repeat(6)));

	if (tasks.length === 0) {
		lines.push(theme.fg("dim", "  no tasks — ask the agent: marbles new_task \"...\""));
		return lines;
	}

	// ── Pick which tasks to show ──────────────────────────────────────────
	const MAX_VISIBLE = 4;

	// Active pool = open + review combined, sorted: working → review → idle
	const activeTasks = tasks
		.filter((t) => t.status !== "closed")
		.sort((a, b) => {
			const aScore =
				a.status === "open" && a.claimed_by === AGENT_NAME ? 0 :
				a.status === "review" && a.claimed_by === AGENT_NAME ? 1 :
				a.status === "open" ? 2 :
				3; // review (not mine)
			const bScore =
				b.status === "open" && b.claimed_by === AGENT_NAME ? 0 :
				b.status === "review" && b.claimed_by === AGENT_NAME ? 1 :
				b.status === "open" ? 2 :
				3;
			return aScore - bScore || a.priority - b.priority || a.id - b.id;
		});

	const closedTasks = tasks
		.filter((t) => t.status === "closed")
		.sort((a, b) => {
			const aAt = a.closed_at ?? a.id;
			const bAt = b.closed_at ?? b.id;
			return (bAt as number) - (aAt as number);
		});

	// Show up to 3 active tasks, then fill remaining slots with recent closed
	const showActive = activeTasks.slice(0, Math.min(3, activeTasks.length));
	let toDisplay: MbItem[];
	if (closedTasks.length > 0) {
		toDisplay = [...showActive, ...closedTasks.slice(0, MAX_VISIBLE - showActive.length)];
	} else {
		toDisplay = activeTasks.slice(0, MAX_VISIBLE);
	}

	// ── Render each visible task ───────────────────────────────────────────
	for (const t of toDisplay) {
		let check: string;
		let title: string;
		if (t.status === "closed") {
			check = theme.fg("success", "☑");
			title = theme.strikethrough(theme.fg("dim", t.title));
		} else if (t.status === "review") {
			check = theme.fg("warning", "◷");
			title = theme.fg("warning", t.title);
		} else if (t.claimed_by === AGENT_NAME) {
			check = theme.fg("accent", "●");
			title = theme.fg("text", t.title);
		} else {
			check = theme.fg("dim", "☐");
			title = theme.fg("muted", t.title);
		}

		const meta: string[] = [];
		if (t.priority <= 1) meta.push(theme.fg("warning", priorityLabel(t.priority)));
		if (t.claimed_by && t.claimed_by !== AGENT_NAME && t.status !== "closed") {
			meta.push(theme.fg("dim", `@${t.claimed_by}`));
		}
		if (t.blocked_by && t.blocked_by.length > 0) {
			meta.push(theme.fg("warning", `► blocked by ${t.blocked_by.map((n) => "T" + n).join(",")}`));
		}
		if (t.blocks && t.blocks.length > 0) {
			meta.push(theme.fg("dim", `◄ blocks ${t.blocks.map((n) => "T" + n).join(",")}`));
		}
		const metaStr = meta.length ? "  " + meta.join("  ") : "";
		lines.push(`  ${check}  ${theme.fg("dim", dispID(t))} ${title}${metaStr}`);
	}

	// ── Remaining-count footer ─────────────────────────────────────────────
	const hidden = total - toDisplay.length;
	if (hidden > 0) {
		lines.push(theme.fg("borderMuted", "─".repeat(4)) + theme.fg("dim", `  ${hidden} more`));
	}

	return lines;
}

/** Render a list of pre-themed lines through the width-aware widget callback. */
function showWidget(ctx: ExtensionContext, lines: string[]): void {
	ctx.ui.setWidget("marbles", () => ({
		render: (width: number) => lines.map((l) => truncateToWidth(l, width)),
		invalidate: () => {},
	}));
}

async function refreshWidget(ctx: ExtensionContext, client: MbClient, state: MarblesState): Promise<void> {
	if (!ctx.hasUI) return;
	const theme = ctx.ui.theme;

	if (state.currentProject === undefined) {
		showWidget(ctx, [
			theme.fg("borderMuted", "── ") + theme.fg("accent", "marbles") + theme.fg("borderMuted", " ──"),
			theme.fg("dim", "  no project selected"),
			theme.fg("muted", "  ask the agent: “marbles list_projects” then “marbles set_project <P>”"),
		]);
		ctx.ui.setStatus("marbles", theme.fg("dim", "◧ no project"));
		return;
	}

	let project: MbItem | undefined;
	let tasks: MbItem[] = [];
	try {
		const [projs, ts] = await Promise.all([client.listProjects(), client.listTasks(state.currentProject)]);
		project = projs.find((p) => p.id === state.currentProject);
		tasks = ts ?? [];
	} catch (e) {
		const msg = e instanceof MbError ? e.message : "store unavailable";
		showWidget(ctx, [
			theme.fg("borderMuted", "── ") + theme.fg("accent", "marbles") + theme.fg("borderMuted", " ──"),
			theme.fg("error", `  ${msg}`),
		]);
		ctx.ui.setStatus("marbles", theme.fg("error", "◧ mb error"));
		return;
	}

	if (!project) {
		showWidget(ctx, [
			theme.fg("borderMuted", "── ") + theme.fg("accent", "marbles") + theme.fg("borderMuted", " ──"),
			theme.fg("warning", `  project P${state.currentProject} not found — pick another`),
		]);
		ctx.ui.setStatus("marbles", theme.fg("warning", "◧ stale project"));
		return;
	}

	showWidget(ctx, buildProjectLines(theme, project, tasks));
	const done = tasks.filter((t) => t.status === "closed").length;
	ctx.ui.setStatus(
		"marbles",
		theme.fg("accent", "◧") + " " + theme.fg("accent", dispID(project)) + " " +
			theme.fg("muted", project.title) + "  " + theme.fg("success", `${done}/${tasks.length}`),
	);
}

export default function marblesExtension(pi: ExtensionAPI): void {
	const client = new MbClient();
	const state: MarblesState = {};

	let persistQueued = false;
	function persist(): void {
		if (persistQueued) return;
		persistQueued = true;
		queueMicrotask(() => {
			pi.appendEntry(STATE_CUSTOM_TYPE, { currentProject: state.currentProject });
			persistQueued = false;
		});
	}

	function reconstruct(ctx: ExtensionContext): void {
		for (const entry of ctx.sessionManager.getBranch()) {
			if (
				entry.type === "custom" &&
				(entry as { customType?: string }).customType === STATE_CUSTOM_TYPE &&
				(entry as { data?: MarblesState }).data
			) {
				state.currentProject = (entry as { data: MarblesState }).data.currentProject;
			}
		}
	}

	// --- The marbles tool --------------------------------------------------

	pi.registerTool({
		name: "marbles",
		label: "Marbles",
		description:
			"Interact with the marbles (`mb`) project/task manager. Pick a working " +
			"project and claim/close/open/comment on tasks. The current project and " +
			"its task checkboxes are shown in the UI and update live. Always call " +
			"`marbles` (not the `mb` CLI directly) so the UI stays in sync.",
		promptSnippet: "Manage projects/tasks in the marbles store (list/set project, claim/close/open/comment tasks)",
		promptGuidelines: [
			"Use the marbles tool — not the `mb` bash command — to manage marbles tasks, " +
				"so the project/task widget stays in sync.",
			"At the start of work on a marbles project, call marbles list_projects and " +
				"marbles set_project to focus the UI on that project.",
			"Before implementing a task, call marbles claim to mark it in-progress. When " +
				"done, call marbles close with a commit_message summarising the changes — " +
				"the extension handles git add/commit/push automatically.",
		],
		parameters: Type.Object({
			action: StringEnum(
				["list_projects", "set_project", "current", "list_tasks", "claim", "close", "open", "review", "comment", "new_task", "new_project"],
				{ description: "What to do" },
			),
			project_id: Type.Optional(Type.Number({ description: "Project id (for set_project, new_task)" })),
			task_id: Type.Optional(Type.Number({ description: "Task id (for claim/close/open/comment)" })),
			title: Type.Optional(Type.String({ description: "Title for new_task / new_project" })),
			comment: Type.Optional(Type.String({ description: "Comment body (for comment)" })),
			commit_message: Type.Optional(Type.String({ description: "Commit message (for close — extension will git add, commit, push)" })),
		}),

		async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
			const reply = (text: string) => ({ content: [{ type: "text" as const, text }], details: {} as Record<string, unknown> });

			try {
				switch (params.action) {
					case "list_projects": {
						const projects = await client.listProjects();
						return reply(
							projects.length
								? projects
										.map((p) => `${dispID(p)} ${p.status === "open" ? "open" : "closed"} ${p.title} (${p.open_count ?? 0} open)`)
										.join("\n")
								: "no projects in the marbles store",
						);
					}
					case "set_project": {
						if (params.project_id === undefined) {
							const projects = await client.listProjects();
							return reply(
								"set_project requires a project_id. Available projects:\n" +
									projects.map((p) => `${dispID(p)} ${p.title}`).join("\n"),
							);
						}
						state.currentProject = params.project_id;
						persist();
						await refreshWidget(ctx, client, state);
						return reply(`Now working in marbles project P${params.project_id}.`);
					}
					case "current": {
						if (state.currentProject === undefined) return reply("No marbles project selected.");
						const projects = await client.listProjects();
						const p = projects.find((x) => x.id === state.currentProject);
						if (!p) return reply(`Project P${state.currentProject} not found.`);
						const tasks = await client.listTasks(p.id);
						return reply(
							`${dispID(p)} ${p.title} (${p.open_count ?? 0} open / ${p.child_count ?? 0} total)\n` +
								tasks
									.map((t) => `[${t.status === "closed" ? "x" : " "}] ${dispID(t)} ${t.title}${t.claimed_by ? ` @${t.claimed_by}` : ""}`)
									.join("\n"),
						);
					}
					case "list_tasks": {
						const pid = params.project_id ?? state.currentProject;
						if (pid === undefined) return reply("No marbles project selected. Use set_project first.");
						const tasks = await client.listTasks(pid);
						return reply(
							tasks.length
								? tasks.map((t) => `${dispID(t)} [${t.status}] ${t.title}${t.claimed_by ? ` @${t.claimed_by}` : ""}`).join("\n")
								: `no tasks in P${pid}`,
						);
					}
					case "new_project": {
						if (!params.title) return reply("new_project requires a title.");
						const out = await client.newProject(params.title);
						const id = parseCreatedId(out);
						if (id !== null) state.currentProject = id;
						persist();
						await refreshWidget(ctx, client, state);
						return reply(out.trim() || "project created");
					}
					case "new_task": {
						if (!params.title) return reply("new_task requires a title.");
						const pid = params.project_id ?? state.currentProject;
						if (pid === undefined) return reply("new_task requires a project (select one via set_project).");
						const out = await client.newTask(params.title, pid);
						await refreshWidget(ctx, client, state);
						return reply(out.trim() || "task created");
					}
					case "claim": {
						if (params.task_id === undefined) return reply("claim requires a task_id.");
						const out = await client.claim(params.task_id);
						await refreshWidget(ctx, client, state);
						return reply(out.trim());
					}
					case "close": {
						if (params.task_id === undefined) return reply("close requires a task_id.");
						const out = await client.close(params.task_id);
						await refreshWidget(ctx, client, state);

						// If a commit message was provided, handle git add/commit/push
						if (params.commit_message) {
							try {
								const gitOut = await gitCommitAndPush(params.commit_message);
								return reply(`${out.trim()}\n${gitOut}`);
							} catch (gitErr) {
								return reply(`${out.trim()}\n⚠️ git: ${(gitErr as Error).message}`);
							}
						}
						return reply(out.trim());
					}
					case "open": {
						if (params.task_id === undefined) return reply("open requires a task_id.");
						const out = await client.open(params.task_id);
						await refreshWidget(ctx, client, state);
						return reply(out.trim());
					}
					case "review": {
						if (params.task_id === undefined) return reply("review requires a task_id.");
						const out = await client.exec(["task", "review", String(params.task_id)]);
						await refreshWidget(ctx, client, state);
						return reply(out.trim());
					}
					case "comment": {
						if (params.task_id === undefined || !params.comment) return reply("comment requires a task_id and comment.");
						const out = await client.comment(params.task_id, params.comment);
						await refreshWidget(ctx, client, state);
						return reply(out.trim());
					}
					default:
						return reply(`unknown marbles action: ${String(params.action)}`);
				}
			} catch (e) {
				const msg = e instanceof MbError ? e.message : (e as Error).message;
				return reply(`marbles error: ${msg}`);
			}
		},

		renderCall(args, theme, _context) {
			let text = theme.fg("toolTitle", theme.bold("marbles ")) + theme.fg("muted", String(args.action));
			if (args.project_id !== undefined) text += ` ${theme.fg("accent", "P" + args.project_id)}`;
			if (args.task_id !== undefined) text += ` ${theme.fg("accent", "T" + args.task_id)}`;
			if (args.title) text += ` ${theme.fg("dim", `"${args.title}"`)}`;
			if (args.commit_message) text += ` ${theme.fg("dim", `commit: "${args.commit_message}"`)}`;
			return new Text(text, 0, 0);
		},

		renderResult(result, _options, theme, _context) {
			const text = result.content[0]?.type === "text" ? result.content[0].text : "";
			const first = text.split("\n")[0] ?? "";
			if (first.startsWith("marbles error")) return new Text(theme.fg("error", first), 0, 0);
			return new Text(theme.fg("success", "✓ ") + theme.fg("muted", first.length > 80 ? first.slice(0, 77) + "…" : first), 0, 0);
		},
	});

	// --- Commands ----------------------------------------------------------

	pi.registerCommand("marbles", {
		description: "Pick the marbles project to track in the UI",
		handler: async (_args, ctx) => {
			const projects = await client.listProjects();
			if (projects.length === 0) {
				ctx.ui.notify("No marbles projects. Ask the agent: marbles new_project \"...\"", "info");
				return;
			}
			const choice = await ctx.ui.select(
				"Marbles project to track:",
				projects.map((p) => `${dispID(p)} ${p.title} (${p.open_count ?? 0} open)`),
			);
			if (!choice) return;
			const m = choice.match(/^P(\d+)/);
			if (!m) return;
			state.currentProject = Number(m[1]);
			persist();
			await refreshWidget(ctx, client, state);
			ctx.ui.notify(`Tracking marbles project P${state.currentProject}`, "info");
		},
	});

	// --- Lifecycle: restore state + initial render -------------------------

	pi.on("session_start", async (_event, ctx) => {
		reconstruct(ctx);
		await client.init().catch(() => {
			/* surfaced in the widget on next refresh */
		});
		await refreshWidget(ctx, client, state);
	});

	pi.on("session_tree", async (_event, ctx) => {
		reconstruct(ctx);
		await refreshWidget(ctx, client, state);
	});

	// --- Inject agent guidance --------------------------------------------

	pi.on("before_agent_start", async () => {
		const pid = state.currentProject;
		const projectLine = pid !== undefined ? `The current marbles project is P${pid}.` : "No marbles project is selected yet.";
		return {
			message: {
				customType: "marbles-context",
				content: `[MARBLES INTEGRATION]
You have a \`marbles\` tool to manage work in the marbles task store.
${projectLine}
Workflow when tackling tracked work:
1. marbles list_projects / marbles set_project <P>  to focus on a project
2. marbles list_tasks to see what needs doing (checkboxes are shown in the UI)
3. marbles claim <task_id> before you start a task (marks it in-progress)
4. do the work, then marbles review <task_id> when you believe it is done
   → The task appears in the widget with a ◷ icon awaiting human review.
5. If the reviewer says it looks good: marbles close <task_id> with a
   commit_message — the extension will automatically git add, commit, and
   push. Write a concise, descriptive commit message summarising the changes.
6. If the reviewer says to iterate: marbles open <task_id> to go back to
   in-progress and make further changes.
7. add marbles comment <task_id> <note> for updates the user should see.
Prefer the marbles tool over calling \`mb\` via bash — it keeps the UI widget in sync.

Autonomous project & task management:
- If no project is selected and the user asks you to do work, proactively
  list projects (marbles list_projects) to find a suitable existing one. If a
  relevant project exists, confirm with the user before setting it. If no
  suitable project exists, create one with marbles new_project.
- Before creating a new project, ask the user for context — what the project
  is about, its goals, or other relevant details — so the project is
  well-scoped from the start.
- When adding tasks (marbles new_task), break the user's request down into
  as many discrete, actionable tasks as you see fit. Each task should be a
  self-contained unit of work that can be claimed, implemented, and reviewed
  independently. Add all tasks to the relevant project.`,
				display: false,
			},
		};
	});

	// --- Refresh after marbles tool calls, `mb` bash calls, and each turn --

	pi.on("tool_result", async (event, ctx) => {
		if (event.toolName === "marbles") {
			await refreshWidget(ctx, client, state);
			return;
		}
		if (event.toolName === "bash") {
			const cmd = (event.input as { command?: string }).command ?? "";
			if (/\bmb\b/.test(cmd)) await refreshWidget(ctx, client, state);
		}
	});

	pi.on("turn_end", async (_event, ctx) => {
		await refreshWidget(ctx, client, state);
	});
}