package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// --- Items ---

func createItem(db *sql.DB, kind, title, body, createdBy string, priority int, parentItem *int64, cwdHint string) (*Item, error) {
	now := time.Now().Unix()
	// Convert empty cwd_hint to nil for proper NULL storage
	var cwdPtr interface{}
	if cwdHint != "" {
		cwdPtr = cwdHint
	}
	result, err := db.Exec(`INSERT INTO items (kind, title, body, status, priority, parent_item, cwd_hint, created_at, created_by, updated_at)
		VALUES (?, ?, ?, 'open', ?, ?, ?, ?, ?, ?)`,
		kind, title, body, priority, parentItem, cwdPtr, now, createdBy, now)
	if err != nil {
		return nil, fmt.Errorf("create item: %w", err)
	}
	id, _ := result.LastInsertId()
	return &Item{
		ID:         id,
		Kind:       kind,
		Title:      title,
		Body:       body,
		Status:     "open",
		Priority:   priority,
		ParentItem: parentItem,
		CwdHint:    cwdHint,
		CreatedAt:  now,
		CreatedBy:  createdBy,
		UpdatedAt:  now,
	}, nil
}

func getItem(db *sql.DB, id int64) (*Item, error) {
	var i Item
	var parentItem sql.NullInt64
	var closedAt sql.NullInt64
	var claimedBy, cwdHint, body sql.NullString
	err := db.QueryRow(`SELECT id, kind, title, body, status, priority, claimed_by, parent_item, cwd_hint, created_at, created_by, updated_at, closed_at
		FROM items WHERE id = ?`, id).Scan(
		&i.ID, &i.Kind, &i.Title, &body, &i.Status, &i.Priority, &claimedBy,
		&parentItem, &cwdHint, &i.CreatedAt, &i.CreatedBy, &i.UpdatedAt, &closedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("item %d not found", id)
		}
		return nil, fmt.Errorf("get item %d: %w", id, err)
	}
	if claimedBy.Valid {
		i.ClaimedBy = claimedBy.String
	}
	if parentItem.Valid {
		i.ParentItem = &parentItem.Int64
	}
	if closedAt.Valid {
		i.ClosedAt = &closedAt.Int64
	}
	if cwdHint.Valid {
		i.CwdHint = cwdHint.String
	}
	if body.Valid {
		i.Body = body.String
	}
	return &i, nil
}

func updateItemStatus(db *sql.DB, id int64, status string, actor string) error {
	now := time.Now().Unix()
	var closedAt interface{}
	if status == "closed" {
		closedAt = now
	} else {
		closedAt = nil
	}
	_, err := db.Exec(`UPDATE items SET status = ?, closed_at = ?, updated_at = ? WHERE id = ?`,
		status, closedAt, now, id)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

func updateItemPriority(db *sql.DB, id int64, priority int) error {
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET priority = ?, updated_at = ? WHERE id = ?`, priority, now, id)
	if err != nil {
		return fmt.Errorf("update priority: %w", err)
	}
	return nil
}

func claimItem(db *sql.DB, id int64, agent string) error {
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET claimed_by = ?, updated_at = ? WHERE id = ?`, agent, now, id)
	if err != nil {
		return fmt.Errorf("claim item: %w", err)
	}
	return nil
}

func unclaimItem(db *sql.DB, id int64) error {
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET claimed_by = NULL, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("unclaim item: %w", err)
	}
	return nil
}

func promoteItem(db *sql.DB, id int64) error {
	now := time.Now().Unix()
	result, err := db.Exec(`UPDATE items SET kind = 'project', updated_at = ? WHERE id = ? AND kind = 'task' AND status = 'open'`, now, id)
	if err != nil {
		return fmt.Errorf("promote item: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("item %d is not an open task (cannot promote)", id)
	}
	return nil
}

func updateItemTitle(db *sql.DB, id int64, title string) error {
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET title = ?, updated_at = ? WHERE id = ?`, title, now, id)
	return err
}

func updateItemBody(db *sql.DB, id int64, body string) error {
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET body = ?, updated_at = ? WHERE id = ?`, body, now, id)
	return err
}

func checkParentCycles(db *sql.DB, id int64, newParent *int64) error {
	if newParent == nil {
		return nil
	}
	if id == *newParent {
		return fmt.Errorf("self-parent not allowed")
	}
	// Check that newParent is not a descendant of id
	visited := map[int64]bool{}
	queue := []int64{*newParent}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == id {
			return fmt.Errorf("setting this parent would create a cycle")
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		rows, err := db.Query("SELECT id FROM items WHERE parent_item = ?", current)
		if err != nil {
			return err
		}
		for rows.Next() {
			var child int64
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return err
			}
			queue = append(queue, child)
		}
		rows.Close()
	}
	return nil
}

