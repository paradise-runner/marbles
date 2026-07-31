package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func routeProject(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "project: expected subcommand (ls, new, show, close, open, claim, unclaim, prio, mv, edit)")
		os.Exit(1)
	}

	sub := args[0]
	subargs := args[1:]

	switch sub {
	case "ls":
		cmdProjectLs(out, subargs)
	case "new":
		cmdProjectNew(out, subargs)
	case "show":
		cmdProjectShow(out, subargs)
	case "close":
		cmdProjectClose(out, subargs)
	case "open":
		cmdProjectOpen(out, subargs)
	case "claim":
		cmdProjectClaim(out, subargs)
	case "unclaim":
		cmdProjectUnclaim(out, subargs)
	case "prio":
		cmdProjectPrio(out, subargs)
	case "mv":
		cmdProjectMv(out, subargs)
	case "edit":
		cmdProjectEdit(out, subargs)
	default:
		fmt.Fprintf(os.Stderr, "project: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
}

func cmdProjectLs(out *Output, args []string) {
	f := ListFilter{Kind: "project", IncludeOpen: true}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--open":
			f.IncludeOpen = true
		case "--review":
			f.IncludeReview = true
		case "--closed":
			f.IncludeClosed = true
		case "--mine":
			f.Mine = true
		case "--claimed":
			f.Claimed = true
		case "--unclaimed":
			f.Unclaimed = true
		case "--top":
			f.Top = true
		case "--here":
			f.Here = true
			f.CurrentDir = getCwd()
		case "--sort":
			if i+1 < len(args) {
				i++
				f.Sort = args[i]
			}
		case "--parent":
			if i+1 < len(args) {
				i++
				pid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				f.Parent = &pid
			}
		}
	}

	// Default: open only (IncludeOpen is on; --closed adds closed).

	items, err := listItems(db, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Get child counts for each project
	counts := make(map[int64]*ItemCounts)
	for _, item := range items {
		c, err := getChildCounts(db, item.ID)
		if err == nil {
			counts[item.ID] = c
		}
	}

	out.PrintProjects(items, counts)
}

