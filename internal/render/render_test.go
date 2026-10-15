package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xnmp/gtasks-cli/internal/domain"
)

var today = domain.Date{Year: 2026, Month: time.October, Day: 15} // Thursday

func TestDueLabel(t *testing.T) {
	tests := []struct {
		name string
		due  domain.Date
		want string
	}{
		{"today", today, "due today"},
		{"tomorrow", today.AddDays(1), "due tomorrow"},
		{"yesterday", today.AddDays(-1), "overdue 1 day"},
		{"overdue 2 days", today.AddDays(-2), "overdue 2 days"},
		{"overdue long", today.AddDays(-400), "overdue 400 days"},
		{"in 2 days shows weekday", today.AddDays(2), "due Sat"},
		{"in 6 days shows weekday", today.AddDays(6), "due Wed"},
		{"in 7 days shows ISO date", today.AddDays(7), "due 2026-10-22"},
		{"far future", domain.Date{Year: 2030, Month: 1, Day: 2}, "due 2030-01-02"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DueLabel(tc.due, today); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func pending(title string) domain.Task {
	return domain.Task{ID: "id", Title: title, Status: domain.StatusNeedsAction}
}

func TestRowPending(t *testing.T) {
	due := today.AddDays(1)
	tk := pending("Buy milk")
	tk.Due = &due
	line := Row(domain.Row{Task: tk, Index: 3}, today)
	for _, want := range []string{"3", "[ ]", "Buy milk", "due tomorrow"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing %q", line, want)
		}
	}
}

func TestRowCompleted(t *testing.T) {
	due := today.AddDays(-5)
	tk := pending("Done thing")
	tk.Status = domain.StatusCompleted
	tk.Due = &due
	line := Row(domain.Row{Task: tk}, today)
	if !strings.Contains(line, "[x]") || !strings.Contains(line, "Done thing") {
		t.Errorf("line = %q", line)
	}
	if strings.Contains(line, "overdue") || strings.Contains(line, "due") {
		t.Errorf("completed task shows due label: %q", line)
	}
	if strings.ContainsAny(line, "0123456789") {
		t.Errorf("completed task shows an index: %q", line)
	}
}

func TestRowNotesMarker(t *testing.T) {
	tk := pending("x")
	tk.Notes = "details"
	if line := Row(domain.Row{Task: tk, Index: 1}, today); !strings.Contains(line, "+notes") {
		t.Errorf("line = %q", line)
	}
}

func TestRowCollapsesWhitespaceInTitle(t *testing.T) {
	for _, title := range []string{"line one\nline two", "line one\r\n\tline   two", "  line one \n line two\n\n"} {
		line := Row(domain.Row{Task: pending(title), Index: 1}, today)
		if strings.ContainsAny(line, "\n\r\t") {
			t.Errorf("title %q produced multi-line output %q", title, line)
		}
		if !strings.Contains(line, "line one line two") {
			t.Errorf("title %q rendered as %q", title, line)
		}
	}
}

func TestRowDepthIndents(t *testing.T) {
	col := func(depth int) int {
		line := Row(domain.Row{Task: pending("TITLE"), Index: 1, Depth: depth}, today)
		return strings.Index(line, "TITLE")
	}
	if !(col(0) < col(1) && col(1) < col(2)) {
		t.Errorf("title columns not increasing with depth: %d %d %d", col(0), col(1), col(2))
	}
	if col(2)-col(1) != col(1)-col(0) {
		t.Errorf("indent step is not uniform")
	}
}

func TestRowsEmptyAndHeading(t *testing.T) {
	var b bytes.Buffer
	Rows(&b, domain.TaskList{Title: "Inbox"}, nil, today)
	out := b.String()
	if !strings.HasPrefix(out, "Inbox\n") || !strings.Contains(out, "(no tasks)") {
		t.Errorf("out = %q", out)
	}
}

func TestListsAlignsIDsWithMultibyteTitles(t *testing.T) {
	var b bytes.Buffer
	Lists(&b, []domain.TaskList{
		{ID: "ID1", Title: "日本語リスト"},
		{ID: "ID2", Title: "Work"},
		{ID: "ID3", Title: "café ☕"},
	})
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	col := -1
	for _, l := range lines {
		idx := strings.Index(l, "ID")
		c := len([]rune(l[:idx]))
		if col == -1 {
			col = c
		} else if c != col {
			t.Errorf("IDs not aligned (rune column %d vs %d): %q", c, col, lines)
		}
	}
}

func TestListsEmpty(t *testing.T) {
	var b bytes.Buffer
	Lists(&b, nil)
	if !strings.Contains(b.String(), "no lists") {
		t.Errorf("out = %q", b.String())
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"a": 1`) {
		t.Errorf("out = %q", b.String())
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"keeps newline and tab", "a\n\tb", "a\n\tb"},
		{"strips escape and bell", "x\x1b]0;pwned\x07", "x]0;pwned"},
		{"strips C0", "a\x00b\x01c\x08d\x7fe", "abcde"},
		{"strips C1", "a\u0085b\u009bc\u009fd", "abcd"},
		{"keeps unicode", "日本語 café ☕  ", "日本語 café ☕  "},
		{"empty", "", ""},
		{"carriage return stripped", "a\rb", "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeLargeInput(t *testing.T) {
	in := strings.Repeat("a\x1b", 1_000_000)
	if got := Sanitize(in); got != strings.Repeat("a", 1_000_000) {
		t.Error("large input mis-sanitized")
	}
}

const hostile = "x\x1b]0;pwned\x07\x9b31m"

func noControl(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r == 0x1b || r == 0x07 || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s contains control char %U: %q", what, r, s)
		}
	}
}

func TestRenderersNeverEmitControlCharacters(t *testing.T) {
	tk := pending(hostile)
	tk.Notes = hostile
	tk.WebViewLink = "http://x/" + hostile
	var b bytes.Buffer
	Rows(&b, domain.TaskList{Title: hostile}, []domain.Row{{Task: tk, Index: 1}}, today)
	noControl(t, "Rows", b.String())
	if !strings.Contains(b.String(), "x]0;pwned") {
		t.Errorf("visible text should survive: %q", b.String())
	}
	b.Reset()
	Task(&b, tk, today)
	noControl(t, "Task", b.String())
	b.Reset()
	Lists(&b, []domain.TaskList{{ID: "i", Title: hostile}})
	noControl(t, "Lists", b.String())
}
