package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xnmp/gtasks-cli/internal/domain"
)

// fakeStore is an in-memory Store. Mutating calls are recorded in mutations.
type fakeStore struct {
	lists     []domain.TaskList
	tasks     map[string][]domain.Task
	nextID    int
	mutations []string
	lowPos    int // new tasks are inserted before all others, as the real API does
	highPos   int

	// failUpdateAt, when > 0, makes the Nth UpdateTask/DeleteTask call fail.
	failAt   int
	failedBy int
}

var errBoom = errors.New("boom")

func newFake(listTitles ...string) *fakeStore {
	f := &fakeStore{tasks: map[string][]domain.Task{}, lowPos: 1_000_000, highPos: 1_000_000}
	for _, t := range listTitles {
		f.addList(t)
	}
	return f
}

func (f *fakeStore) id(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s%04d", prefix, f.nextID)
}

func (f *fakeStore) addList(title string) domain.TaskList {
	l := domain.TaskList{ID: f.id("list"), Title: title}
	f.lists = append(f.lists, l)
	return l
}

// seed adds a task directly (not recorded as a mutation).
func (f *fakeStore) seed(listID, title, parent string, done bool) domain.Task {
	st := domain.StatusNeedsAction
	if done {
		st = domain.StatusCompleted
	}
	t := domain.Task{ID: f.id("task"), Title: title, Parent: parent, Status: st,
		Position: f.nextHigh()}
	f.tasks[listID] = append(f.tasks[listID], t)
	return t
}

func (f *fakeStore) nextHigh() string { f.highPos++; return fmt.Sprintf("%020d", f.highPos) }
func (f *fakeStore) nextLow() string  { f.lowPos--; return fmt.Sprintf("%020d", f.lowPos) }

