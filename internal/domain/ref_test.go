package domain

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func rowsOf(ts ...Task) []Row { return Arrange(ts) }

func titled(id, title string, done bool) Task {
	t := tk(id, "", id, done)
	t.Title = title
	return t
}

func resolve(ref string, ts ...Task) (Task, error) {
	rows := rowsOf(ts...)
	return ResolveTask(ref, TasksOf(rows), ResolveOpts{Numbering: Numbering(rows)})
}

func TestResolveTaskByNumber(t *testing.T) {
	ts := []Task{titled("aaaa1", "one", false), titled("bbbb2", "two", true), titled("cccc3", "three", false)}
	got, err := resolve("2", ts...)
	if err != nil || got.ID != "cccc3" {
		t.Fatalf("ResolveTask(2) = %v, %v; want cccc3 (completed tasks are not numbered)", got.ID, err)
	}
	if got, err := resolve(" 1 ", ts...); err != nil || got.ID != "aaaa1" {
		t.Errorf("padded number: %v, %v", got.ID, err)
	}
}

func TestResolveTaskNumberUsesSavedNumberingNotLiveOrder(t *testing.T) {
	milk := titled("milk1", "Milk", false)
	rent := titled("rent1", "Rent", false)
	saved := []string{milk.ID, rent.ID}
	// A new task now sorts first in the live list.
	live := []Task{titled("new01", "New", false), milk, rent}
	live[0].Position = "0"
	milk.Position, rent.Position = "1", "2"
	live[1], live[2] = milk, rent
	got, err := ResolveTask("2", live, ResolveOpts{Numbering: saved})
	if err != nil || got.ID != rent.ID {
		t.Errorf("#2 = %v, %v; want Rent", got.ID, err)
	}
}

func TestResolveTaskStaleNumber(t *testing.T) {
	live := []Task{titled("aaaa1", "one", false)}
	_, err := ResolveTask("2", live, ResolveOpts{Numbering: []string{"aaaa1", "gone"}})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("err = %v, want ErrNotFound mentioning \"no longer exists\"", err)
	}
}

func TestResolveTaskNumberWithoutNumbering(t *testing.T) {
	live := []Task{titled("aaaa1", "one", false)}
	for _, opts := range []ResolveOpts{{}, {Numbering: []string{}}} {
		if _, err := ResolveTask("1", live, opts); !errors.Is(err, ErrNotFound) {
			t.Errorf("opts %+v: err = %v, want ErrNotFound", opts, err)
		}
	}
}

