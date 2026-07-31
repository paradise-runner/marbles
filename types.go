package main

import "fmt"

// Item represents a task or project in the single items table.
type Item struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`       // "task" or "project"
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Status     string `json:"status"` // "open", "review", or "closed"
	Priority   int    `json:"priority"` // 0=critical, 1=high, 2=med, 3=low
	ClaimedBy  string `json:"claimed_by,omitempty"`
	ParentItem *int64 `json:"parent_item,omitempty"`
	CwdHint    string `json:"cwd_hint,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	CreatedBy  string `json:"created_by"`
	UpdatedAt  int64  `json:"updated_at"`
	ClosedAt   *int64 `json:"closed_at,omitempty"`
}

// DisplayID returns the kind-prefixed ID for display (e.g. "T12", "P3").
func (i *Item) DisplayID() string {
	switch i.Kind {
	case "project":
		return fmt.Sprintf("P%d", i.ID)
	default:
		return fmt.Sprintf("T%d", i.ID)
	}
}

// Link represents a typed edge between two items.
type Link struct {
	ID        int64  `json:"id"`
	FromItem  int64  `json:"from_item"`
	ToItem    int64  `json:"to_item"`
	Rel       string `json:"rel"` // blocks, related, parent, child
	CreatedAt int64  `json:"created_at"`
	CreatedBy string `json:"created_by"`
}

// Comment represents a comment on an item.
type Comment struct {
	ID        int64  `json:"id"`
	Item      int64  `json:"item"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// Event represents an entry in the append-only audit trail.
type Event struct {
	ID     int64  `json:"id"`
	Item   *int64 `json:"item,omitempty"` // NULL for global events
	Actor  string `json:"actor"`
	Verb   string `json:"verb"`
	Detail string `json:"detail,omitempty"`
	At     int64  `json:"at"`
}

// Agent represents a registered agent identity.
type Agent struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TokenHash   string `json:"token_hash,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	CreatedBy   string `json:"created_by"`
}

// Priority strings for display/parsing.
var PriorityNames = map[string]int{
	"critical": 0,
	"high":     1,
	"med":      2,
	"low":      3,
}

var PriorityLabels = map[int]string{
	0: "critical",
	1: "high",
	2: "med",
	3: "low",
}

// Verb constants for audit trail.
const (
	VerbCreated       = "created"
	VerbClosed        = "closed"
	VerbReviewed      = "reviewed"
	VerbReopened      = "reopened"
	VerbReprio        = "reprio"
	VerbClaimed       = "claimed"
	VerbUnclaimed     = "unclaimed"
	VerbReclaimed     = "reclaimed"
	VerbTitleEdited   = "title_edited"
	VerbBodyEdited    = "body_edited"
	VerbMoved         = "moved"
	VerbPromoted      = "promoted"
	VerbLinked        = "linked"
	VerbUnlinked      = "unlinked"
	VerbCommented     = "commented"
	VerbAgentRegistered = "agent_registered"
	VerbAgentAsserted = "agent_asserted"
)