func moveItem(db *sql.DB, id int64, parent *int64) error {
	if err := checkParentCycles(db, id, parent); err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err := db.Exec(`UPDATE items SET parent_item = ?, updated_at = ? WHERE id = ?`, parent, now, id)
	return err
}

// listItems returns items matching the given filters.
type ListFilter struct {
	Kind          string // "task" or "project"
	IncludeOpen   bool   // include open items (default on)
	IncludeReview bool   // include review items (--review)
	IncludeClosed bool   // include closed items (--closed)
	Mine          bool   // claimed by current agent
	Claimed       bool   // has a claim
	Unclaimed     bool   // no claim
	Parent        *int64 // parent item ID
	Top           bool   // no parent (top-level)
	Sort          string // "created", "priority", "title"
	BlockedBy     *int64 // items blocked by this ID (blocking the given ID)
	Blocks        *int64 // items that block this ID (blocked by the given ID)
	Here          bool   // scope by cwd
	Search        string // text search in title
	CurrentDir    string // current working directory for --here
	Agent         string // current agent for --mine
	Limit         int
	Offset        int
}

func listItems(db *sql.DB, f ListFilter) ([]*Item, error) {
	query := `SELECT id, kind, title, body, status, priority, claimed_by, parent_item, cwd_hint, created_at, created_by, updated_at, closed_at FROM items WHERE 1=1`
	args := []interface{}{}

	if f.Kind != "" {
		query += " AND kind = ?"
		args = append(args, f.Kind)
	}

	// Status inclusion: open is on by default; review and closed are added
	// with --review and --closed respectively.
	var statuses []string
	if f.IncludeOpen {
		statuses = append(statuses, "'open'")
	}
	if f.IncludeReview {
		statuses = append(statuses, "'review'")
	}
	if f.IncludeClosed {
		statuses = append(statuses, "'closed'")
	}
	switch len(statuses) {
	case 0:
		query += " AND 0"
	case 1:
		query += fmt.Sprintf(" AND status = %s", statuses[0])
	default:
		query += " AND status IN (" + strings.Join(statuses, ",") + ")"
	}

	if f.Mine {
		query += " AND claimed_by = ?"
		args = append(args, f.Agent)
	}
	if f.Claimed {
		query += " AND claimed_by IS NOT NULL"
	}
	if f.Unclaimed {
		query += " AND claimed_by IS NULL"
	}

	if f.Parent != nil {
		query += " AND parent_item = ?"
		args = append(args, *f.Parent)
	}
	if f.Top {
		query += " AND parent_item IS NULL"
	}

	if f.Search != "" {
		query += " AND title LIKE ?"
		args = append(args, "%"+f.Search+"%")
	}

	// Filter by blocks relations
	if f.BlockedBy != nil {
		// Items that block the given ID (i.e., where to_item = givenId AND rel = blocks)
		// Or items that are blocked by the given ID (from_item = givenId AND rel = blocks)
		// --blocks-by T means: items blocked by T (where T blocks the item)
		query += " AND id IN (SELECT to_item FROM links WHERE from_item = ? AND rel = 'blocks')"
		args = append(args, *f.BlockedBy)
	}
	if f.Blocks != nil {
		// --blocks T means: items that block T (where the item blocks T)
		query += " AND id IN (SELECT from_item FROM links WHERE to_item = ? AND rel = 'blocks')"
		args = append(args, *f.Blocks)
	}

	if f.Here && f.CurrentDir != "" {
		// Scope to items whose cwd_hint is a prefix of the current directory,
		// or whose parent's cwd_hint is a prefix.
		query += " AND (cwd_hint IS NOT NULL AND ? LIKE (cwd_hint || '%') OR parent_item IN (SELECT id FROM items WHERE cwd_hint IS NOT NULL AND ? LIKE (cwd_hint || '%')))"
		args = append(args, f.CurrentDir, f.CurrentDir)
	}

	// Sorting
	switch f.Sort {
	case "priority":
		query += " ORDER BY priority ASC, created_at ASC"
	case "title":
		query += " ORDER BY title ASC"
	default:
		query += " ORDER BY priority ASC, created_at ASC"
	}

	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	if f.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, f.Offset)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	var items []*Item
	for rows.Next() {
		var i Item
		var parentItem sql.NullInt64
		var closedAt sql.NullInt64
		var claimedBy, cwdHint, body sql.NullString
		if err := rows.Scan(&i.ID, &i.Kind, &i.Title, &body, &i.Status, &i.Priority,
			&claimedBy, &parentItem, &cwdHint, &i.CreatedAt, &i.CreatedBy, &i.UpdatedAt, &closedAt); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		if claimedBy.Valid {
			i.ClaimedBy = claimedBy.String
		}
		if parentItem.Valid {
			i.ParentItem = &parentItem.Int64
		}
		if closedAt.Valid {
			i.ClosedAt = &closedAt.Int64
		}
		if cwdHint.Valid {
			i.CwdHint = cwdHint.String
		}
		if body.Valid {
			i.Body = body.String
		}
		items = append(items, &i)
	}
	return items, rows.Err()
}

