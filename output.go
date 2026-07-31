package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"
)

// OutputFormat controls CLI output style.
type OutputFormat int

const (
	FormatHuman OutputFormat = iota
	FormatJSON
)

type Output struct {
	Format OutputFormat
	Quiet  bool
}

func NewOutput(jsonOutput, quiet bool) *Output {
	o := &Output{}
	if jsonOutput {
		o.Format = FormatJSON
	}
	if quiet {
		o.Quiet = true
	}
	return o
}

func (o *Output) PrintJSON(v interface{}) {
	if o.Format != FormatJSON {
		return
	}
	// A nil slice marshals to `null`; render it as `[]` instead so consumers
	// always get a JSON array for listings.
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func (o *Output) Println(a ...interface{}) {
	if o.Quiet {
		return
	}
	fmt.Println(a...)
}

func (o *Output) Printf(format string, a ...interface{}) {
	if o.Quiet {
		return
	}
	fmt.Printf(format, a...)
}

func (o *Output) Errorf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format, a...)
}

// --- Item list formatting ---

func (o *Output) PrintItems(items []*Item, showParent bool) {
	if o.Format == FormatJSON {
		o.PrintJSON(items)
		return
	}
	if len(items) == 0 {
		o.Println("No items.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	if showParent {
		fmt.Fprintln(w, "ID\tTitle\tStatus\tPriority\tClaimed\tParent")
	} else {
		fmt.Fprintln(w, "ID\tTitle\tStatus\tPriority\tClaimed")
	}

	for _, item := range items {
		pid := ""
		if item.ParentItem != nil {
			pid = fmt.Sprintf("%d", *item.ParentItem)
		}
		claimed := item.ClaimedBy
		if claimed == "" {
			claimed = "-"
		}
		prio := PriorityLabels[item.Priority]

		if showParent {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", item.DisplayID(), truncateTitle(item.Title), item.Status, prio, claimed, pid)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", item.DisplayID(), truncateTitle(item.Title), item.Status, prio, claimed)
		}
	}
	w.Flush()
}

