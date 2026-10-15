package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xnmp/gtasks-cli/internal/app"
	"github.com/xnmp/gtasks-cli/internal/domain"
	"github.com/xnmp/gtasks-cli/internal/state"
)

// memStore is a small in-memory app.Store.
type memStore struct {
	lists   []domain.TaskList
	tasks   map[string][]domain.Task
	n       int
	deleted []string // deleted list IDs
}

func newMem(titles ...string) *memStore {
	m := &memStore{tasks: map[string][]domain.Task{}}
	for _, t := range titles {
		m.n++
		m.lists = append(m.lists, domain.TaskList{ID: fmt.Sprintf("list%d", m.n), Title: t})
	}
	return m
}

func (m *memStore) Lists(context.Context) ([]domain.TaskList, error) { return m.lists, nil }
func (m *memStore) GetList(_ context.Context, id string) (domain.TaskList, error) {
	if id == app.DefaultList {
		return m.lists[0], nil
	}
	for _, l := range m.lists {
		if l.ID == id {
			return l, nil
		}
	}
	return domain.TaskList{}, domain.ErrNotFound
}
func (m *memStore) CreateList(_ context.Context, title string) (domain.TaskList, error) {
	m.n++
	l := domain.TaskList{ID: fmt.Sprintf("list%d", m.n), Title: title}
	m.lists = append(m.lists, l)
	return l, nil
}
func (m *memStore) RenameList(_ context.Context, id, title string) (domain.TaskList, error) {
	for i := range m.lists {
		if m.lists[i].ID == id {
			m.lists[i].Title = title
			return m.lists[i], nil
		}
	}
	return domain.TaskList{}, domain.ErrNotFound
}
func (m *memStore) DeleteList(_ context.Context, id string) error {
	for i := range m.lists {
		if m.lists[i].ID == id {
			m.lists = append(m.lists[:i], m.lists[i+1:]...)
			m.deleted = append(m.deleted, id)
			return nil
		}
	}
	return domain.ErrNotFound
}
func (m *memStore) Tasks(_ context.Context, id string) ([]domain.Task, error) {
	return append([]domain.Task(nil), m.tasks[id]...), nil
}
func (m *memStore) CreateTask(_ context.Context, listID, parent string, nt domain.NewTask) (domain.Task, error) {
	m.n++
	t := domain.Task{ID: fmt.Sprintf("task%04d", m.n), Title: nt.Title, Notes: nt.Notes, Due: nt.Due,
		Parent: parent, Status: domain.StatusNeedsAction, Position: fmt.Sprintf("%08d", 1_000_000-m.n)}
	m.tasks[listID] = append(m.tasks[listID], t)
	return t, nil
}
func (m *memStore) UpdateTask(_ context.Context, listID, id string, p domain.TaskPatch) (domain.Task, error) {
	for i, t := range m.tasks[listID] {
		if t.ID != id {
			continue
		}
		if p.Title != nil {
			t.Title = *p.Title
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
		}
		m.tasks[listID][i] = t
		return t, nil
	}
	return domain.Task{}, domain.ErrNotFound
}
func (m *memStore) DeleteTask(_ context.Context, listID, id string) error {
	all := m.tasks[listID]
	found := false
	gone := map[string]bool{id: true}
	for _, d := range domain.Descendants(all, id) {
		gone[d.ID] = true
	}
	var keep []domain.Task
	for _, t := range all {
		if t.ID == id {
			found = true
		}
		if !gone[t.ID] {
			keep = append(keep, t)
		}
	}
	if !found {
		return domain.ErrNotFound
	}
	m.tasks[listID] = keep
	return nil
}
func (m *memStore) ClearCompleted(context.Context, string) error { return nil }

type harness struct {
	store    *memStore
	out, err bytes.Buffer
	terminal bool

	numbersPath string
}

func newHarness(t *testing.T, lists ...string) *harness {
	t.Helper()
	t.Setenv("GTASKS_LIST", "") // flag default must not leak in from the environment
	if len(lists) == 0 {
		lists = []string{"Inbox"}
	}
	return &harness{store: newMem(lists...), numbersPath: filepath.Join(t.TempDir(), "numbers")}
}