func TestResolveTaskBadNumbers(t *testing.T) {
	ts := []Task{titled("aaaa1", "one", false), titled("bbbb2", "two", true)}
	for _, ref := range []string{"0", "-1", "2", "3", "99999999999999999999", "+1x"} {
		_, err := resolve(ref, ts...)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolveTask(%q) err = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestResolveTaskNumericTitleIsReachable(t *testing.T) {
	ts := []Task{titled("aaaa1", "one", false), titled("bbbb2", "42", false)}
	got, err := resolve("42", ts...)
	if err != nil || got.ID != "bbbb2" {
		t.Errorf("got %v, %v; want the task titled 42", got.ID, err)
	}
	// Inside the numbering, the number still wins over a title.
	ts = []Task{titled("aaaa1", "2", false), titled("bbbb2", "other", false)}
	got, err = resolve("2", ts...)
	if err != nil || got.ID != "bbbb2" {
		t.Errorf("in-range number must mean the numbered task: %v, %v", got.ID, err)
	}
}

func TestResolveTaskEmptyRef(t *testing.T) {
	ts := []Task{titled("aaaa1", "one", false)}
	for _, ref := range []string{"", "   ", "\t\n"} {
		if _, err := resolve(ref, ts...); !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolveTask(%q) err = %v, want ErrNotFound", ref, err)
		}
	}
	if _, err := ResolveTask("x", nil, ResolveOpts{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no tasks: err = %v", err)
	}
}

func TestResolveTaskByID(t *testing.T) {
	got, err := resolve("abcd5678", titled("abcd1234", "one", false), titled("abcd5678", "two", true))
	if err != nil || got.Title != "two" {
		t.Errorf("exact ID: %v, %v", got.Title, err)
	}
	// Exact ID wins even when it is also a prefix of another ID.
	if got, err := resolve("abcd", titled("abcd", "short", false), titled("abcdef", "long", false)); err != nil || got.ID != "abcd" {
		t.Errorf("exact-ID-vs-prefix: %v, %v", got.ID, err)
	}
}

func TestResolveTaskIDPrefix(t *testing.T) {
	a, b := titled("abcd1234", "one", false), titled("wxyz5678", "two", true)
	if got, err := resolve("wxyz", a, b); err != nil || got.ID != "wxyz5678" {
		t.Errorf("4-char prefix: %v, %v", got.ID, err)
	}
	if got, err := resolve("abcd12", a, b); err != nil || got.ID != "abcd1234" {
		t.Errorf("6-char prefix: %v, %v", got.ID, err)
	}
	if _, err := resolve("wxy", a, b); !errors.Is(err, ErrNotFound) {
		t.Errorf("3-char prefix err = %v, want ErrNotFound", err)
	}
}

func TestResolveTaskAmbiguousPrefix(t *testing.T) {
	_, err := resolve("abcd", titled("abcd1111", "one", false), titled("abcd2222", "two", false))
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "abcd1111") || !strings.Contains(msg, "abcd2222") {
		t.Errorf("error should list candidates: %q", msg)
	}
}

func TestResolveTaskByTitle(t *testing.T) {
	a, b := titled("id1", "Buy Milk", false), titled("id2", "Walk dog", true)
	if got, err := resolve("buy milk", a, b); err != nil || got.ID != "id1" {
		t.Errorf("case-insensitive title: %v, %v", got.ID, err)
	}
	if got, err := resolve("WALK DOG", a, b); err != nil || got.ID != "id2" {
		t.Errorf("completed task by title: %v, %v", got.ID, err)
	}
	for _, ref := range []string{"buy", "milk", "Buy Milk!"} {
		if _, err := resolve(ref, a, b); !errors.Is(err, ErrNotFound) {
			t.Errorf("partial title %q: err = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestResolveTaskDuplicateTitles(t *testing.T) {
	got, err := resolve("same", titled("id1", "Same", true), titled("id2", "Same", false), titled("id3", "Same", true))
	if err != nil || got.ID != "id2" {
		t.Errorf("single pending wins by default: %v, %v", got.ID, err)
	}
	if _, err := resolve("same", titled("id1", "Same", false), titled("id2", "Same", false)); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("two pending: err = %v, want ErrAmbiguous", err)
	}
	if _, err := resolve("same", titled("id1", "Same", true), titled("id2", "Same", true)); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("two completed: err = %v, want ErrAmbiguous", err)
	}
}

func TestResolveTaskPreferCompleted(t *testing.T) {
	ts := []Task{titled("id1", "Same", true), titled("id2", "Same", false), titled("id3", "Same", false)}
	got, err := ResolveTask("same", ts, ResolveOpts{Prefer: StatusCompleted})
	if err != nil || got.ID != "id1" {
		t.Errorf("Prefer completed: %v, %v", got.ID, err)
	}
	// Prefer does not override a unique match.
	got, err = ResolveTask("other", append(ts, titled("id4", "Other", false)), ResolveOpts{Prefer: StatusCompleted})
	if err != nil || got.ID != "id4" {
		t.Errorf("unique match: %v, %v", got.ID, err)
	}
}

func TestResolveTaskIDBeatsTitle(t *testing.T) {
	got, err := resolve("milk", titled("id1", "milk", false), titled("milk", "other", false))
	if err != nil || got.ID != "milk" {
		t.Errorf("got %v, %v; ID match must take priority over title", got.ID, err)
	}
}

func TestNumberingSkipsCompletedAndKeepsDisplayOrder(t *testing.T) {
	rows := Arrange([]Task{tk("a", "", "1", false), tk("b", "", "2", true), tk("c", "a", "1", false), tk("d", "", "3", false)})
	if got, want := Numbering(rows), []string{"a", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Numbering = %v, want %v", got, want)
	}
	if got := Numbering(nil); len(got) != 0 {
		t.Errorf("Numbering(nil) = %v", got)
	}
}

func TestDescendants(t *testing.T) {
	ts := []Task{
		tk("root", "", "1", false), tk("c1", "root", "1", false), tk("c2", "root", "2", true),
		tk("g1", "c1", "1", false), tk("other", "", "2", false), tk("og", "other", "1", false),
	}
	got := ids(rowsOf(Descendants(ts, "root")...))
	sort.Strings(got)
	if want := []string{"c1", "c2", "g1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Descendants(root) = %v, want %v", got, want)
	}
	if got := Descendants(ts, "g1"); len(got) != 0 {
		t.Errorf("leaf has descendants: %v", got)
	}
	if got := Descendants(ts, "missing"); len(got) != 0 {
		t.Errorf("unknown id: %v", got)
	}
}

func TestDescendantsTerminatesOnCycles(t *testing.T) {
	ts := []Task{tk("A", "B", "1", false), tk("B", "A", "1", false), tk("S", "S", "1", false)}
	got := Descendants(ts, "A")
	if len(got) != 1 || got[0].ID != "B" {
		t.Errorf("cycle: %v", got)
	}
	if got := Descendants(ts, "S"); len(got) != 0 {
		t.Errorf("self-parent: %v", got)
	}
}

func TestResolveList(t *testing.T) {
	lists := []TaskList{
		{ID: "L1", Title: "Work"},
		{ID: "L2", Title: "Workout"},
		{ID: "L3", Title: "Home"},
		{ID: "L4", Title: "Hobby"},
	}
	tests := []struct {
		ref     string
		wantID  string
		wantErr error
	}{
		{"L3", "L3", nil},
		{"  L3  ", "L3", nil},
		{"home", "L3", nil},
		{"work", "L1", nil}, // exact title beats prefix match of "Workout"
		{"WORKOUT", "L2", nil},
		{"workou", "L2", nil},
		{"wo", "", ErrAmbiguous},
		{"ho", "", ErrAmbiguous},
		{"hom", "L3", nil},
		{"nope", "", ErrNotFound},
		{"ork", "", ErrNotFound}, // substring is not a prefix
		{"", "", ErrNotFound},
		{"   ", "", ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			got, err := ResolveList(tc.ref, lists, MatchPrefix)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.ID != tc.wantID {
				t.Errorf("got %s, want %s", got.ID, tc.wantID)
			}
		})
	}
	if _, err := ResolveList("x", nil, MatchPrefix); !errors.Is(err, ErrNotFound) {
		t.Errorf("no lists: %v", err)
	}
}