func cmdProjectNew(out *Output, args []string) {
	// Need at least a title
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "--") {
		fmt.Fprintln(os.Stderr, "usage: mb project new \"Title\" [--body ...] [--priority low|med|high|critical] [--parent P] [--claim] [--cwd .]")
		os.Exit(1)
	}

	title := args[0]
	subargs := args[1:]

	var body string
	priority := 2 // default med
	var parentItem *int64
	claim := false
	cwdHint := ""

	for i := 0; i < len(subargs); i++ {
		switch subargs[i] {
		case "--body":
			if i+1 < len(subargs) {
				i++
				body = subargs[i]
			}
		case "--priority":
			if i+1 < len(subargs) {
				i++
				if p, ok := PriorityNames[subargs[i]]; ok {
					priority = p
				} else {
					fmt.Fprintf(os.Stderr, "error: invalid priority %q (use: low, med, high, critical)\n", subargs[i])
					os.Exit(1)
				}
			}
		case "--parent":
			if i+1 < len(subargs) {
				i++
				pid, _, err := resolveDisplayID(subargs[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				parentItem = &pid
			}
		case "--claim":
			claim = true
		case "--cwd":
			if i+1 < len(subargs) {
				i++
				cwdHint = subargs[i]
			}
		}
	}

	// Validate parent is a project
	if parentItem != nil {
		parent, err := getItem(db, *parentItem)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: parent %d not found\n", *parentItem)
			os.Exit(1)
		}
		if parent.Kind != "project" {
			fmt.Fprintf(os.Stderr, "error: parent must be a project, got %s\n", parent.Kind)
			os.Exit(1)
		}
	}

	// Resolve identity
	info, err := ResolveIdentity(db, "")
	if err != nil && info.Method != "ambiguous" && info.Method != "none" {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	agent := MustGetAgent(info)

	// If --cwd . is given, use current directory
	if cwdHint == "." {
		cwdHint = getCwd()
	}

	item, err := createItem(db, "project", title, body, agent, priority, parentItem, cwdHint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	createEvent(db, &item.ID, agent, VerbCreated, fmt.Sprintf(`{"kind":"project","title":%q}`, title))

	// Sync parent link if needed
	if parentItem != nil {
		syncParentLinks(db, item.ID, nil, parentItem, agent)
	}

	if claim {
		claimItem(db, item.ID, agent)
		createEvent(db, &item.ID, agent, VerbClaimed, fmt.Sprintf(`{"agent":%q}`, agent))
	}

	out.Println("Created project", item.DisplayID())
}

func cmdProjectShow(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb project show <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "project" {
		fmt.Fprintf(os.Stderr, "error: item %s is a %s, not a project\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}

	links, _ := listLinks(db, &id, "")
	comments, _ := listComments(db, id)
	events, _ := listEvents(db, &id, 20)

	out.PrintItemDetail(item, links, comments, events)
}

func cmdProjectClose(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb project close <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "project" {
		fmt.Fprintf(os.Stderr, "error: item %s is a %s, not a project\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "closed", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbClosed, "{}")
	out.Println("Closed", item.DisplayID())
}

func cmdProjectOpen(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb project open <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "project" {
		fmt.Fprintf(os.Stderr, "error: item %s is a %s, not a project\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "open", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReopened, "{}")
	out.Println("Reopened", item.DisplayID())
}

func cmdProjectClaim(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb project claim <id> [--as <agent>]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	agent := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--as" && i+1 < len(args) {
			agent = args[i+1]
			i++
		}
	}

	if agent == "" {
		info, _ := ResolveIdentity(db, "")
		agent = MustGetAgent(info)
	}

	prevClaimed := item.ClaimedBy
	verb := VerbClaimed
	if prevClaimed != "" && prevClaimed != agent {
		verb = VerbReclaimed
	}

	if err := claimItem(db, id, agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, verb, fmt.Sprintf(`{"agent":%q,"prev":%q}`, agent, prevClaimed))
	out.Println(item.DisplayID(), "claimed by", agent)
}

func cmdProjectUnclaim(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb project unclaim <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := unclaimItem(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbUnclaimed, fmt.Sprintf(`{"prev":%q}`, item.ClaimedBy))
	out.Println("Unclaimed", item.DisplayID())
}

func cmdProjectPrio(out *Output, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mb project prio <id> <low|med|high|critical>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	priority, ok := PriorityNames[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: invalid priority %q (use: low, med, high, critical)\n", args[1])
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemPriority(db, id, priority); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReprio, fmt.Sprintf(`{"priority":%d}`, priority))
	out.Println("Priority set on", args[0])
}

func cmdProjectMv(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb project mv <id> --parent <P>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var newParent *int64
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--parent":
			if i+1 < len(args) {
				i++
				pid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				newParent = &pid
			}
		case "--top", "--inbox":
			newParent = nil
		}
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	item, _ := getItem(db, id)
	oldParent := item.ParentItem

	if err := moveItem(db, id, newParent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	syncParentLinks(db, id, oldParent, newParent, agent)
	createEvent(db, &id, agent, VerbMoved, fmt.Sprintf(`{"old_parent":%v,"new_parent":%v}`, formatOptInt64(oldParent), formatOptInt64(newParent)))
	out.Println("Moved", args[0])
}

func cmdProjectEdit(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb project edit <id> [--title ...] [--body ...]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	var newTitle, newBody string
	titleSet := false
	bodySet := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				i++
				newTitle = args[i]
				titleSet = true
			}
		case "--body":
			if i+1 < len(args) {
				i++
				newBody = args[i]
				bodySet = true
			}
		}
	}

	if titleSet {
		if err := updateItemTitle(db, id, newTitle); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbTitleEdited, fmt.Sprintf(`{"title":%q}`, newTitle))
	}
	if bodySet {
		if err := updateItemBody(db, id, newBody); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbBodyEdited, `{"body":"updated"}`)
	}
	out.Println("Updated", args[0])
}

// --- Task commands ---

func routeTask(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "task: expected subcommand (ls, new, show, close, open, claim, unclaim, prio, promote, mv, edit)")
		os.Exit(1)
	}

	sub := args[0]
	subargs := args[1:]

	switch sub {
	case "ls":
		cmdTaskLs(out, subargs)
	case "new":
		cmdTaskNew(out, subargs)
	case "show":
		cmdTaskShow(out, subargs)
	case "close":
		cmdTaskClose(out, subargs)
	case "open":
		cmdTaskOpen(out, subargs)
	case "claim":
		cmdTaskClaim(out, subargs)
	case "unclaim":
		cmdTaskUnclaim(out, subargs)
	case "prio":
		cmdTaskPrio(out, subargs)
	case "promote":
		cmdTaskPromote(out, subargs)
	case "review":
		cmdTaskReview(out, subargs)
	case "mv":
		cmdTaskMv(out, subargs)
	case "edit":
		cmdTaskEdit(out, subargs)
	default:
		fmt.Fprintf(os.Stderr, "task: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
}

func cmdTaskLs(out *Output, args []string) {
	f := ListFilter{Kind: "task", IncludeOpen: true}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--open":
			f.IncludeOpen = true
		case "--review":
			f.IncludeReview = true
		case "--closed":
			f.IncludeClosed = true
		case "--mine":
			f.Mine = true
		case "--claimed":
			f.Claimed = true
		case "--unclaimed":
			f.Unclaimed = true
		case "--here":
			f.Here = true
			f.CurrentDir = getCwd()
		case "--sort":
			if i+1 < len(args) {
				i++
				f.Sort = args[i]
			}
		case "--project":
			if i+1 < len(args) {
				i++
				pid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				f.Parent = &pid
			}
		case "--blocks-by":
			if i+1 < len(args) {
				i++
				bid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				f.BlockedBy = &bid
			}
		case "--blocks":
			if i+1 < len(args) {
				i++
				bid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				f.Blocks = &bid
			}
		}
	}

	// Default: open only (IncludeOpen is on; --closed adds closed).

	items, err := listItems(db, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Get all links involving these items for display markers (deduplicated)
	linkMap := make(map[int64]*Link)
	for _, item := range items {
		links, _ := listLinks(db, &item.ID, "")
		for _, l := range links {
			linkMap[l.ID] = l
		}
	}
	allLinks := make([]*Link, 0, len(linkMap))
	for _, l := range linkMap {
		allLinks = append(allLinks, l)
	}

	out.PrintTasks(items, allLinks)
}