// run executes the CLI and returns stdout and the command's error.
func (h *harness) run(args ...string) (string, error) {
	h.out.Reset()
	h.err.Reset()
	env := Env{
		In:         strings.NewReader(""),
		Out:        &h.out,
		Err:        &h.err,
		Today:      func() domain.Date { return domain.Date{Year: 2026, Month: time.October, Day: 15} },
		NewStore:   func(context.Context) (app.Store, error) { return h.store, nil },
		IsTerminal: func() bool { return h.terminal },
		Numbers:    state.Numbers{Dir: h.numbersPath},
		Version:    "test",
	}
	cmd := NewRoot(env)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return h.out.String(), err
}

func (h *harness) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := h.run(args...)
	if err != nil {
		t.Fatalf("gtasks %v: %v", args, err)
	}
	return out
}

func TestAddThenLsShowsTaskWithDueLabel(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Buy", "milk", "-d", "tomorrow")
	out := h.mustRun(t, "ls")
	for _, want := range []string{"Inbox", "Buy milk", "due tomorrow", "[ ]"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}

func TestAddReportsWhatWasAdded(t *testing.T) {
	h := newHarness(t)
	if out := h.mustRun(t, "add", "Buy milk"); !strings.Contains(out, "Added: Buy milk") {
		t.Errorf("out = %q", out)
	}
}

func TestDoneHidesFromLsButAllShowsCompleted(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Walk dog")
	h.mustRun(t, "add", "Buy milk") // new tasks appear first, so this is #1
	h.mustRun(t, "ls")
	if out := h.mustRun(t, "done", "1"); !strings.Contains(out, "Completed: Buy milk") {
		t.Errorf("done output = %q", out)
	}
	out := h.mustRun(t, "ls")
	if strings.Contains(out, "Buy milk") || !strings.Contains(out, "Walk dog") {
		t.Errorf("ls after done:\n%s", out)
	}
	out = h.mustRun(t, "ls", "-a")
	var doneLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Buy milk") {
			doneLine = l
		}
	}
	if !strings.Contains(doneLine, "[x]") {
		t.Errorf("ls -a should show Buy milk as [x]:\n%s", out)
	}
}

func TestUndoReopens(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A")
	h.mustRun(t, "ls")
	h.mustRun(t, "done", "1")
	h.mustRun(t, "undo", "A")
	if out := h.mustRun(t, "ls"); !strings.Contains(out, "A") {
		t.Errorf("task not back in ls:\n%s", out)
	}
}

func TestJSONLsIsValidAndContainsTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Buy milk")
	out := h.mustRun(t, "--json", "ls")
	var snap struct {
		List struct{ Title string } `json:"list"`
		Rows []struct {
			Task  struct{ Title string } `json:"task"`
			Index int                    `json:"index"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if snap.List.Title != "Inbox" || len(snap.Rows) != 1 || snap.Rows[0].Task.Title != "Buy milk" || snap.Rows[0].Index != 0 {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestJSONEmptyMutationIsEmptyArray(t *testing.T) {
	h := newHarness(t)
	out, err := h.run("--json", "done", "5")
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
	var arr []any
	if jerr := json.Unmarshal([]byte(out), &arr); jerr != nil || arr == nil {
		t.Errorf("stdout should be a JSON array, got %q (%v)", out, jerr)
	}
}

func TestEditDueAndNoDueMutuallyExclusive(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A", "-d", "tomorrow")
	h.mustRun(t, "ls")
	if _, err := h.run("edit", "1", "--due", "x", "--no-due"); err == nil {
		t.Fatal("expected error")
	}
	if out := h.mustRun(t, "ls"); !strings.Contains(out, "due tomorrow") {
		t.Errorf("task changed despite error:\n%s", out)
	}
}

func TestEditNoDueClearsDue(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A", "-d", "tomorrow")
	h.mustRun(t, "ls")
	h.mustRun(t, "edit", "1", "--no-due")
	if out := h.mustRun(t, "ls"); strings.Contains(out, "due") {
		t.Errorf("due still shown:\n%s", out)
	}
}

func TestEditBlankDueFlagRejected(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A", "-d", "tomorrow")
	h.mustRun(t, "ls")
	if _, err := h.run("edit", "1", "--due", ""); err == nil {
		t.Error("expected error for blank --due")
	}
	if out := h.mustRun(t, "ls"); !strings.Contains(out, "due tomorrow") {
		t.Errorf("due cleared by blank --due:\n%s", out)
	}
}

func TestEditRetitles(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Old")
	h.mustRun(t, "ls")
	h.mustRun(t, "edit", "1", "-t", "New")
	out := h.mustRun(t, "ls")
	if strings.Contains(out, "Old") || !strings.Contains(out, "New") {
		t.Errorf("ls:\n%s", out)
	}
}

func TestListsRmRefusesWithoutYesWhenNonInteractive(t *testing.T) {
	h := newHarness(t, "Inbox", "Work")
	h.terminal = false
	if _, err := h.run("lists", "rm", "Work"); err == nil {
		t.Fatal("expected refusal")
	}
	if len(h.store.deleted) != 0 || len(h.store.lists) != 2 {
		t.Errorf("list was deleted: %v", h.store.deleted)
	}
}

func TestListsRmWithYesDeletes(t *testing.T) {
	h := newHarness(t, "Inbox", "Work")
	out := h.mustRun(t, "lists", "rm", "Work", "-y")
	if !strings.Contains(out, "Deleted list: Work") {
		t.Errorf("out = %q", out)
	}
	if got := h.mustRun(t, "lists"); strings.Contains(got, "Work") {
		t.Errorf("Work still listed:\n%s", got)
	}
}

func TestListsRmInteractiveConfirmation(t *testing.T) {
	for in, wantDeleted := range map[string]bool{"y\n": true, "yes\n": true, "n\n": false, "\n": false, "": false} {
		h := newHarness(t, "Inbox", "Work")
		h.terminal = true
		h.out.Reset()
		env := Env{
			In: strings.NewReader(in), Out: &h.out, Err: &h.err,
			Today:      func() domain.Date { return domain.Date{Year: 2026, Month: 10, Day: 15} },
			NewStore:   func(context.Context) (app.Store, error) { return h.store, nil },
			IsTerminal: func() bool { return true },
			Numbers:    state.Numbers{Dir: h.numbersPath},
		}
		cmd := NewRoot(env)
		cmd.SetArgs([]string{"lists", "rm", "Work"})
		_ = cmd.Execute()
		if got := len(h.store.deleted) == 1; got != wantDeleted {
			t.Errorf("answer %q: deleted=%v, want %v", in, got, wantDeleted)
		}
	}
}

func TestListFlagTargetsNamedList(t *testing.T) {
	h := newHarness(t, "Inbox", "Work")
	h.mustRun(t, "-l", "Work", "add", "foo")
	if out := h.mustRun(t, "-l", "Work", "ls"); !strings.Contains(out, "foo") {
		t.Errorf("foo not in Work:\n%s", out)
	}
	if out := h.mustRun(t, "ls"); strings.Contains(out, "foo") {
		t.Errorf("foo leaked into default list:\n%s", out)
	}
	if len(h.store.tasks[h.store.lists[1].ID]) != 1 {
		t.Error("task not stored in Work")
	}
}

func TestUnknownListIsAnError(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run("-l", "ghost", "ls"); err == nil {
		t.Error("expected error")
	}
}

func TestRmDeletesTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "B")
	h.mustRun(t, "add", "A") // A is #1
	h.mustRun(t, "ls")
	h.mustRun(t, "rm", "1", "B")
	out := h.mustRun(t, "ls")
	if !strings.Contains(out, "(no tasks)") {
		t.Errorf("ls after rm:\n%s", out)
	}
}

func TestAddSubtaskIsIndentedUnderParent(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Parent")
	h.mustRun(t, "add", "Child", "-p", "Parent")
	var pcol, ccol int
	for _, l := range strings.Split(h.mustRun(t, "ls"), "\n") {
		if strings.Contains(l, "Parent") {
			pcol = strings.Index(l, "Parent")
		}
		if strings.Contains(l, "Child") {
			ccol = strings.Index(l, "Child")
		}
	}
	if ccol <= pcol {
		t.Errorf("child not indented: parent col %d, child col %d", pcol, ccol)
	}
}

func TestAddWithBlankTitleFails(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run("add", "   "); err == nil {
		t.Error("expected error")
	}
	if len(h.store.tasks) != 0 {
		t.Error("task created")
	}
}

func TestShowPrintsDetails(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Buy milk", "-d", "2026-12-25", "-n", "2 litres")
	h.mustRun(t, "ls")
	out := h.mustRun(t, "show", "1")
	for _, want := range []string{"Buy milk", "2026-12-25", "2 litres"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestNumbersFromLsSurviveNewTaskAddedAtTop(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Rent")
	h.mustRun(t, "add", "Milk") // inserted above Rent
	out := h.mustRun(t, "ls")   // #1 Milk, #2 Rent
	if !strings.Contains(out, "1  [ ] Milk") || !strings.Contains(out, "2  [ ] Rent") {
		t.Fatalf("fixture ls:\n%s", out)
	}
	h.mustRun(t, "add", "Newest")
	h.mustRun(t, "done", "2")
	got := h.mustRun(t, "ls", "-a")
	for _, l := range strings.Split(got, "\n") {
		switch {
		case strings.Contains(l, "Rent") && !strings.Contains(l, "[x]"):
			t.Errorf("Rent should be done:\n%s", got)
		case (strings.Contains(l, "Milk") || strings.Contains(l, "Newest")) && strings.Contains(l, "[x]"):
			t.Errorf("wrong task completed:\n%s", got)
		}
	}
}

func TestNumericRefBeforeAnyLsErrors(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A")
	if _, err := h.run("done", "1"); err == nil {
		t.Error("expected error: numbers only exist after `ls`")
	}
	if out := h.mustRun(t, "ls"); !strings.Contains(out, "A") {
		t.Errorf("task changed: %s", out)
	}
}

func TestRmTaskWithSubtasks(t *testing.T) {
	setup := func(t *testing.T) *harness {
		h := newHarness(t)
		h.mustRun(t, "add", "Parent")
		h.mustRun(t, "add", "Child", "-p", "Parent")
		return h
	}
	t.Run("non-interactive without -y refuses and deletes nothing", func(t *testing.T) {
		h := setup(t)
		if _, err := h.run("rm", "Parent"); err == nil {
			t.Fatal("expected error")
		}
		if n := len(h.store.tasks[h.store.lists[0].ID]); n != 2 {
			t.Errorf("%d tasks left, want 2", n)
		}
	})
	t.Run("with -y deletes parent and child", func(t *testing.T) {
		h := setup(t)
		h.mustRun(t, "rm", "Parent", "-y")
		if n := len(h.store.tasks[h.store.lists[0].ID]); n != 0 {
			t.Errorf("%d tasks left, want 0", n)
		}
	})
	t.Run("interactive decline deletes nothing", func(t *testing.T) {
		h := setup(t)
		env := Env{
			In: strings.NewReader("n\n"), Out: &h.out, Err: &h.err,
			Today:      func() domain.Date { return domain.Date{Year: 2026, Month: 10, Day: 15} },
			NewStore:   func(context.Context) (app.Store, error) { return h.store, nil },
			IsTerminal: func() bool { return true },
			Numbers:    state.Numbers{Dir: h.numbersPath},
		}
		cmd := NewRoot(env)
		cmd.SetArgs([]string{"rm", "Parent"})
		if err := cmd.Execute(); err == nil {
			t.Error("expected abort error")
		}
		if n := len(h.store.tasks[h.store.lists[0].ID]); n != 2 {
			t.Errorf("%d tasks left, want 2", n)
		}
	})
	t.Run("leaf task needs no confirmation", func(t *testing.T) {
		h := setup(t)
		h.mustRun(t, "rm", "Child")
		if n := len(h.store.tasks[h.store.lists[0].ID]); n != 1 {
			t.Errorf("%d tasks left, want 1", n)
		}
	})
}

func TestListsRmRequiresExactName(t *testing.T) {
	h := newHarness(t, "Inbox", "Work")
	for _, ref := range []string{"W", "Wor", ""} {
		if _, err := h.run("lists", "rm", ref, "-y"); err == nil {
			t.Errorf("lists rm %q -y succeeded", ref)
		}
	}
	if len(h.store.deleted) != 0 || len(h.store.lists) != 2 {
		t.Errorf("a list was deleted: %v", h.store.deleted)
	}
}

func TestListsRenameRequiresExactName(t *testing.T) {
	h := newHarness(t, "Inbox", "Work")
	if _, err := h.run("lists", "rename", "Wo", "Jobs"); err == nil {
		t.Error("prefix rename should fail")
	}
	h.mustRun(t, "lists", "rename", "work", "Jobs")
	if got := h.mustRun(t, "lists"); !strings.Contains(got, "Jobs") {
		t.Errorf("lists:\n%s", got)
	}
}

func TestDoneAlreadyCompletedErrors(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A")
	h.mustRun(t, "ls")
	h.mustRun(t, "done", "1")
	if _, err := h.run("done", "A"); err == nil {
		t.Error("expected error completing an already-completed task")
	}
}

func TestLsSaveFailureShowsNoNumbersAndWarns(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "A")
	h.mustRun(t, "ls") // establishes numbering #1 A
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.numbersPath = filepath.Join(blocker, "numbers") // unwritable
	out, err := h.run("ls")
	if err != nil {
		t.Fatalf("ls failed: %v", err)
	}
	if !strings.Contains(out, "A") {
		t.Errorf("listing missing:\n%s", out)
	}
	if strings.ContainsAny(out, "123456789") {
		t.Errorf("numbers shown although they could not be saved:\n%s", out)
	}
	if !strings.Contains(h.err.String(), "warning") {
		t.Errorf("stderr = %q, want a warning", h.err.String())
	}
}

func TestLsJSONDoesNotRenumber(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Rent")
	h.mustRun(t, "add", "Milk")
	if out := h.mustRun(t, "ls"); !strings.Contains(out, "2  [ ] Rent") {
		t.Fatalf("fixture:\n%s", out)
	}
	h.mustRun(t, "add", "Newest")
	out := h.mustRun(t, "--json", "ls")
	var snap struct {
		Rows []struct {
			Index int `json:"index"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Rows) != 3 {
		t.Fatalf("rows = %d", len(snap.Rows))
	}
	for _, r := range snap.Rows {
		if r.Index != 0 {
			t.Errorf("json rows must carry no index, got %d", r.Index)
		}
	}
	h.mustRun(t, "done", "2")
	for _, l := range strings.Split(h.mustRun(t, "ls", "-a"), "\n") {
		if strings.Contains(l, "Rent") && !strings.Contains(l, "[x]") {
			t.Errorf("#2 from plain ls (Rent) should be done; got line %q", l)
		}
		if (strings.Contains(l, "Milk") || strings.Contains(l, "Newest")) && strings.Contains(l, "[x]") {
			t.Errorf("wrong task completed: %q", l)
		}
	}
}

func TestRmNestedTargetsBothDeleted(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "add", "Parent")
	h.mustRun(t, "add", "Child", "-p", "Parent")
	h.mustRun(t, "add", "Other")
	out := h.mustRun(t, "rm", "Parent", "Child", "-y")
	if !strings.Contains(out, "Deleted: Child") || !strings.Contains(out, "Deleted: Parent") {
		t.Errorf("both should be reported:\n%s", out)
	}
	if strings.Index(out, "Child") > strings.Index(out, "Parent") {
		t.Errorf("child should be deleted before parent:\n%s", out)
	}
}
