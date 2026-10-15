package domain

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func tk(id, parent, pos string, done bool) Task {
	s := StatusNeedsAction
	if done {
		s = StatusCompleted
	}
	return Task{ID: id, Title: "t-" + id, Parent: parent, Position: pos, Status: s}
}

func ids(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Task.ID
	}
	return out
}

func TestArrangeEmpty(t *testing.T) {
	for _, in := range [][]Task{nil, {}} {
		if rows := Arrange(in); len(rows) != 0 {
			t.Errorf("Arrange(%v) = %v, want empty", in, rows)
		}
	}
}

func TestArrangeOrdersParentsThenChildrenByPosition(t *testing.T) {
	tasks := []Task{
		tk("c2", "p1", "002", false),
		tk("p2", "", "20", false),
		tk("c1", "p1", "001", false),
		tk("p1", "", "10", false),
		tk("g1", "c1", "001", false),
	}
	rows := Arrange(tasks)
	if got, want := ids(rows), []string{"p1", "c1", "g1", "c2", "p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	wantDepth := []int{0, 1, 2, 1, 0}
	for i, r := range rows {
		if r.Depth != wantDepth[i] {
			t.Errorf("row %s depth = %d, want %d", r.Task.ID, r.Depth, wantDepth[i])
		}
	}
}

func TestArrangeOrphansBecomeTopLevel(t *testing.T) {
	rows := Arrange([]Task{
		tk("a", "", "1", false),
		tk("orphan", "missing", "2", false),
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Depth != 0 {
			t.Errorf("%s depth = %d, want 0", r.Task.ID, r.Depth)
		}
	}
}

func TestArrangeMalformedParentsKeepEveryTaskOnce(t *testing.T) {
	cases := map[string][]Task{
		"two-cycle":   {tk("A", "B", "1", false), tk("B", "A", "2", false)},
		"self-parent": {tk("A", "A", "1", false)},
		"cycle with tail and normal": {
			tk("A", "B", "1", false), tk("B", "A", "2", false),
			tk("C", "A", "3", true), tk("N", "", "4", false),
		},
		"three-cycle": {tk("A", "C", "1", false), tk("B", "A", "1", false), tk("C", "B", "1", false)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			rows := Arrange(in)
			got := ids(rows)
			sort.Strings(got)
			var want []string
			for _, x := range in {
				want = append(want, x.ID)
			}
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("tasks in output = %v, want each of %v exactly once", got, want)
			}
		})
	}
}

func TestArrangeNumbersPendingInDisplayOrder(t *testing.T) {
	rows := Arrange([]Task{
		tk("p1", "", "1", false),
		tk("c1", "p1", "1", true),
		tk("c2", "p1", "2", false),
		tk("p2", "", "2", true),
		tk("p3", "", "3", false),
	})
	want := map[string]int{"p1": 1, "c1": 0, "c2": 2, "p2": 0, "p3": 3}
	for _, r := range rows {
		if r.Index != want[r.Task.ID] {
			t.Errorf("%s index = %d, want %d", r.Task.ID, r.Index, want[r.Task.ID])
		}
	}
}

func TestArrangeDoesNotMutateInput(t *testing.T) {
	in := []Task{
		tk("b", "", "2", false),
		tk("a", "", "1", false),
		tk("c", "a", "1", true),
	}
	snapshot := append([]Task(nil), in...)
	Arrange(in)
	if !reflect.DeepEqual(in, snapshot) {
		t.Errorf("input mutated: %v, was %v", in, snapshot)
	}
}

func TestArrangeEqualPositionsAreDeterministic(t *testing.T) {
	a := Arrange([]Task{tk("y", "", "1", false), tk("x", "", "1", false)})
	b := Arrange([]Task{tk("x", "", "1", false), tk("y", "", "1", false)})
	if !reflect.DeepEqual(ids(a), ids(b)) {
		t.Errorf("order depends on input order: %v vs %v", ids(a), ids(b))
	}
}

func TestPendingView(t *testing.T) {
	rows := Arrange([]Task{
		tk("doneParent", "", "1", true),
		tk("pendingChild", "doneParent", "1", false),
		tk("doneChild", "doneParent", "2", true),
		tk("doneAlone", "", "2", true),
		tk("doneWithDoneKids", "", "3", true),
		tk("doneKid", "doneWithDoneKids", "1", true),
		tk("pending", "", "4", false),
	})
	view := PendingView(rows)
	if got, want := ids(view), []string{"doneParent", "pendingChild", "pending"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("view = %v, want %v", got, want)
	}
	// Numbering is unchanged vs Arrange.
	byID := map[string]Row{}
	for _, r := range rows {
		byID[r.Task.ID] = r
	}
	for _, r := range view {
		if r != byID[r.Task.ID] {
			t.Errorf("row %s changed in view: %+v vs %+v", r.Task.ID, r, byID[r.Task.ID])
		}
	}
	if byID["pendingChild"].Index != 1 || byID["pending"].Index != 2 {
		t.Errorf("unexpected indices: %+v", byID)
	}
}

func TestPendingViewEmptyAndAllDone(t *testing.T) {
	if got := PendingView(nil); len(got) != 0 {
		t.Errorf("PendingView(nil) = %v", got)
	}
	if got := PendingView(Arrange([]Task{tk("a", "", "1", true)})); len(got) != 0 {
		t.Errorf("all-done view = %v", got)
	}
}

func TestArrangeLargeFlat(t *testing.T) {
	const n = 10000
	tasks := make([]Task, n)
	for i := range tasks {
		tasks[i] = tk(fmt.Sprintf("t%05d", i), "", fmt.Sprintf("%08d", n-i), i%3 == 0)
	}
	rows := Arrange(tasks)
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	pending := 0
	for _, r := range rows {
		if !r.Task.IsDone() {
			pending++
			if r.Index != pending {
				t.Fatalf("pending index %d at pending position %d", r.Index, pending)
			}
		} else if r.Index != 0 {
			t.Fatalf("completed task has index %d", r.Index)
		}
	}
	if len(PendingView(rows)) != pending {
		t.Errorf("PendingView kept wrong number of rows")
	}
}

func TestArrangeDeepChain(t *testing.T) {
	const n = 5000
	tasks := make([]Task, n)
	for i := range tasks {
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("t%05d", i-1)
		}
		tasks[i] = tk(fmt.Sprintf("t%05d", i), parent, "1", false)
	}
	rows := Arrange(tasks)
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	for i, r := range rows {
		if r.Depth != i || r.Index != i+1 {
			t.Fatalf("row %d: depth=%d index=%d", i, r.Depth, r.Index)
		}
	}
	if len(PendingView(rows)) != n {
		t.Errorf("PendingView dropped rows of a pending chain")
	}
}