func cmdTaskNew(out *Output, args []string) {
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "--") {
		fmt.Fprintln(os.Stderr, "usage: mb task new \"Title\" [--project P] [--body ...] [--priority ...] [--blocks T] [--blocked-by T] [--related T] [--claim]")
		os.Exit(1)
	}

	title := args[0]
	subargs := args[1:]

	var body string
	priority := 2
	var project *int64
	var blocks, blockedBy, related *int64
	claim := false

	for i := 0; i < len(subargs); i++ {
		switch subargs[i] {
		case "--body":
			if i+1 < len(subargs) {
				i++
				body = subargs[i]
			}
		case "--priority":
			if i+1 < len(subargs) {
				i++
				if p, ok := PriorityNames[subargs[i]]; ok {
					priority = p
				} else {
					fmt.Fprintf(os.Stderr, "error: invalid priority %q (use: low, med, high, critical)\n", subargs[i])
					os.Exit(1)
				}
			}
		case "--project":
			if i+1 < len(subargs) {
				i++
				pid, _, err := resolveDisplayID(subargs[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				project = &pid
			}
		case "--blocks":
			if i+1 < len(subargs) {
				i++
				bid, _, err := resolveDisplayID(subargs[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				blocks = &bid
			}
		case "--blocked-by":
			if i+1 < len(subargs) {
				i++
				bid, _, err := resolveDisplayID(subargs[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				blockedBy = &bid
			}
		case "--related":
			if i+1 < len(subargs) {
				i++
				rid, _, err := resolveDisplayID(subargs[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				related = &rid
			}
		case "--claim":
			claim = true
		}
	}

	// Validate project is a project
	if project != nil {
		parent, err := getItem(db, *project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: project %d not found\n", *project)
			os.Exit(1)
		}
		if parent.Kind != "project" {
			fmt.Fprintf(os.Stderr, "error: --project target must be a project, got %s\n", parent.Kind)
			os.Exit(1)
		}
	}

	info, err := ResolveIdentity(db, "")
	if err != nil && info.Method != "ambiguous" && info.Method != "none" {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	agent := MustGetAgent(info)

	item, err := createItem(db, "task", title, body, agent, priority, project, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	createEvent(db, &item.ID, agent, VerbCreated, fmt.Sprintf(`{"kind":"task","title":%q}`, title))

	// Sync parent link if needed
	if project != nil {
		syncParentLinks(db, item.ID, nil, project, agent)
	}

	// Create link for --blocks
	if blocks != nil {
		if err := checkBlockCycles(db, item.ID, *blocks); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createLink(db, item.ID, *blocks, "blocks", agent)
		createEvent(db, &item.ID, agent, VerbLinked, fmt.Sprintf(`{"rel":"blocks","from":%d,"to":%d}`, item.ID, *blocks))
	}

	// Create link for --blocked-by (reverse blocks)
	if blockedBy != nil {
		if err := checkBlockCycles(db, *blockedBy, item.ID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createLink(db, *blockedBy, item.ID, "blocks", agent)
		createEvent(db, &item.ID, agent, VerbLinked, fmt.Sprintf(`{"rel":"blocks","from":%d,"to":%d}`, *blockedBy, item.ID))
	}

	// Create link for --related
	if related != nil {
		if item.ID != *related {
			createLink(db, item.ID, *related, "related", agent)
			createEvent(db, &item.ID, agent, VerbLinked, fmt.Sprintf(`{"rel":"related","from":%d,"to":%d}`, item.ID, *related))
		}
	}

	if claim {
		claimItem(db, item.ID, agent)
		createEvent(db, &item.ID, agent, VerbClaimed, fmt.Sprintf(`{"agent":%q}`, agent))
	}

	out.Println("Created task", item.DisplayID())
}

func cmdTaskShow(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task show <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "task" {
		fmt.Fprintf(os.Stderr, "error: item %s is a %s, not a task\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}

	links, _ := listLinks(db, &id, "")
	comments, _ := listComments(db, id)
	events, _ := listEvents(db, &id, 20)

	out.PrintItemDetail(item, links, comments, events)
}

func cmdTaskClose(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task close <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "closed", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbClosed, "{}")
	out.Println("Closed", item.DisplayID())
}

func cmdTaskOpen(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task open <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "open", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReopened, "{}")
	out.Println("Reopened", item.DisplayID())
}

func cmdTaskClaim(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task claim <id> [--as <agent>]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	agent := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--as" && i+1 < len(args) {
			agent = args[i+1]
			i++
		}
	}

	if agent == "" {
		info, _ := ResolveIdentity(db, "")
		agent = MustGetAgent(info)
	}

	prevClaimed := item.ClaimedBy
	verb := VerbClaimed
	if prevClaimed != "" && prevClaimed != agent {
		verb = VerbReclaimed
	}

	if err := claimItem(db, id, agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, verb, fmt.Sprintf(`{"agent":%q,"prev":%q}`, agent, prevClaimed))
	out.Println(item.DisplayID(), "claimed by", agent)
}

func cmdTaskUnclaim(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task unclaim <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := unclaimItem(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbUnclaimed, fmt.Sprintf(`{"prev":%q}`, item.ClaimedBy))
	out.Println("Unclaimed", item.DisplayID())
}

func cmdTaskPrio(out *Output, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mb task prio <id> <low|med|high|critical>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	priority, ok := PriorityNames[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: invalid priority %q (use: low, med, high, critical)\n", args[1])
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemPriority(db, id, priority); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReprio, fmt.Sprintf(`{"priority":%d}`, priority))
	out.Println("Priority set on", args[0])
}

func cmdTaskPromote(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task promote <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "task" {
		fmt.Fprintf(os.Stderr, "error: item %s is already a %s\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := promoteItem(db, id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbPromoted, `{"from":"task","to":"project"}`)
	out.Println("Promoted", args[0], "to project", item.DisplayID())
}

func cmdTaskReview(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb task review <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if item.Kind != "task" {
		fmt.Fprintf(os.Stderr, "error: item %s is a %s, not a task\n", item.DisplayID(), item.Kind)
		os.Exit(1)
	}
	if item.Status != "open" {
		fmt.Fprintf(os.Stderr, "error: only open tasks can be sent to review (status is %s)\n", item.Status)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "review", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReviewed, `{}`)
	out.Println("Sent to review:", args[0])
}

func cmdTaskMv(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb task mv <id> --project <P>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var newParent *int64
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				pid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				newParent = &pid
			}
		case "--inbox":
			newParent = nil
		}
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	item, _ := getItem(db, id)
	oldParent := item.ParentItem

	if err := moveItem(db, id, newParent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	syncParentLinks(db, id, oldParent, newParent, agent)
	createEvent(db, &id, agent, VerbMoved, fmt.Sprintf(`{"old_parent":%v,"new_parent":%v}`, formatOptInt64(oldParent), formatOptInt64(newParent)))
	out.Println("Moved", args[0])
}

func cmdTaskEdit(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb task edit <id> [--title ...] [--body ...]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	var newTitle, newBody string
	titleSet := false
	bodySet := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				i++
				newTitle = args[i]
				titleSet = true
			}
		case "--body":
			if i+1 < len(args) {
				i++
				newBody = args[i]
				bodySet = true
			}
		}
	}

	if titleSet {
		if err := updateItemTitle(db, id, newTitle); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbTitleEdited, fmt.Sprintf(`{"title":%q}`, newTitle))
	}
	if bodySet {
		if err := updateItemBody(db, id, newBody); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbBodyEdited, `{"body":"updated"}`)
	}
	out.Println("Updated", args[0])
}

// --- Polymorphic shortcuts ---

func cmdShow(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb show <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	links, _ := listLinks(db, &id, "")
	comments, _ := listComments(db, id)
	events, _ := listEvents(db, &id, 20)

	out.PrintItemDetail(item, links, comments, events)
}

func cmdClaim(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb claim <id> [--as <agent>]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	agent := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--as" && i+1 < len(args) {
			agent = args[i+1]
			i++
		}
	}

	if agent == "" {
		info, _ := ResolveIdentity(db, "")
		agent = MustGetAgent(info)
	}

	prevClaimed := item.ClaimedBy
	verb := VerbClaimed
	if prevClaimed != "" && prevClaimed != agent {
		verb = VerbReclaimed
	}

	if err := claimItem(db, id, agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, verb, fmt.Sprintf(`{"agent":%q,"prev":%q}`, agent, prevClaimed))
	out.Println(item.DisplayID(), "claimed by", agent)
}

func cmdClose(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb close <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "closed", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbClosed, "{}")
	out.Println("Closed", item.DisplayID())
}

func cmdOpen(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb open <id>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	item, err := resolveItem(db, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemStatus(db, id, "open", agent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReopened, "{}")
	out.Println("Reopened", item.DisplayID())
}

func cmdPrio(out *Output, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mb prio <id> <low|med|high|critical>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	priority, ok := PriorityNames[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: invalid priority %q (use: low, med, high, critical)\n", args[1])
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := updateItemPriority(db, id, priority); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbReprio, fmt.Sprintf(`{"priority":%d}`, priority))
	out.Println("Priority set on", args[0])
}

func cmdMv(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb mv <id> --project <P|--inbox>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var newParent *int64
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--project", "--parent":
			if i+1 < len(args) {
				i++
				pid, _, err := resolveDisplayID(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				newParent = &pid
			}
		case "--inbox", "--top":
			newParent = nil
		}
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	item, _ := getItem(db, id)
	oldParent := item.ParentItem

	if err := moveItem(db, id, newParent); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	syncParentLinks(db, id, oldParent, newParent, agent)
	createEvent(db, &id, agent, VerbMoved, fmt.Sprintf(`{"old_parent":%v,"new_parent":%v}`, formatOptInt64(oldParent), formatOptInt64(newParent)))
	out.Println("Moved", args[0])
}

func cmdEdit(out *Output, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mb edit <id> [--title ...] [--body ...]")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	var newTitle, newBody string
	titleSet := false
	bodySet := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--title":
			if i+1 < len(args) {
				i++
				newTitle = args[i]
				titleSet = true
			}
		case "--body":
			if i+1 < len(args) {
				i++
				newBody = args[i]
				bodySet = true
			}
		}
	}

	if titleSet {
		if err := updateItemTitle(db, id, newTitle); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbTitleEdited, fmt.Sprintf(`{"title":%q}`, newTitle))
	}
	if bodySet {
		if err := updateItemBody(db, id, newBody); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		createEvent(db, &id, agent, VerbBodyEdited, `{"body":"updated"}`)
	}
	out.Println("Updated", args[0])
}

