// Package domain holds the pure model and rules of the CLI: tasks, lists,
// display ordering and reference resolution. It performs no I/O.
package domain

import (
	"slices"
	"sort"
	"time"
)

type Status string

const (
	StatusNeedsAction Status = "needsAction"
	StatusCompleted   Status = "completed"
)

type TaskList struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Notes       string     `json:"notes,omitempty"`
	Status      Status     `json:"status"`
	Due         *Date      `json:"due,omitempty"`
	Completed   *time.Time `json:"completed,omitempty"`
	Parent      string     `json:"parent,omitempty"`
	Position    string     `json:"position"`
	Updated     time.Time  `json:"updated"`
	WebViewLink string     `json:"webViewLink,omitempty"`
	// Assigned marks a task assigned from Google Docs or Chat Spaces;
	// deleting it also deletes the original assignment there.
	Assigned bool `json:"assigned,omitempty"`
}

func (t Task) IsDone() bool { return t.Status == StatusCompleted }

// NewTask is the data needed to create a task.
type NewTask struct {
	Title string
	Notes string
	Due   *Date
}

// DueChange sets or clears a due date.
type DueChange struct {
	Clear bool
	Date  Date
}

// TaskPatch describes a partial update; nil fields are left unchanged.
type TaskPatch struct {
	Title  *string
	Notes  *string
	Due    *DueChange
	Status *Status
}

func (p TaskPatch) IsEmpty() bool {
	return p.Title == nil && p.Notes == nil && p.Due == nil && p.Status == nil
}

// Row is a task placed in display order.
type Row struct {
	Task  Task `json:"task"`
	Depth int  `json:"depth"`
	// Index is the 1-based number users can refer to the task by, or 0 for
	// completed tasks (which are referenced by ID instead).
	Index int `json:"index,omitempty"`
}

// Arrange orders tasks as an outline: each parent is followed by its
// subtasks, siblings sorted by their API position. Tasks whose parent is
// absent from the input are treated as top-level. Pending tasks are numbered
// consecutively in that order. The input slice is not modified.
func Arrange(tasks []Task) []Row {
	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		ids[t.ID] = true
	}
	children := make(map[string][]Task)
	for _, t := range tasks {
		parent := t.Parent
		if !ids[parent] || parent == t.ID {
			parent = ""
		}
		children[parent] = append(children[parent], t)
	}
	for _, sibs := range children {
		sort.SliceStable(sibs, func(i, j int) bool {
			if sibs[i].Position != sibs[j].Position {
				return sibs[i].Position < sibs[j].Position
			}
			return sibs[i].ID < sibs[j].ID
		})
	}

	rows := make([]Row, 0, len(tasks))
	visited := make(map[string]bool, len(tasks))
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, t := range children[parent] {
			if visited[t.ID] {
				continue
			}
			visited[t.ID] = true
			rows = append(rows, Row{Task: t, Depth: depth})
			walk(t.ID, depth+1)
		}
	}
	walk("", 0)
	// Parent cycles (malformed data) are unreachable from the root; surface them flat.
	for _, t := range tasks {
		if !visited[t.ID] {
			visited[t.ID] = true
			rows = append(rows, Row{Task: t})
		}
	}

	n := 0
	for i := range rows {
		if !rows[i].Task.IsDone() {
			n++
			rows[i].Index = n
		}
	}
	return rows
}

// Unnumbered returns a copy of rows with every Index cleared.
func Unnumbered(rows []Row) []Row {
	out := slices.Clone(rows)
	for i := range out {
		out[i].Index = 0
	}
	return out
}

// PendingView keeps pending rows plus any completed ancestors they need for
// context, so numbering and nesting match the full Arrange output.
func PendingView(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for i, r := range rows {
		if !r.Task.IsDone() || hasPendingDescendant(rows, i) {
			out = append(out, r)
		}
	}
	return out
}

func hasPendingDescendant(rows []Row, i int) bool {
	for j := i + 1; j < len(rows) && rows[j].Depth > rows[i].Depth; j++ {
		if !rows[j].Task.IsDone() {
			return true
		}
	}
	return false
}
