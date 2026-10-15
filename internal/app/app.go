// Package app implements the CLI's use cases on top of abstract ports.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xnmp/gtasks-cli/internal/domain"
)

// DefaultList is the Tasks API alias for the user's default list.
const DefaultList = "@default"

// Store is the persistence port. Implementations talk to Google Tasks; tests
// use an in-memory fake.
type Store interface {
	Lists(ctx context.Context) ([]domain.TaskList, error)
	GetList(ctx context.Context, id string) (domain.TaskList, error)
	CreateList(ctx context.Context, title string) (domain.TaskList, error)
	RenameList(ctx context.Context, id, title string) (domain.TaskList, error)
	DeleteList(ctx context.Context, id string) error

	// Tasks returns every task in the list, including completed, hidden and
	// assigned ones.
	Tasks(ctx context.Context, listID string) ([]domain.Task, error)
	CreateTask(ctx context.Context, listID, parentID string, t domain.NewTask) (domain.Task, error)
	UpdateTask(ctx context.Context, listID, taskID string, p domain.TaskPatch) (domain.Task, error)
	DeleteTask(ctx context.Context, listID, taskID string) error
	// ClearCompleted hides all completed tasks in the list.
	ClearCompleted(ctx context.Context, listID string) error
}

// NumberStore remembers, per list, which task IDs the last `ls` numbered.
type NumberStore interface {
	// Load returns nil (and no error) when nothing was saved for the list.
	Load(listID string) ([]string, error)
	Save(listID string, ids []string) error
	// Forget drops the saved numbering for the list.
	Forget(listID string) error
}

type Service struct {
	Store   Store
	Numbers NumberStore
	Today   func() domain.Date
}

// ResolveList maps a user list reference ("" meaning the default list) to a list.
func (s Service) ResolveList(ctx context.Context, ref string) (domain.TaskList, error) {
	if ref == "" || ref == DefaultList {
		return s.Store.GetList(ctx, DefaultList)
	}
	lists, err := s.Store.Lists(ctx)
	if err != nil {
		return domain.TaskList{}, err
	}
	return domain.ResolveList(ref, lists, domain.MatchPrefix)
}

// resolveListExact is ResolveList for destructive operations: the reference
// must be non-empty and name a list exactly.
func (s Service) resolveListExact(ctx context.Context, ref string) (domain.TaskList, error) {
	switch strings.TrimSpace(ref) {
	case "":
		return domain.TaskList{}, errors.New("list name is empty")
	case DefaultList:
		return s.Store.GetList(ctx, DefaultList)
	}
	lists, err := s.Store.Lists(ctx)
	if err != nil {
		return domain.TaskList{}, err
	}
	return domain.ResolveList(ref, lists, domain.MatchExact)
}

// Snapshot is a list and its tasks in display order.
type Snapshot struct {
	List domain.TaskList `json:"list"`
	Rows []domain.Row    `json:"rows"`
}

func (s Service) snapshot(ctx context.Context, list domain.TaskList) (Snapshot, error) {
	tasks, err := s.Store.Tasks(ctx, list.ID)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{List: list, Rows: domain.Arrange(tasks)}, nil
}

func (s Service) Snapshot(ctx context.Context, listRef string) (Snapshot, error) {
	list, err := s.ResolveList(ctx, listRef)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, list)
}

// ErrNumbersNotSaved reports that `ls` numbering could not be remembered;
// the listing is still valid but carries no numbers.
var ErrNumbersNotSaved = errors.New("could not save task numbers")

// Listing returns the list for display. With remember, it records the
// numbering so later commands resolve #N to the task shown here. Without it
// (e.g. machine-readable output from scripts) rows carry no numbers and the
// user's saved numbering is left untouched.
//
// If the numbering cannot be saved, the stale saved numbering is dropped and
// rows are returned unnumbered with ErrNumbersNotSaved, so the screen never
// shows numbers that later commands would interpret differently.
func (s Service) Listing(ctx context.Context, listRef string, includeDone, remember bool) (Snapshot, error) {
	snap, err := s.Snapshot(ctx, listRef)
	if err != nil {
		return Snapshot{}, err
	}
	var saveErr error
	if remember {
		saveErr = s.Numbers.Save(snap.List.ID, domain.Numbering(snap.Rows))
		if saveErr != nil {
			_ = s.Numbers.Forget(snap.List.ID) // best effort; failure leaves old numbers that we warn about
		}
	}
	if !remember || saveErr != nil {
		snap.Rows = domain.Unnumbered(snap.Rows)
	}
	if !includeDone {
		snap.Rows = domain.PendingView(snap.Rows)
	}
	if saveErr != nil {
		return snap, fmt.Errorf("%w (numbers hidden; refer to tasks by title or ID): %v", ErrNumbersNotSaved, saveErr)
	}
	return snap, nil
}