func (f *fakeStore) find(listID, id string) (int, bool) {
	for i, t := range f.tasks[listID] {
		if t.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (f *fakeStore) record(s string) { f.mutations = append(f.mutations, s) }

func (f *fakeStore) shouldFail() bool {
	f.failedBy++
	return f.failAt > 0 && f.failedBy == f.failAt
}

func (f *fakeStore) Lists(context.Context) ([]domain.TaskList, error) {
	return append([]domain.TaskList(nil), f.lists...), nil
}

func (f *fakeStore) GetList(_ context.Context, id string) (domain.TaskList, error) {
	if id == DefaultList && len(f.lists) > 0 {
		return f.lists[0], nil
	}
	for _, l := range f.lists {
		if l.ID == id {
			return l, nil
		}
	}
	return domain.TaskList{}, fmt.Errorf("list %q: %w", id, domain.ErrNotFound)
}

func (f *fakeStore) CreateList(_ context.Context, title string) (domain.TaskList, error) {
	f.record("createList:" + title)
	return f.addList(title), nil
}

func (f *fakeStore) RenameList(_ context.Context, id, title string) (domain.TaskList, error) {
	f.record("renameList:" + id + ":" + title)
	for i := range f.lists {
		if f.lists[i].ID == id {
			f.lists[i].Title = title
			return f.lists[i], nil
		}
	}
	return domain.TaskList{}, domain.ErrNotFound
}

func (f *fakeStore) DeleteList(_ context.Context, id string) error {
	f.record("deleteList:" + id)
	for i := range f.lists {
		if f.lists[i].ID == id {
			f.lists = append(f.lists[:i], f.lists[i+1:]...)
			delete(f.tasks, id)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeStore) Tasks(_ context.Context, listID string) ([]domain.Task, error) {
	return append([]domain.Task(nil), f.tasks[listID]...), nil
}

func (f *fakeStore) CreateTask(_ context.Context, listID, parentID string, nt domain.NewTask) (domain.Task, error) {
	f.record("create:" + nt.Title)
	t := domain.Task{ID: f.id("task"), Title: nt.Title, Notes: nt.Notes, Due: nt.Due, Parent: parentID,
		Status: domain.StatusNeedsAction, Position: f.nextLow()}
	f.tasks[listID] = append(f.tasks[listID], t)
	return t, nil
}

func (f *fakeStore) UpdateTask(_ context.Context, listID, taskID string, p domain.TaskPatch) (domain.Task, error) {
	f.record("update:" + taskID)
	if f.shouldFail() {
		return domain.Task{}, errBoom
	}
	i, ok := f.find(listID, taskID)
	if !ok {
		return domain.Task{}, domain.ErrNotFound
	}
	t := f.tasks[listID][i]
	if p.Title != nil {
		t.Title = *p.Title
	}
	if p.Notes != nil {
		t.Notes = *p.Notes
	}
	if p.Due != nil {
		if p.Due.Clear {
			t.Due = nil
		} else {
			d := p.Due.Date
			t.Due = &d
		}
	}
	if p.Status != nil {
		t.Status = *p.Status
		if *p.Status == domain.StatusCompleted {
			now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
			t.Completed = &now
		} else {
			t.Completed = nil
		}
	}
	f.tasks[listID][i] = t
	return t, nil
}

func (f *fakeStore) DeleteTask(_ context.Context, listID, taskID string) error {
	f.record("delete:" + taskID)
	if f.shouldFail() {
		return errBoom
	}
	i, ok := f.find(listID, taskID)
	if !ok {
		return domain.ErrNotFound
	}
	f.tasks[listID] = append(f.tasks[listID][:i], f.tasks[listID][i+1:]...)
	return nil
}

func (f *fakeStore) ClearCompleted(_ context.Context, listID string) error {
	f.record("clear:" + listID)
	var keep []domain.Task
	for _, t := range f.tasks[listID] {
		if !t.IsDone() {
			keep = append(keep, t)
		}
	}
	f.tasks[listID] = keep
	return nil
}

// memNumbers is an in-memory app.NumberStore.
type memNumbers struct {
	m       map[string][]string
	saveErr error
	forgot  []string
}

func (n *memNumbers) Forget(id string) error {
	n.forgot = append(n.forgot, id)
	delete(n.m, id)
	return nil
}

func (n *memNumbers) Load(id string) ([]string, error) { return n.m[id], nil }
func (n *memNumbers) Save(id string, ids []string) error {
	if n.saveErr != nil {
		return n.saveErr
	}
	n.m[id] = ids
	return nil
}

func approveAll([]DeletePlan) error { return nil }

var today = domain.Date{Year: 2026, Month: time.October, Day: 15}

func svcOf(f *fakeStore) Service {
	return Service{Store: f, Numbers: &memNumbers{m: map[string][]string{}}, Today: func() domain.Date { return today }}
}

// seen returns a service whose saved numbering matches an `ls -a` of listRef.
func seen(t *testing.T, f *fakeStore, listRef string) Service {
	t.Helper()
	s := svcOf(f)
	if _, err := s.Listing(context.Background(), listRef, true, true); err != nil {
		t.Fatal(err)
	}
	return s
}

func statusOf(f *fakeStore, listID, id string) domain.Status {
	i, ok := f.find(listID, id)
	if !ok {
		return ""
	}
	return f.tasks[listID][i].Status
}

func TestSetStatusResolvesPositionalRefsBeforeMutating(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", false)
	b := f.seed(l, "B", "", false)
	c := f.seed(l, "C", "", false)

	got, err := seen(t, f, "").SetStatus(context.Background(), "", []string{"1", "2"}, domain.StatusCompleted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("changed = %+v, want A then B", got)
	}
	if statusOf(f, l, a.ID) != domain.StatusCompleted || statusOf(f, l, b.ID) != domain.StatusCompleted {
		t.Error("A and B should be completed")
	}
	if statusOf(f, l, c.ID) != domain.StatusNeedsAction {
		t.Error("C must remain pending (numbering must not shift after completing #1)")
	}
	for _, g := range got {
		if g.Status != domain.StatusCompleted {
			t.Errorf("returned task %s status = %s", g.Title, g.Status)
		}
	}
}

func TestSetStatusReopens(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", true)
	if _, err := seen(t, f, "").SetStatus(context.Background(), "", []string{a.ID}, domain.StatusNeedsAction); err != nil {
		t.Fatal(err)
	}
	if statusOf(f, l, a.ID) != domain.StatusNeedsAction {
		t.Error("task should be pending again")
	}
}

func TestSetStatusDuplicateRefsMutateOnce(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", false)
	f.seed(l, "B", "", false)
	for _, refs := range [][]string{{"1", "1"}, {"1", a.ID}, {a.ID, "A", "1"}} {
		f.mutations = nil
		got, err := seen(t, f, "").SetStatus(context.Background(), "", refs, domain.StatusCompleted)
		if err != nil {
			t.Fatalf("%v: %v", refs, err)
		}
		if len(got) != 1 || len(f.mutations) != 1 {
			t.Errorf("%v: changed=%d mutations=%v, want exactly one", refs, len(got), f.mutations)
		}
		// reset for next case
		f.tasks[l][0].Status = domain.StatusNeedsAction
	}
}

func TestSetStatusUnresolvableRefMutatesNothing(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", false)
	f.seed(l, "B", "", false)
	for _, refs := range [][]string{{"1", "99"}, {"1", "nope"}, {"2", ""}, {"1", "0"}} {
		got, err := seen(t, f, "").SetStatus(context.Background(), "", refs, domain.StatusCompleted)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%v: err = %v, want ErrNotFound", refs, err)
		}
		if len(got) != 0 {
			t.Errorf("%v: changed = %v", refs, got)
		}
		if len(f.mutations) != 0 {
			t.Errorf("%v: store mutated: %v", refs, f.mutations)
		}
	}
}

func TestSetStatusNoRefsIsAnError(t *testing.T) {
	f := newFake("Inbox")
	if _, err := seen(t, f, "").SetStatus(context.Background(), "", nil, domain.StatusCompleted); err == nil {
		t.Error("expected error for no refs")
	}
}

func TestMutationFailureReturnsAlreadyChanged(t *testing.T) {
	for name, run := range map[string]func(s Service) ([]domain.Task, error){
		"setStatus": func(s Service) ([]domain.Task, error) {
			return s.SetStatus(context.Background(), "", []string{"1", "2", "3"}, domain.StatusCompleted)
		},
		"delete": func(s Service) ([]domain.Task, error) {
			return s.Delete(context.Background(), "", []string{"1", "2", "3"}, approveAll)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake("Inbox")
			l := f.lists[0].ID
			a := f.seed(l, "A", "", false)
			f.seed(l, "B", "", false)
			f.seed(l, "C", "", false)
			f.failAt = 2
			got, err := run(seen(t, f, ""))
			if !errors.Is(err, errBoom) {
				t.Fatalf("err = %v, want boom", err)
			}
			if len(got) != 1 || got[0].ID != a.ID {
				t.Fatalf("changed = %+v, want only A", got)
			}
			if len(f.mutations) != 2 {
				t.Errorf("mutations = %v; processing must stop at the failure", f.mutations)
			}
		})
	}
}

func TestDeleteRemovesTasks(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", false)
	b := f.seed(l, "B", "", false)
	got, err := seen(t, f, "").Delete(context.Background(), "", []string{"A"}, approveAll)
	if err != nil || len(got) != 1 || got[0].Title != "A" {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(f.tasks[l]) != 1 || f.tasks[l][0].ID != b.ID {
		t.Errorf("remaining = %+v", f.tasks[l])
	}
}

func TestAddTrimsTitleAndStoresNotes(t *testing.T) {
	f := newFake("Inbox")
	got, err := seen(t, f, "").Add(context.Background(), AddInput{Title: "  Buy milk \n", Notes: "2%"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Buy milk" || got.Notes != "2%" || got.Due != nil {
		t.Errorf("got %+v", got)
	}
	if len(f.tasks[f.lists[0].ID]) != 1 {
		t.Error("task not stored in default list")
	}
}

func TestAddRejectsEmptyTitleWithoutCallingStore(t *testing.T) {
	for _, title := range []string{"", "   ", "\t\n"} {
		f := newFake("Inbox")
		if _, err := seen(t, f, "").Add(context.Background(), AddInput{Title: title}); err == nil {
			t.Errorf("title %q accepted", title)
		}
		if len(f.mutations) != 0 {
			t.Errorf("title %q: store mutated: %v", title, f.mutations)
		}
	}
}

func TestAddParsesDueRelativeToToday(t *testing.T) {
	f := newFake("Inbox")
	got, err := seen(t, f, "").Add(context.Background(), AddInput{Title: "x", Due: "+3d"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Date{Year: 2026, Month: time.October, Day: 18}
	if got.Due == nil || *got.Due != want {
		t.Errorf("due = %v, want %v", got.Due, want)
	}
}

func TestAddInvalidDueRejectedWithoutCallingStore(t *testing.T) {
	f := newFake("Inbox")
	if _, err := seen(t, f, "").Add(context.Background(), AddInput{Title: "x", Due: "whenever"}); err == nil {
		t.Fatal("expected error")
	}
	if len(f.mutations) != 0 {
		t.Errorf("store mutated: %v", f.mutations)
	}
}

func TestAddResolvesParentAndListByTitle(t *testing.T) {
	f := newFake("Inbox", "Work")
	work := f.lists[1].ID
	f.seed(work, "Project", "", false)
	parent := f.seed(work, "Other", "", false)

	got, err := svcOf(f).Add(context.Background(), AddInput{ListRef: "work", ParentRef: "Other", Title: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Parent != parent.ID {
		t.Errorf("parent = %q, want %q", got.Parent, parent.ID)
	}
	if len(f.tasks[work]) != 3 || len(f.tasks[f.lists[0].ID]) != 0 {
		t.Error("task must land in the Work list only")
	}

	// positional parent ref
	got, err = seen(t, f, "Work").Add(context.Background(), AddInput{ListRef: "Work", ParentRef: "1", Title: "sub2"})
	if err != nil || got.Parent == "" {
		t.Fatalf("positional parent: %+v, %v", got, err)
	}
}

func TestAddBadParentOrListMutatesNothing(t *testing.T) {
	f := newFake("Inbox")
	f.seed(f.lists[0].ID, "A", "", false)
	if _, err := seen(t, f, "").Add(context.Background(), AddInput{ParentRef: "ghost", Title: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bad parent err = %v", err)
	}
	if _, err := seen(t, f, "").Add(context.Background(), AddInput{ListRef: "ghost", Title: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bad list err = %v", err)
	}
	if len(f.mutations) != 0 {
		t.Errorf("store mutated: %v", f.mutations)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEditEmptyPatchRejected(t *testing.T) {
	f := newFake("Inbox")
	f.seed(f.lists[0].ID, "A", "", false)
	if _, err := seen(t, f, "").Edit(context.Background(), EditInput{Ref: "1"}); err == nil {
		t.Error("expected error")
	}
	if len(f.mutations) != 0 {
		t.Errorf("store mutated: %v", f.mutations)
	}
}

func TestEditDueEmptyClearsDueDate(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", false)
	due := domain.Date{Year: 2026, Month: 11, Day: 1}
	f.tasks[l][0].Due = &due

	got, err := seen(t, f, "").Edit(context.Background(), EditInput{Ref: "1", Due: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Due != nil || f.tasks[l][0].Due != nil {
		t.Errorf("due not cleared: %v", got.Due)
	}
	if got.ID != a.ID {
		t.Error("wrong task edited")
	}
}

func TestEditSetsFields(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", false)
	got, err := seen(t, f, "").Edit(context.Background(), EditInput{Ref: "A", Title: ptr("  New "), Notes: ptr("n"), Due: ptr("tomorrow")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New" || got.Notes != "n" || got.Due == nil || got.Due.Day != 16 {
		t.Errorf("got %+v", got)
	}
}

func TestEditNotesEmptyClearsNotes(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", false)
	f.tasks[l][0].Notes = "old"
	got, err := seen(t, f, "").Edit(context.Background(), EditInput{Ref: "1", Notes: ptr("")})
	if err != nil || got.Notes != "" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestEditRejections(t *testing.T) {
	cases := map[string]EditInput{
		"blank title":  {Ref: "1", Title: ptr("   ")},
		"empty title":  {Ref: "1", Title: ptr("")},
		"invalid due":  {Ref: "1", Due: ptr("nonsense")},
		"unknown ref":  {Ref: "ghost", Title: ptr("x")},
		"out of range": {Ref: "42", Title: ptr("x")},
		"empty ref":    {Ref: "", Title: ptr("x")},
		"unknown list": {Ref: "1", ListRef: "ghost", Title: ptr("x")},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake("Inbox")
			f.seed(f.lists[0].ID, "A", "", false)
			if _, err := seen(t, f, "").Edit(context.Background(), in); err == nil {
				t.Error("expected error")
			}
			if len(f.mutations) != 0 {
				t.Errorf("store mutated: %v", f.mutations)
			}
		})
	}
	f := newFake("Inbox")
	if _, err := seen(t, f, "").Edit(context.Background(), EditInput{Ref: "ghost", Title: ptr("x")}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown ref should wrap ErrNotFound: %v", err)
	}
}

func TestResolveListDefault(t *testing.T) {
	f := newFake("Inbox", "Work")
	for _, ref := range []string{"", DefaultList} {
		l, err := seen(t, f, "").ResolveList(context.Background(), ref)
		if err != nil || l.Title != "Inbox" {
			t.Errorf("ResolveList(%q) = %+v, %v", ref, l, err)
		}
	}
	l, err := seen(t, f, "").ResolveList(context.Background(), "wor")
	if err != nil || l.Title != "Work" {
		t.Errorf("prefix: %+v, %v", l, err)
	}
}

func TestCreateAndRenameListTrimAndRejectEmpty(t *testing.T) {
	f := newFake("Inbox")
	for _, title := range []string{"", "  ", "\n"} {
		if _, err := seen(t, f, "").CreateList(context.Background(), title); err == nil {
			t.Errorf("CreateList(%q) accepted", title)
		}
		if _, err := seen(t, f, "").RenameList(context.Background(), "Inbox", title); err == nil {
			t.Errorf("RenameList(%q) accepted", title)
		}
	}
	if len(f.mutations) != 0 {
		t.Errorf("store mutated: %v", f.mutations)
	}
	l, err := seen(t, f, "").CreateList(context.Background(), "  Home ")
	if err != nil || l.Title != "Home" {
		t.Errorf("CreateList = %+v, %v", l, err)
	}
	l, err = seen(t, f, "").RenameList(context.Background(), "Home", " Casa ")
	if err != nil || l.Title != "Casa" {
		t.Errorf("RenameList = %+v, %v", l, err)
	}
	if _, err := seen(t, f, "").RenameList(context.Background(), "ghost", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rename unknown list: %v", err)
	}
}

func TestSnapshotOrdersAndNumbers(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	p := f.seed(l, "P", "", false)
	f.seed(l, "Done", "", true)
	f.seed(l, "Child", p.ID, false)
	snap, err := seen(t, f, "").Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, r := range snap.Rows {
		titles = append(titles, fmt.Sprintf("%s:%d", r.Task.Title, r.Index))
	}
	if got := strings.Join(titles, ","); got != "P:1,Child:2,Done:0" {
		t.Errorf("rows = %s", got)
	}
}

func TestClearCompletedAndShow(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", true)
	b := f.seed(l, "B", "", false)
	list, err := seen(t, f, "").ClearCompleted(context.Background(), "")
	if err != nil || list.Title != "Inbox" {
		t.Fatalf("%v %v", list, err)
	}
	got, err := seen(t, f, "").Show(context.Background(), "", "1")
	if err != nil || got.ID != b.ID {
		t.Errorf("Show = %+v, %v", got, err)
	}
	if _, err := seen(t, f, "").Show(context.Background(), "", "A"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cleared task still visible: %v", err)
	}
}

func TestNumbersStayStableWhenTaskAddedAtTop(t *testing.T) {
	for _, op := range []string{"rm", "done"} {
		t.Run(op, func(t *testing.T) {
			f := newFake("Inbox")
			l := f.lists[0].ID
			f.seed(l, "Milk", "", false)
			rent := f.seed(l, "Rent", "", false)
			s := svcOf(f)
			ctx := context.Background()
			if _, err := s.Listing(ctx, "", false, true); err != nil { // user runs `ls`: #1 Milk, #2 Rent
				t.Fatal(err)
			}
			// Meanwhile a task appears at the top of the list.
			if _, err := s.Add(ctx, AddInput{Title: "Newest"}); err != nil {
				t.Fatal(err)
			}
			snap, _ := s.Snapshot(ctx, "")
			if snap.Rows[0].Task.Title != "Newest" {
				t.Fatalf("fixture: new task should be first, got %s", snap.Rows[0].Task.Title)
			}
			var err error
			if op == "rm" {
				_, err = s.Delete(ctx, "", []string{"2"}, approveAll)
			} else {
				_, err = s.SetStatus(ctx, "", []string{"2"}, domain.StatusCompleted)
			}
			if err != nil {
				t.Fatal(err)
			}
			if op == "rm" {
				if _, ok := f.find(l, rent.ID); ok {
					t.Error("Rent should have been deleted")
				}
				if len(f.tasks[l]) != 2 {
					t.Errorf("other tasks affected: %+v", f.tasks[l])
				}
			} else {
				if statusOf(f, l, rent.ID) != domain.StatusCompleted {
					t.Error("Rent should be completed")
				}
				for _, tk := range f.tasks[l] {
					if tk.ID != rent.ID && tk.IsDone() {
						t.Errorf("%s wrongly completed", tk.Title)
					}
				}
			}
		})
	}
}

func TestNumericRefsWithoutPriorListingError(t *testing.T) {
	f := newFake("Inbox")
	f.seed(f.lists[0].ID, "A", "", false)
	s := svcOf(f)
	ctx := context.Background()
	if _, err := s.SetStatus(ctx, "", []string{"1"}, domain.StatusCompleted); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("done: %v", err)
	}
	if _, err := s.Delete(ctx, "", []string{"1"}, approveAll); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rm: %v", err)
	}
	if _, err := s.Show(ctx, "", "1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("show: %v", err)
	}
	if len(f.mutations) != 0 {
		t.Errorf("mutated: %v", f.mutations)
	}
	// Non-numeric refs still work.
	if _, err := s.Show(ctx, "", "A"); err != nil {
		t.Errorf("title ref: %v", err)
	}
}

func TestNumberedTaskGoneReportsNoLongerExists(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", false)
	s := seen(t, f, "")
	f.tasks[l] = nil
	_ = a
	_, err := s.Show(context.Background(), "", "1")
	if !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v", err)
	}
}

func TestListingSavesNumberingOfAllPendingRegardlessOfIncludeDone(t *testing.T) {
	for _, includeDone := range []bool{false, true} {
		f := newFake("Inbox")
		l := f.lists[0].ID
		a := f.seed(l, "A", "", false)
		f.seed(l, "Done", "", true)
		b := f.seed(l, "B", "", false)
		s := svcOf(f)
		snap, err := s.Listing(context.Background(), "", includeDone, true)
		if err != nil {
			t.Fatal(err)
		}
		saved, _ := s.Numbers.Load(l)
		if len(saved) != 2 || saved[0] != a.ID || saved[1] != b.ID {
			t.Errorf("includeDone=%v saved = %v", includeDone, saved)
		}
		if got := len(snap.Rows); (got == 3) != includeDone {
			t.Errorf("includeDone=%v: %d rows returned", includeDone, got)
		}
	}
}

func TestListingWithoutRememberLeavesNumberingAlone(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	a := f.seed(l, "A", "", false)
	s := seen(t, f, "")
	f.seed(l, "B", "", false)
	snap, err := s.Listing(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Rows {
		if r.Index != 0 {
			t.Errorf("%s has index %d", r.Task.Title, r.Index)
		}
	}
	if len(snap.Rows) != 2 {
		t.Errorf("rows = %d", len(snap.Rows))
	}
	if saved, _ := s.Numbers.Load(l); len(saved) != 1 || saved[0] != a.ID {
		t.Errorf("saved numbering changed: %v", saved)
	}
}

func TestListingSaveFailureReturnsUnnumberedSnapshotAndForgetsOldNumbers(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "A", "", false)
	f.seed(l, "B", "", false)
	s := seen(t, f, "") // old numbering: #1 A, #2 B
	nums := s.Numbers.(*memNumbers)
	nums.saveErr = errors.New("disk full")

	snap, err := s.Listing(context.Background(), "", false, true)
	if !errors.Is(err, ErrNumbersNotSaved) {
		t.Fatalf("err = %v", err)
	}
	if len(snap.Rows) != 2 || snap.List.Title != "Inbox" {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, r := range snap.Rows {
		if r.Index != 0 {
			t.Errorf("%s still numbered %d", r.Task.Title, r.Index)
		}
	}
	// The stale numbering must not be usable any more.
	if _, err := s.Show(context.Background(), "", "1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("numeric ref resolved against stale numbering: %v", err)
	}
	if _, err := s.Show(context.Background(), "", "A"); err != nil {
		t.Errorf("title ref should still work: %v", err)
	}
}

func TestSetStatusPrefersByTitleDirection(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	oldDone := f.seed(l, "Dup", "", true)
	pend := f.seed(l, "Dup", "", false)

	got, err := svcOf(f).SetStatus(context.Background(), "", []string{"dup"}, domain.StatusCompleted)
	if err != nil || len(got) != 1 || got[0].ID != pend.ID {
		t.Fatalf("done picked %+v, %v; want the pending Dup", got, err)
	}
	// Now both are completed; undo of a title with one completed and one pending prefers the completed.
	f.tasks[l][1].Status = domain.StatusNeedsAction
	got, err = svcOf(f).SetStatus(context.Background(), "", []string{"dup"}, domain.StatusNeedsAction)
	if err != nil || len(got) != 1 || got[0].ID != oldDone.ID {
		t.Fatalf("undo picked %+v, %v; want the completed Dup", got, err)
	}
}

func TestSetStatusAlreadyInTargetStatusErrorsWithoutMutating(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	f.seed(l, "Pending", "", false)
	done := f.seed(l, "Done", "", true)
	s := seen(t, f, "")
	if _, err := s.SetStatus(context.Background(), "", []string{"Pending", "Done"}, domain.StatusCompleted); err == nil {
		t.Error("done on an already-completed task should error")
	}
	if _, err := s.SetStatus(context.Background(), "", []string{done.ID, "Pending"}, domain.StatusNeedsAction); err == nil {
		t.Error("undo on an already-pending task should error")
	}
	if len(f.mutations) != 0 {
		t.Errorf("mutated: %v", f.mutations)
	}
}

func TestDeleteApprovalRejectionDeletesNothing(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	p := f.seed(l, "P", "", false)
	f.seed(l, "C", p.ID, false)
	s := seen(t, f, "")
	veto := errors.New("vetoed")
	got, err := s.Delete(context.Background(), "", []string{"P"}, func([]DeletePlan) error { return veto })
	if !errors.Is(err, veto) || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
	if len(f.mutations) != 0 || len(f.tasks[l]) != 2 {
		t.Errorf("mutated: %v", f.mutations)
	}
}

func TestDeletePlansDescribeSubtasksAndRisk(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	p := f.seed(l, "P", "", false)
	c := f.seed(l, "C", p.ID, false)
	f.seed(l, "G", c.ID, true)
	f.seed(l, "Plain", "", false)
	asg := f.seed(l, "Assigned", "", false)
	f.tasks[l][4].Assigned = true
	s := seen(t, f, "")

	var plans []DeletePlan
	_, err := s.Delete(context.Background(), "", []string{"P", "Plain", asg.ID}, func(ps []DeletePlan) error {
		plans = ps
		return errors.New("stop")
	})
	if err == nil || len(plans) != 3 {
		t.Fatalf("plans = %+v, err = %v", plans, err)
	}
	byTitle := map[string]DeletePlan{}
	for _, p := range plans {
		byTitle[p.Task.Title] = p
	}
	if n := len(byTitle["P"].Subtasks); n != 2 || !byTitle["P"].Risky() {
		t.Errorf("P: %d subtasks, risky=%v", n, byTitle["P"].Risky())
	}
	if byTitle["Plain"].Risky() {
		t.Error("plain task should not be risky")
	}
	if !byTitle["Assigned"].Risky() {
		t.Error("assigned task should be risky")
	}
}

func TestDeleteDeletesNestedTargetsDeepestFirst(t *testing.T) {
	f := newFake("Inbox")
	l := f.lists[0].ID
	p := f.seed(l, "P", "", false)
	c := f.seed(l, "C", p.ID, false)
	g := f.seed(l, "G", c.ID, false)
	f.seed(l, "Other", "", false)
	s := seen(t, f, "")
	got, err := s.Delete(context.Background(), "", []string{p.ID, c.ID, g.ID}, approveAll)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"delete:" + g.ID, "delete:" + c.ID, "delete:" + p.ID}
	if !reflect.DeepEqual(f.mutations, want) {
		t.Errorf("mutations = %v, want %v", f.mutations, want)
	}
	if len(got) != 3 || got[0].ID != g.ID || got[2].ID != p.ID {
		t.Errorf("reported = %+v; want all three, deepest first", got)
	}
}

func TestRenameAndPlanListDeletionRequireExactName(t *testing.T) {
	f := newFake("Inbox", "Work")
	s := svcOf(f)
	ctx := context.Background()
	for _, ref := range []string{"", "  ", "W", "Wor", "wo"} {
		if _, err := s.RenameList(ctx, ref, "X"); err == nil {
			t.Errorf("RenameList(%q) accepted", ref)
		}
		if _, err := s.PlanListDeletion(ctx, ref); err == nil {
			t.Errorf("PlanListDeletion(%q) accepted", ref)
		}
	}
	if len(f.mutations) != 0 {
		t.Errorf("mutated: %v", f.mutations)
	}
	if _, err := s.PlanListDeletion(ctx, "W"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("prefix: %v", err)
	}
	if l, err := s.RenameList(ctx, "work", "Jobs"); err != nil || l.Title != "Jobs" {
		t.Errorf("exact (case-insensitive) rename: %+v, %v", l, err)
	}
}

func TestPlanListDeletionCountsTasks(t *testing.T) {
	f := newFake("Inbox", "Work")
	w := f.lists[1].ID
	f.seed(w, "a", "", false)
	f.seed(w, "b", "", true)
	f.seed(w, "c", "", false)
	f.tasks[w][2].Assigned = true
	plan, err := svcOf(f).PlanListDeletion(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	if plan.List.ID != w || plan.Tasks != 3 || plan.Assigned != 1 {
		t.Errorf("plan = %+v", plan)
	}
	if len(f.mutations) != 0 {
		t.Error("planning must not mutate")
	}
	if err := svcOf(f).DeleteList(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(f.lists) != 1 || f.lists[0].Title != "Inbox" {
		t.Errorf("lists = %+v", f.lists)
	}
}