func cmdComment(out *Output, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mb comment <id> <text>")
		os.Exit(1)
	}
	id, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	body := strings.Join(args[1:], " ")
	// Read from stdin if body is "-"
	if strings.TrimSpace(body) == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: reading stdin: %v\n", err)
			os.Exit(1)
		}
		body = strings.TrimSpace(string(data))
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	comment, err := createComment(db, id, agent, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	createEvent(db, &id, agent, VerbCommented, fmt.Sprintf(`{"comment_id":%d}`, comment.ID))
	out.Println("Comment added")
}

func cmdLog(out *Output, args []string) {
	var itemID *int64
	if len(args) > 0 {
		id, _, err := resolveDisplayID(args[0])
		if err == nil {
			itemID = &id
		}
	}

	events, err := listEvents(db, itemID, 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	out.PrintEvents(events)
}

func cmdWhoami(out *Output, args []string) {
	info, err := ResolveIdentity(db, "")
	if err != nil {
		out.Printf("No identity asserted (%v)\n", err)
		return
	}

	switch info.Method {
	case "fingerprint", "token", "env":
		out.Printf("Agent: %s (via %s)\n", info.Agent, info.Method)
	case "ambiguous":
		out.Println("Ambiguous identity (multiple agents match this fingerprint)")
	case "none":
		out.Println("No identity asserted")
	}
}

func cmdStatus(out *Output, args []string) {
	// Count items
	var taskCount, projectCount, openCount, closedCount int
	db.QueryRow("SELECT COUNT(*) FROM items WHERE kind='task'").Scan(&taskCount)
	db.QueryRow("SELECT COUNT(*) FROM items WHERE kind='project'").Scan(&projectCount)
	db.QueryRow("SELECT COUNT(*) FROM items WHERE status='open'").Scan(&openCount)
	db.QueryRow("SELECT COUNT(*) FROM items WHERE status='closed'").Scan(&closedCount)

	var agentCount int
	db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&agentCount)

	if out.Format == FormatJSON {
		status := map[string]interface{}{
			"store":    storePath(),
			"tasks":    taskCount,
			"projects": projectCount,
			"open":     openCount,
			"closed":   closedCount,
			"agents":   agentCount,
		}
		out.PrintJSON(status)
		return
	}

	out.Println("Store:", storePath())
	out.Printf("Tasks: %d, Projects: %d\n", taskCount, projectCount)
	out.Printf("Open: %d, Closed: %d\n", openCount, closedCount)
	out.Printf("Agents registered: %d\n", agentCount)

	info, _ := ResolveIdentity(db, "")
	if info.Agent != "" {
		out.Printf("Asserted agent: %s (via %s)\n", info.Agent, info.Method)
	} else {
		out.Println("No agent asserted")
	}
}

