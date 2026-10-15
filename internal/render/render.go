// Package render formats domain values for terminal output.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xnmp/gtasks-cli/internal/domain"
)

// DueLabel describes a due date relative to today.
func DueLabel(due, today domain.Date) string {
	switch n := today.DaysUntil(due); {
	case n == 0:
		return "due today"
	case n == 1:
		return "due tomorrow"
	case n == -1:
		return "overdue 1 day"
	case n < 0:
		return fmt.Sprintf("overdue %d days", -n)
	case n < 7:
		return fmt.Sprintf("due %s", due.Weekday().String()[:3])
	default:
		return "due " + due.String()
	}
}

// Rows writes an outline of tasks under a list heading.
func Rows(w io.Writer, list domain.TaskList, rows []domain.Row, today domain.Date) {
	fmt.Fprintln(w, oneLine(list.Title))
	if len(rows) == 0 {
		fmt.Fprintln(w, "  (no tasks)")
		return
	}
	for _, r := range rows {
		fmt.Fprintln(w, Row(r, today))
	}
}

// Row formats one task line: number, checkbox, indented title and annotations.
func Row(r domain.Row, today domain.Date) string {
	idx := ""
	if r.Index > 0 {
		idx = fmt.Sprintf("%d", r.Index)
	}
	box := "[ ]"
	if r.Task.IsDone() {
		box = "[x]"
	}
	var notes []string
	if r.Task.Due != nil && !r.Task.IsDone() {
		notes = append(notes, DueLabel(*r.Task.Due, today))
	}
	if r.Task.Notes != "" {
		notes = append(notes, "+notes")
	}
	line := fmt.Sprintf("%4s  %s %s%s", idx, box, strings.Repeat("  ", r.Depth), oneLine(r.Task.Title))
	if len(notes) > 0 {
		line += "  (" + strings.Join(notes, ", ") + ")"
	}
	return line
}

// Task writes every field of a single task.
func Task(w io.Writer, t domain.Task, today domain.Date) {
	status := "pending"
	if t.IsDone() {
		status = "completed"
		if t.Completed != nil {
			status += " " + t.Completed.Local().Format(time.DateTime)
		}
	}
	fmt.Fprintf(w, "Title:   %s\n", oneLine(t.Title))
	fmt.Fprintf(w, "ID:      %s\n", t.ID)
	fmt.Fprintf(w, "Status:  %s\n", status)
	if t.Due != nil {
		fmt.Fprintf(w, "Due:     %s (%s)\n", t.Due, DueLabel(*t.Due, today))
	}
	if t.Parent != "" {
		fmt.Fprintf(w, "Parent:  %s\n", t.Parent)
	}
	if !t.Updated.IsZero() {
		fmt.Fprintf(w, "Updated: %s\n", t.Updated.Local().Format(time.DateTime))
	}
	if t.Assigned {
		fmt.Fprintln(w, "Origin:  assigned from Google Docs/Chat")
	}
	if t.WebViewLink != "" {
		fmt.Fprintf(w, "Link:    %s\n", oneLine(t.WebViewLink))
	}
	if t.Notes != "" {
		fmt.Fprintf(w, "\n%s\n", Sanitize(t.Notes))
	}
}

func Lists(w io.Writer, lists []domain.TaskList) {
	if len(lists) == 0 {
		fmt.Fprintln(w, "(no lists)")
		return
	}
	titles := make([]string, len(lists))
	width := 0
	for i, l := range lists {
		titles[i] = oneLine(l.Title)
		width = max(width, len([]rune(titles[i])))
	}
	for i, l := range lists {
		pad := width - len([]rune(titles[i]))
		fmt.Fprintf(w, "%s%s  %s\n", titles[i], strings.Repeat(" ", pad), l.ID)
	}
}

func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// oneLine collapses whitespace so a title cannot break the outline layout,
// and strips control characters.
func oneLine(s string) string {
	return strings.Join(strings.Fields(Sanitize(s)), " ")
}

// Sanitize removes C0/C1 control characters (keeping newlines and tabs) so
// text from the API cannot inject terminal escape sequences.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
