package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
)

// MinIDPrefix is the shortest ID prefix accepted as a task reference.
const MinIDPrefix = 4

// ResolveOpts carries context for interpreting a task reference.
type ResolveOpts struct {
	// Numbering holds the task IDs the user last saw numbered by `ls`
	// (Numbering[0] is #1). Numbers resolve against it rather than the live
	// list, so tasks added or reordered elsewhere cannot shift what #N means.
	Numbering []string
	// Prefer breaks ties between tasks sharing a title: when exactly one
	// match has this status it wins. Empty means StatusNeedsAction.
	Prefer Status
}

// ResolveTask finds the task a user reference points to. In priority order a
// reference may be: a number from the last `ls`, an exact task ID, a unique
// ID prefix of at least MinIDPrefix characters, or an exact case-insensitive
// title. A number outside the saved numbering falls through to ID and title
// matching, so numeric titles stay reachable.
func ResolveTask(ref string, tasks []Task, opts ResolveOpts) (Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Task{}, fmt.Errorf("empty task reference: %w", ErrNotFound)
	}
	n, numErr := strconv.Atoi(ref)
	isNumber := numErr == nil
	if isNumber && n >= 1 && n <= len(opts.Numbering) {
		id := opts.Numbering[n-1]
		for _, t := range tasks {
			if t.ID == id {
				return t, nil
			}
		}
		return Task{}, fmt.Errorf("task #%d from your last `gtasks ls` no longer exists (run `gtasks ls` again): %w", n, ErrNotFound)
	}

	for _, t := range tasks {
		if t.ID == ref {
			return t, nil
		}
	}
	if len(ref) >= MinIDPrefix {
		if m := filter(tasks, func(t Task) bool { return strings.HasPrefix(t.ID, ref) }); len(m) > 0 {
			return single(ref, m)
		}
	}
	m := filter(tasks, func(t Task) bool { return strings.EqualFold(t.Title, ref) })
	if len(m) > 1 {
		prefer := opts.Prefer
		if prefer == "" {
			prefer = StatusNeedsAction
		}
		if p := filter(m, func(t Task) bool { return t.Status == prefer }); len(p) == 1 {
			return p[0], nil
		}
	}
	if len(m) == 0 && isNumber {
		return Task{}, fmt.Errorf("no task #%d in your last `gtasks ls` (run it to number tasks): %w", n, ErrNotFound)
	}
	return single(ref, m)
}

func single(ref string, m []Task) (Task, error) {
	switch len(m) {
	case 0:
		return Task{}, fmt.Errorf("task %q: %w", ref, ErrNotFound)
	case 1:
		return m[0], nil
	}
	names := make([]string, len(m))
	for i, t := range m {
		names[i] = fmt.Sprintf("%q (%s)", t.Title, t.ID)
	}
	return Task{}, fmt.Errorf("task %q matches %d tasks: %s: %w", ref, len(m), strings.Join(names, ", "), ErrAmbiguous)
}

func filter[T any](xs []T, pred func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if pred(x) {
			out = append(out, x)
		}
	}
	return out
}

// TasksOf extracts the tasks from rows, preserving order.
func TasksOf(rows []Row) []Task {
	out := make([]Task, len(rows))
	for i, r := range rows {
		out[i] = r.Task
	}
	return out
}

// Numbering returns the IDs of numbered rows in number order.
func Numbering(rows []Row) []string {
	var ids []string
	for _, r := range rows {
		if r.Index > 0 {
			ids = append(ids, r.Task.ID)
		}
	}
	return ids
}

// ListMatch controls how loosely a list reference may match.
type ListMatch int

const (
	// MatchPrefix also accepts a unique case-insensitive title prefix.
	MatchPrefix ListMatch = iota
	// MatchExact accepts only an ID or an exact case-insensitive title; used
	// for destructive operations so a typo or empty variable cannot hit
	// another list.
	MatchExact
)

// ResolveList finds a task list by exact ID, exact case-insensitive title, or
// (with MatchPrefix) unique case-insensitive title prefix.
func ResolveList(ref string, lists []TaskList, mode ListMatch) (TaskList, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return TaskList{}, fmt.Errorf("empty list reference: %w", ErrNotFound)
	}
	for _, l := range lists {
		if l.ID == ref {
			return l, nil
		}
	}
	m := filter(lists, func(l TaskList) bool { return strings.EqualFold(l.Title, ref) })
	if len(m) == 0 && mode == MatchPrefix {
		lower := strings.ToLower(ref)
		m = filter(lists, func(l TaskList) bool { return strings.HasPrefix(strings.ToLower(l.Title), lower) })
	}
	switch len(m) {
	case 0:
		return TaskList{}, fmt.Errorf("list %q: %w", ref, ErrNotFound)
	case 1:
		return m[0], nil
	}
	names := make([]string, len(m))
	for i, l := range m {
		names[i] = fmt.Sprintf("%q (%s)", l.Title, l.ID)
	}
	return TaskList{}, fmt.Errorf("list %q matches %d lists: %s: %w", ref, len(m), strings.Join(names, ", "), ErrAmbiguous)
}

// Descendants returns every task nested (at any depth) under the task with id.
func Descendants(tasks []Task, id string) []Task {
	children := make(map[string][]Task)
	for _, t := range tasks {
		if t.Parent != "" && t.Parent != t.ID {
			children[t.Parent] = append(children[t.Parent], t)
		}
	}
	var out []Task
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c)
				queue = append(queue, c.ID)
			}
		}
	}
	return out
}