func cmdInit(out *Output, args []string) {
	if err := EnsureStoreDir(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	d, err := OpenDB(globalStorePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	path := DefaultDBPath
	if globalStorePath != "" {
		path = globalStorePath
	}
	out.Println("Store initialized at", path)
}

// --- Link commands ---

func routeLink(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb link <a> <rel> <b>  or  mb link ls [<id>] [--rel <r>]")
		os.Exit(1)
	}

	if args[0] == "ls" {
		cmdLinkLs(out, args[1:])
		return
	}

	// mb link <a> <rel> <b>
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mb link <a> <rel> <b>")
		os.Exit(1)
	}

	fromID, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	rel := args[1]
	toID, _, err := resolveDisplayID(args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Validate rel
	validRels := map[string]bool{"blocks": true, "related": true, "parent": true, "child": true}
	if !validRels[rel] {
		fmt.Fprintf(os.Stderr, "error: invalid relation %q (use: blocks, related, parent, child)\n", rel)
		os.Exit(1)
	}

	if fromID == toID {
		fmt.Fprintln(os.Stderr, "error: self-links not allowed")
		os.Exit(1)
	}

	// Check blocks cycles
	if rel == "blocks" {
		if err := checkBlockCycles(db, fromID, toID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Check parent cycles (for parent/child)
	if rel == "parent" || rel == "child" {
		// TODO: validate parent/child cycles
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	link, err := createLink(db, fromID, toID, rel, agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// For parent/child, also update items.parent_item
	if rel == "parent" {
		// fromID is the parent, toID is the child
		if item, _ := getItem(db, toID); item != nil && (item.ParentItem == nil || *item.ParentItem != fromID) {
			moveItem(db, toID, &fromID)
		}
	} else if rel == "child" {
		// fromID is the child, toID is the parent
		if item, _ := getItem(db, fromID); item != nil && (item.ParentItem == nil || *item.ParentItem != toID) {
			moveItem(db, fromID, &toID)
		}
	}

	createEvent(db, &fromID, agent, VerbLinked, fmt.Sprintf(`{"rel":%q,"from":%d,"to":%d}`, rel, fromID, toID))
	out.Println("Link created:", link.ID)
}

func routeUnlink(out *Output, args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mb unlink <a> <rel> <b>")
		os.Exit(1)
	}

	fromID, _, err := resolveDisplayID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	rel := args[1]
	toID, _, err := resolveDisplayID(args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, _ := ResolveIdentity(db, "")
	agent := MustGetAgent(info)

	if err := deleteLink(db, fromID, toID, rel); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// For parent/child, also clear items.parent_item if matching
	if rel == "parent" {
		if item, _ := getItem(db, toID); item != nil && item.ParentItem != nil && *item.ParentItem == fromID {
			moveItem(db, toID, nil)
		}
	} else if rel == "child" {
		if item, _ := getItem(db, fromID); item != nil && item.ParentItem != nil && *item.ParentItem == toID {
			moveItem(db, fromID, nil)
		}
	}

	createEvent(db, &fromID, agent, VerbUnlinked, fmt.Sprintf(`{"rel":%q,"from":%d,"to":%d}`, rel, fromID, toID))
	out.Println("Link deleted")
}

func cmdLinkLs(out *Output, args []string) {
	var itemID *int64
	rel := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rel":
			if i+1 < len(args) {
				i++
				rel = args[i]
			}
		default:
			// Assume it's an ID
			if id, _, err := resolveDisplayID(args[i]); err == nil {
				itemID = &id
			}
		}
	}

	links, err := listLinks(db, itemID, rel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Build item map for display labels
	itemMap := make(map[int64]*Item)
	if len(links) > 0 {
		for _, l := range links {
			if _, ok := itemMap[l.FromItem]; !ok {
				if item, err := getItem(db, l.FromItem); err == nil {
					itemMap[l.FromItem] = item
				}
			}
			if _, ok := itemMap[l.ToItem]; !ok {
				if item, err := getItem(db, l.ToItem); err == nil {
					itemMap[l.ToItem] = item
				}
			}
		}
	}

	out.PrintLinks(links, itemMap)
}

// --- Agent commands ---

func routeAgent(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "agent: expected subcommand (register, ls, assert)")
		os.Exit(1)
	}

	sub := args[0]
	subargs := args[1:]

	switch sub {
	case "register":
		cmdAgentRegister(out, subargs)
	case "ls":
		cmdAgentLs(out, subargs)
	case "assert":
		cmdAgentAssert(out, subargs)
	default:
		fmt.Fprintf(os.Stderr, "agent: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
}

func cmdAgentRegister(out *Output, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mb agent register <name>")
		os.Exit(1)
	}
	name := args[0]

	token, err := GenerateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	tokenHash := HashToken(token)

	info, _ := ResolveIdentity(db, "")
	createdBy := info.Agent
	if createdBy == "" {
		createdBy = name // self-registration
	}

	agent, err := registerAgent(db, name, tokenHash, createdBy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	createEvent(db, nil, name, VerbAgentRegistered, fmt.Sprintf(`{"name":%q}`, name))

	out.Println("Agent registered:", agent.Name)
	out.Println("Token (show this once; store it securely):")
	out.Println(token)
}

func cmdAgentLs(out *Output, args []string) {
	agents, err := listAgents(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	out.PrintAgents(agents)
}

func cmdAgentAssert(out *Output, args []string) {
	token := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--token" && i+1 < len(args) {
			token = args[i+1]
			i++
		}
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "usage: mb agent assert --token <token>")
		os.Exit(1)
	}

	// Find the agent by trying all tokens
	agents, err := listAgents(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, a := range agents {
		if a.TokenHash != "" && VerifyToken(token, a.TokenHash) {
			createEvent(db, nil, a.Name, VerbAgentAsserted, `{"method":"assert_command"}`)
			out.Println("Asserted as", a.Name)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "error: token does not match any registered agent")
	os.Exit(1)
}

// --- Helpers ---

func getCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func parseIntOrExit(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid number %q\n", s)
		os.Exit(1)
	}
	return n
}

func formatOptInt64(v *int64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *v)
}