func (s Service) resolveOpts(listID string, prefer domain.Status) (domain.ResolveOpts, error) {
	nums, err := s.Numbers.Load(listID)
	if err != nil {
		return domain.ResolveOpts{}, fmt.Errorf("load task numbers: %w", err)
	}
	return domain.ResolveOpts{Numbering: nums, Prefer: prefer}, nil
}

func (s Service) resolveOne(snap Snapshot, ref string, prefer domain.Status) (domain.Task, error) {
	opts, err := s.resolveOpts(snap.List.ID, prefer)
	if err != nil {
		return domain.Task{}, err
	}
	return domain.ResolveTask(ref, domain.TasksOf(snap.Rows), opts)
}

// resolveAll resolves every reference against one snapshot before anything
// is mutated; any failure aborts the whole command. Duplicates collapse.
func (s Service) resolveAll(snap Snapshot, refs []string, prefer domain.Status) ([]domain.Task, error) {
	if len(refs) == 0 {
		return nil, errors.New("no task given")
	}
	opts, err := s.resolveOpts(snap.List.ID, prefer)
	if err != nil {
		return nil, err
	}
	tasks := domain.TasksOf(snap.Rows)
	seen := make(map[string]bool, len(refs))
	out := make([]domain.Task, 0, len(refs))
	for _, ref := range refs {
		t, err := domain.ResolveTask(ref, tasks, opts)
		if err != nil {
			return nil, err
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	return out, nil
}

type AddInput struct {
	ListRef   string
	ParentRef string
	Title     string
	Notes     string
	Due       string
}

func (s Service) Add(ctx context.Context, in AddInput) (domain.Task, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return domain.Task{}, errors.New("task title is empty")
	}
	nt := domain.NewTask{Title: title, Notes: in.Notes}
	if in.Due != "" {
		d, err := domain.ParseDue(in.Due, s.Today())
		if err != nil {
			return domain.Task{}, err
		}
		nt.Due = &d
	}
	var listID, parentID string
	if in.ParentRef != "" {
		snap, err := s.Snapshot(ctx, in.ListRef)
		if err != nil {
			return domain.Task{}, err
		}
		parent, err := s.resolveOne(snap, in.ParentRef, domain.StatusNeedsAction)
		if err != nil {
			return domain.Task{}, fmt.Errorf("parent: %w", err)
		}
		listID, parentID = snap.List.ID, parent.ID
	} else {
		list, err := s.ResolveList(ctx, in.ListRef)
		if err != nil {
			return domain.Task{}, err
		}
		listID = list.ID
	}
	return s.Store.CreateTask(ctx, listID, parentID, nt)
}

// SetStatus marks the referenced tasks completed or pending. Referencing a
// task already in that status is an error, reported before anything changes.
func (s Service) SetStatus(ctx context.Context, listRef string, refs []string, status domain.Status) ([]domain.Task, error) {
	snap, err := s.Snapshot(ctx, listRef)
	if err != nil {
		return nil, err
	}
	// Among same-titled tasks, prefer the one not yet in the target status.
	prefer := domain.StatusNeedsAction
	if status == domain.StatusNeedsAction {
		prefer = domain.StatusCompleted
	}
	targets, err := s.resolveAll(snap, refs, prefer)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if t.Status == status {
			state := "pending"
			if status == domain.StatusCompleted {
				state = "completed"
			}
			return nil, fmt.Errorf("%q is already %s", t.Title, state)
		}
	}
	return apply(snap.List.ID, targets, func(listID string, t domain.Task) (domain.Task, error) {
		return s.Store.UpdateTask(ctx, listID, t.ID, domain.TaskPatch{Status: &status})
	})
}

// DeletePlan describes a task about to be deleted and what goes with it.
type DeletePlan struct {
	Task domain.Task
	// Subtasks are nested under Task; Google deletes them along with it.
	Subtasks []domain.Task
}

// Risky reports whether deleting needs explicit confirmation: it removes
// subtasks or an assignment that lives on in Docs/Chat.
func (p DeletePlan) Risky() bool {
	return p.Task.Assigned || len(p.Subtasks) > 0
}