// --- Links ---

func createLink(db *sql.DB, fromID, toID int64, rel, createdBy string) (*Link, error) {
	now := time.Now().Unix()
	result, err := db.Exec(`INSERT OR IGNORE INTO links (from_item, to_item, rel, created_at, created_by) VALUES (?, ?, ?, ?, ?)`,
		fromID, toID, rel, now, createdBy)
	if err != nil {
		return nil, fmt.Errorf("create link: %w", err)
	}
	id, _ := result.LastInsertId()
	if id == 0 {
		// Already exists; fetch existing
		row := db.QueryRow("SELECT id, from_item, to_item, rel, created_at, created_by FROM links WHERE from_item=? AND to_item=? AND rel=?", fromID, toID, rel)
		var l Link
		if err := row.Scan(&l.ID, &l.FromItem, &l.ToItem, &l.Rel, &l.CreatedAt, &l.CreatedBy); err != nil {
			return nil, fmt.Errorf("get existing link: %w", err)
		}
		return &l, nil
	}
	return &Link{
		ID:        id,
		FromItem:  fromID,
		ToItem:    toID,
		Rel:       rel,
		CreatedAt: now,
		CreatedBy: createdBy,
	}, nil
}

func deleteLink(db *sql.DB, fromID, toID int64, rel string) error {
	_, err := db.Exec(`DELETE FROM links WHERE from_item = ? AND to_item = ? AND rel = ?`, fromID, toID, rel)
	if err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	return nil
}

func listLinks(db *sql.DB, itemID *int64, rel string) ([]*Link, error) {
	query := `SELECT id, from_item, to_item, rel, created_at, created_by FROM links WHERE 1=1`
	args := []interface{}{}

	if itemID != nil {
		query += " AND (from_item = ? OR to_item = ?)"
		args = append(args, *itemID, *itemID)
	}
	if rel != "" {
		query += " AND rel = ?"
		args = append(args, rel)
	}
	query += " ORDER BY id"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()

	var links []*Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.FromItem, &l.ToItem, &l.Rel, &l.CreatedAt, &l.CreatedBy); err != nil {
			return nil, err
		}
		links = append(links, &l)
	}
	return links, rows.Err()
}

// checkBlockCycles checks if adding a "blocks" link from fromID to toID
// would create a cycle (i.e., toID already blocks fromID transitively).
func checkBlockCycles(db *sql.DB, fromID, toID int64) error {
	if fromID == toID {
		return fmt.Errorf("self-links not allowed")
	}
	// Simple BFS from toID following "blocks" edges to see if we reach fromID.
	visited := map[int64]bool{}
	queue := []int64{toID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == fromID {
			return fmt.Errorf("adding this blocks link would create a cycle")
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		rows, err := db.Query("SELECT to_item FROM links WHERE from_item = ? AND rel = 'blocks'", current)
		if err != nil {
			return err
		}
		for rows.Next() {
			var next int64
			if err := rows.Scan(&next); err != nil {
				rows.Close()
				return err
			}
			queue = append(queue, next)
		}
		rows.Close()
	}
	return nil
}

// --- Comments ---

func createComment(db *sql.DB, itemID int64, author, body string) (*Comment, error) {
	now := time.Now().Unix()
	result, err := db.Exec(`INSERT INTO comments (item, author, body, created_at) VALUES (?, ?, ?, ?)`,
		itemID, author, body, now)
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	id, _ := result.LastInsertId()
	return &Comment{
		ID:        id,
		Item:      itemID,
		Author:    author,
		Body:      body,
		CreatedAt: now,
	}, nil
}

func listComments(db *sql.DB, itemID int64) ([]*Comment, error) {
	rows, err := db.Query("SELECT id, item, author, body, created_at FROM comments WHERE item = ? ORDER BY created_at ASC", itemID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var comments []*Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.Item, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, &c)
	}
	return comments, rows.Err()
}