func TestResolveListIDBeatsTitle(t *testing.T) {
	lists := []TaskList{{ID: "a", Title: "b"}, {ID: "b", Title: "c"}}
	if got, err := ResolveList("b", lists, MatchPrefix); err != nil || got.ID != "b" {
		t.Errorf("got %v, %v; ID must win over title", got.ID, err)
	}
}

func TestResolveListMatchExact(t *testing.T) {
	lists := []TaskList{{ID: "L1", Title: "Work"}, {ID: "L2", Title: "Workout"}}
	tests := []struct {
		ref    string
		wantID string
		err    error
	}{
		{"Work", "L1", nil},
		{"work", "L1", nil},
		{"L2", "L2", nil},
		{"W", "", ErrNotFound},
		{"Wor", "", ErrNotFound},
		{"Workou", "", ErrNotFound},
		{"", "", ErrNotFound},
	}
	for _, tc := range tests {
		got, err := ResolveList(tc.ref, lists, MatchExact)
		if !errors.Is(err, tc.err) || (tc.err == nil && got.ID != tc.wantID) {
			t.Errorf("ResolveList(%q, exact) = %v, %v; want %q, %v", tc.ref, got.ID, err, tc.wantID, tc.err)
		}
	}
	dup := []TaskList{{ID: "a", Title: "Same"}, {ID: "b", Title: "same"}}
	if _, err := ResolveList("SAME", dup, MatchExact); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("duplicate exact titles: %v", err)
	}
}

func TestAmbiguityErrorQuotesTitles(t *testing.T) {
	_, err := resolve("evil\x1b[31m", titled("id1", "evil\x1b[31m", false), titled("id2", "evil\x1b[31m", false))
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("raw escape in error message: %q", err.Error())
	}
	if !strings.Contains(err.Error(), `\x1b`) {
		t.Errorf("escape should appear in escaped form: %q", err.Error())
	}
	_, err = ResolveList("x\x1b", []TaskList{{ID: "a", Title: "x\x1b"}, {ID: "b", Title: "x\x1b"}}, MatchPrefix)
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("raw escape in list error: %q", err.Error())
	}
}