// Delete removes the referenced tasks after approve accepts the plan. A
// rejection from approve aborts without deleting anything.
func (s Service) Delete(ctx context.Context, listRef string, refs []string, approve func([]DeletePlan) error) ([]domain.Task, error) {
	snap, err := s.Snapshot(ctx, listRef)
	if err != nil {
		return nil, err
	}
	targets, err := s.resolveAll(snap, refs, domain.StatusNeedsAction)
	if err != nil {
		return nil, err
	}
	all := domain.TasksOf(snap.Rows)
	plans := make([]DeletePlan, len(targets))
	for i, t := range targets {
		plans[i] = DeletePlan{Task: t, Subtasks: domain.Descendants(all, t.ID)}
	}
	if err := approve(plans); err != nil {
		return nil, err
	}
	// Delete deepest first, so explicitly named subtasks are deleted (and
	// reported) whether or not the API cascades a parent's deletion.
	depth := make(map[string]int, len(snap.Rows))
	for _, r := range snap.Rows {
		depth[r.Task.ID] = r.Depth
	}
	ordered := slices.Clone(targets)
	slices.SortStableFunc(ordered, func(a, b domain.Task) int { return depth[b.ID] - depth[a.ID] })
	return apply(snap.List.ID, ordered, func(listID string, t domain.Task) (domain.Task, error) {
		return t, s.Store.DeleteTask(ctx, listID, t.ID)
	})
}

// apply runs op on each task in order, stopping at the first failure and
// returning the tasks already changed alongside the error.
func apply(listID string, targets []domain.Task, op func(string, domain.Task) (domain.Task, error)) ([]domain.Task, error) {
	done := make([]domain.Task, 0, len(targets))
	for _, t := range targets {
		r, err := op(listID, t)
		if err != nil {
			return done, fmt.Errorf("%q: %w", t.Title, err)
		}
		done = append(done, r)
	}
	return done, nil
}

type EditInput struct {
	ListRef string
	Ref     string
	Title   *string
	Notes   *string
	Due     *string // "" clears the due date
}

func (s Service) Edit(ctx context.Context, in EditInput) (domain.Task, error) {
	var p domain.TaskPatch
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			return domain.Task{}, errors.New("task title is empty")
		}
		p.Title = &t
	}
	p.Notes = in.Notes
	if in.Due != nil {
		if *in.Due == "" {
			p.Due = &domain.DueChange{Clear: true}
		} else {
			d, err := domain.ParseDue(*in.Due, s.Today())
			if err != nil {
				return domain.Task{}, err
			}
			p.Due = &domain.DueChange{Date: d}
		}
	}
	if p.IsEmpty() {
		return domain.Task{}, errors.New("nothing to change (use --title, --notes, --due or --no-due)")
	}
	snap, err := s.Snapshot(ctx, in.ListRef)
	if err != nil {
		return domain.Task{}, err
	}
	t, err := s.resolveOne(snap, in.Ref, domain.StatusNeedsAction)
	if err != nil {
		return domain.Task{}, err
	}
	return s.Store.UpdateTask(ctx, snap.List.ID, t.ID, p)
}

func (s Service) Show(ctx context.Context, listRef, ref string) (domain.Task, error) {
	snap, err := s.Snapshot(ctx, listRef)
	if err != nil {
		return domain.Task{}, err
	}
	return s.resolveOne(snap, ref, domain.StatusNeedsAction)
}

func (s Service) ClearCompleted(ctx context.Context, listRef string) (domain.TaskList, error) {
	list, err := s.ResolveList(ctx, listRef)
	if err != nil {
		return domain.TaskList{}, err
	}
	return list, s.Store.ClearCompleted(ctx, list.ID)
}

func (s Service) CreateList(ctx context.Context, title string) (domain.TaskList, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.TaskList{}, errors.New("list title is empty")
	}
	return s.Store.CreateList(ctx, title)
}

// RenameList renames a list named exactly by ref.
func (s Service) RenameList(ctx context.Context, ref, title string) (domain.TaskList, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.TaskList{}, errors.New("list title is empty")
	}
	l, err := s.resolveListExact(ctx, ref)
	if err != nil {
		return domain.TaskList{}, err
	}
	return s.Store.RenameList(ctx, l.ID, title)
}

// ListDeletion is a list about to be deleted and the tasks that go with it.
type ListDeletion struct {
	List     domain.TaskList
	Tasks    int
	Assigned int
}

// PlanListDeletion resolves a list named exactly by ref and counts its contents.
func (s Service) PlanListDeletion(ctx context.Context, ref string) (ListDeletion, error) {
	l, err := s.resolveListExact(ctx, ref)
	if err != nil {
		return ListDeletion{}, err
	}
	tasks, err := s.Store.Tasks(ctx, l.ID)
	if err != nil {
		return ListDeletion{}, err
	}
	plan := ListDeletion{List: l, Tasks: len(tasks)}
	for _, t := range tasks {
		if t.Assigned {
			plan.Assigned++
		}
	}
	return plan, nil
}

func (s Service) DeleteList(ctx context.Context, plan ListDeletion) error {
	return s.Store.DeleteList(ctx, plan.List.ID)
}