func (o *Output) PrintProjects(items []*Item, counts map[int64]*ItemCounts) {
	if o.Format == FormatJSON {
		// Include counts in JSON output
		type projectWithCounts struct {
			*Item
			ChildCount int `json:"child_count"`
			OpenCount  int `json:"open_count"`
		}
		var enriched []*projectWithCounts
		for _, item := range items {
			c := &projectWithCounts{Item: item}
			if counts != nil {
				if cnt, ok := counts[item.ID]; ok {
					c.ChildCount = cnt.Total
					c.OpenCount = cnt.Open
				}
			}
			enriched = append(enriched, c)
		}
		o.PrintJSON(enriched)
		return
	}
	if len(items) == 0 {
		o.Println("No projects.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tTitle\tStatus\tPriority\tClaimed\tTasks\tOpen")
	for _, item := range items {
		claimed := item.ClaimedBy
		if claimed == "" {
			claimed = "-"
		}
		prio := PriorityLabels[item.Priority]
		total := 0
		open := 0
		if counts != nil {
			if cnt, ok := counts[item.ID]; ok {
				total = cnt.Total
				open = cnt.Open
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n", item.DisplayID(), truncateTitle(item.Title), item.Status, prio, claimed, total, open)
	}
	w.Flush()
}

func (o *Output) PrintTasks(items []*Item, links []*Link) {
	if o.Format == FormatJSON {
		// Enrich with blocking/blocked-by info
		type taskWithLinks struct {
			*Item
			Blocks    []int64 `json:"blocks,omitempty"`
			BlockedBy []int64 `json:"blocked_by,omitempty"`
		}
		var enriched []*taskWithLinks
		for _, item := range items {
			t := &taskWithLinks{Item: item}
			for _, l := range links {
				if l.Rel != "blocks" {
					continue
				}
				if l.FromItem == item.ID {
					// item blocks l.ToItem
					t.Blocks = append(t.Blocks, l.ToItem)
				}
				if l.ToItem == item.ID {
					// item is blocked by l.FromItem
					t.BlockedBy = append(t.BlockedBy, l.FromItem)
				}
			}
			enriched = append(enriched, t)
		}
		o.PrintJSON(enriched)
		return
	}
	if len(items) == 0 {
		o.Println("No tasks.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tTitle\tStatus\tPriority\tClaimed\tProject\tBlocks")

	for _, item := range items {
		claimed := item.ClaimedBy
		if claimed == "" {
			claimed = "-"
		}
		prio := PriorityLabels[item.Priority]
		pid := ""
		if item.ParentItem != nil {
			pid = fmt.Sprintf("%d", *item.ParentItem)
		}

		// Compute block markers
		var blocksMarkers []string
		for _, l := range links {
			if l.FromItem == item.ID && l.Rel == "blocks" {
				blocksMarkers = append(blocksMarkers, fmt.Sprintf("◄ %d", l.ToItem))
			}
			if l.ToItem == item.ID && l.Rel == "blocks" {
				blocksMarkers = append(blocksMarkers, fmt.Sprintf("► %d", l.FromItem))
			}
		}
		blocks := strings.Join(blocksMarkers, " ")

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.DisplayID(), truncateTitle(item.Title), item.Status, prio, claimed, pid, blocks)
	}
	w.Flush()
}

// --- Show item detail ---

func (o *Output) PrintItemDetail(item *Item, links []*Link, comments []*Comment, events []*Event) {
	if o.Format == FormatJSON {
		detail := map[string]interface{}{
			"item":     item,
			"links":    links,
			"comments": comments,
			"events":   events,
		}
		o.PrintJSON(detail)
		return
	}

	// Header
	fmt.Printf("%s  %s\n", item.DisplayID(), item.Title)
	fmt.Printf("  Kind: %s  Status: %s  Priority: %s\n", item.Kind, item.Status, PriorityLabels[item.Priority])
	if item.ClaimedBy != "" {
		fmt.Printf("  Claimed by: %s\n", item.ClaimedBy)
	}
	if item.ParentItem != nil {
		fmt.Printf("  Parent: %d\n", *item.ParentItem)
	}
	if item.CwdHint != "" {
		fmt.Printf("  Cwd: %s\n", item.CwdHint)
	}
	fmt.Printf("  Created: %s by %s\n", formatTime(item.CreatedAt), item.CreatedBy)
	fmt.Printf("  Updated: %s\n", formatTime(item.UpdatedAt))
	if item.ClosedAt != nil {
		fmt.Printf("  Closed: %s\n", formatTime(*item.ClosedAt))
	}
	if item.Body != "" {
		fmt.Printf("\n  Body:\n%s\n", indentText(item.Body, "    "))
	}

	// Links
	if len(links) > 0 {
		fmt.Println("\n  Links:")
		for _, l := range links {
			fmt.Printf("    %d --[%s]--> %d\n", l.FromItem, l.Rel, l.ToItem)
		}
	}

	// Comments
	if len(comments) > 0 {
		fmt.Println("\n  Comments:")
		for _, c := range comments {
			fmt.Printf("    [%s] %s: %s\n", formatTime(c.CreatedAt), c.Author, c.Body)
		}
	}

	// Recent events
	if len(events) > 0 {
		fmt.Println("\n  Recent events:")
		for _, e := range events {
			fmt.Printf("    [%s] %s %s %s\n", formatTime(e.At), e.Actor, e.Verb, e.Detail)
		}
	}
}

// --- Events/Log ---

func (o *Output) PrintEvents(events []*Event) {
	if o.Format == FormatJSON {
		o.PrintJSON(events)
		return
	}
	if len(events) == 0 {
		o.Println("No events.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Time\tActor\tVerb\tDetail")
	for _, e := range events {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", formatTime(e.At), e.Actor, e.Verb, e.Detail)
	}
	w.Flush()
}

// --- Links ---

func (o *Output) PrintLinks(links []*Link, items map[int64]*Item) {
	if o.Format == FormatJSON {
		o.PrintJSON(links)
		return
	}
	if len(links) == 0 {
		o.Println("No links.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "From\tRel\tTo\tCreated")
	for _, l := range links {
		fromLabel := fmt.Sprintf("%d", l.FromItem)
		toLabel := fmt.Sprintf("%d", l.ToItem)
		if items != nil {
			if item, ok := items[l.FromItem]; ok {
				fromLabel = item.DisplayID()
			}
			if item, ok := items[l.ToItem]; ok {
				toLabel = item.DisplayID()
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", fromLabel, l.Rel, toLabel, formatTime(l.CreatedAt))
	}
	w.Flush()
}

// --- Agents ---

func (o *Output) PrintAgents(agents []*Agent) {
	if o.Format == FormatJSON {
		o.PrintJSON(agents)
		return
	}
	if len(agents) == 0 {
		o.Println("No agents registered.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Name\tToken Hash\tFingerprint\tCreated")
	for _, a := range agents {
		fp := a.Fingerprint
		if fp == "" {
			fp = "-"
		}
		th := a.TokenHash
		if th == "" {
			th = "-"
		} else if len(th) > 16 {
			th = th[:16] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, th, fp, formatTime(a.CreatedAt))
	}
	w.Flush()
}

// --- Helpers ---

func formatTime(epoch int64) string {
	return time.Unix(epoch, 0).Format("2006-01-02 15:04:05")
}

const maxTitleLen = 52 // max visible title characters before truncation

// truncateTitle shortens a title so the tabwriter columns stay tidy
// even on narrow terminals.
func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= maxTitleLen {
		return title
	}
	return string(runes[:maxTitleLen-1]) + "…"
}

func indentText(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}