// --- Events ---

func createEvent(db *sql.DB, item *int64, actor, verb, detail string) error {
	_, err := db.Exec(`INSERT INTO events (item, actor, verb, detail, at) VALUES (?, ?, ?, ?, ?)`,
		item, actor, verb, detail, time.Now().Unix())
	return err
}

func listEvents(db *sql.DB, itemID *int64, limit int) ([]*Event, error) {
	query := `SELECT id, item, actor, verb, detail, at FROM events WHERE 1=1`
	args := []interface{}{}

	if itemID != nil {
		query += " AND item = ?"
		args = append(args, *itemID)
	}
	query += " ORDER BY at DESC, id DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		var e Event
		var item sql.NullInt64
		if err := rows.Scan(&e.ID, &item, &e.Actor, &e.Verb, &e.Detail, &e.At); err != nil {
			return nil, err
		}
		if item.Valid {
			e.Item = &item.Int64
		}
		events = append(events, &e)
	}
	return events, rows.Err()
}

// --- Agents ---

func registerAgent(db *sql.DB, name, tokenHash, createdBy string) (*Agent, error) {
	now := time.Now().Unix()
	result, err := db.Exec(`INSERT INTO agents (name, token_hash, created_at, created_by) VALUES (?, ?, ?, ?)`,
		name, tokenHash, now, createdBy)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("agent %q already registered", name)
		}
		return nil, fmt.Errorf("register agent: %w", err)
	}
	id, _ := result.LastInsertId()
	return &Agent{
		ID:        id,
		Name:      name,
		TokenHash: tokenHash,
		CreatedAt: now,
		CreatedBy: createdBy,
	}, nil
}

func getAgent(db *sql.DB, name string) (*Agent, error) {
	var a Agent
	err := db.QueryRow("SELECT id, name, token_hash, fingerprint, created_at, created_by FROM agents WHERE name = ?", name).
		Scan(&a.ID, &a.Name, &a.TokenHash, &a.Fingerprint, &a.CreatedAt, &a.CreatedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent %q not found", name)
		}
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return &a, nil
}

func listAgents(db *sql.DB) ([]*Agent, error) {
	rows, err := db.Query("SELECT id, name, token_hash, fingerprint, created_at, created_by FROM agents ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Name, &a.TokenHash, &a.Fingerprint, &a.CreatedAt, &a.CreatedBy); err != nil {
			return nil, err
		}
		agents = append(agents, &a)
	}
	return agents, rows.Err()
}

func updateAgentFingerprint(db *sql.DB, name, fingerprint string) error {
	_, err := db.Exec("UPDATE agents SET fingerprint = ? WHERE name = ?", fingerprint, name)
	return err
}

// --- syncParentLinks syncs parent/child link rows with items.parent_item ---

func syncParentLinks(db *sql.DB, itemID int64, oldParent, newParent *int64, actor string) error {
	now := time.Now().Unix()
	// Remove old parent link if exists
	if oldParent != nil {
		db.Exec("DELETE FROM links WHERE from_item = ? AND rel = 'child' AND to_item = ?", itemID, *oldParent)
		db.Exec("DELETE FROM links WHERE from_item = ? AND rel = 'parent' AND to_item = ?", *oldParent, itemID)
	}
	// Add new parent link if exists
	if newParent != nil {
		db.Exec("INSERT OR IGNORE INTO links (from_item, to_item, rel, created_at, created_by) VALUES (?, ?, 'parent', ?, ?)", *newParent, itemID, now, actor)
		db.Exec("INSERT OR IGNORE INTO links (from_item, to_item, rel, created_at, created_by) VALUES (?, ?, 'child', ?, ?)", itemID, *newParent, now, actor)
	}
	return nil
}

// getItemCounts returns counts for a project (child task count, open count).
type ItemCounts struct {
	Total int
	Open  int
}

func getChildCounts(db *sql.DB, parentID int64) (*ItemCounts, error) {
	var c ItemCounts
	err := db.QueryRow("SELECT COUNT(*), SUM(CASE WHEN status='open' THEN 1 ELSE 0 END) FROM items WHERE parent_item = ?", parentID).Scan(&c.Total, &c.Open)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
