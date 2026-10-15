package domain

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) Date { return Date{y, m, day} }

func TestParseDue(t *testing.T) {
	today := d(2026, 10, 15) // Thursday
	if today.Weekday() != time.Thursday {
		t.Fatalf("fixture: expected Thursday, got %v", today.Weekday())
	}
	tests := []struct {
		name  string
		in    string
		today Date
		want  Date
	}{
		{"today", "today", today, d(2026, 10, 15)},
		{"tomorrow", "tomorrow", today, d(2026, 10, 16)},
		{"tom", "tom", today, d(2026, 10, 16)},
		{"tmr", "tmr", today, d(2026, 10, 16)},
		{"yesterday", "yesterday", today, d(2026, 10, 14)},
		{"upper case", "TOMORROW", today, d(2026, 10, 16)},
		{"mixed case", "ToDaY", today, d(2026, 10, 15)},
		{"whitespace", "  tomorrow \t", today, d(2026, 10, 16)},
		{"plus days", "+3d", today, d(2026, 10, 18)},
		{"bare days", "3d", today, d(2026, 10, 18)},
		{"upper days", "+3D", today, d(2026, 10, 18)},
		{"zero days", "+0d", today, d(2026, 10, 15)},
		{"plus weeks", "+2w", today, d(2026, 10, 29)},
		{"bare weeks", "2w", today, d(2026, 10, 29)},
		{"weekday later this week", "fri", today, d(2026, 10, 16)},
		{"weekday full name", "Saturday", today, d(2026, 10, 17)},
		{"weekday wraps to next week", "mon", today, d(2026, 10, 19)},
		{"own weekday is 7 days ahead", "thu", today, d(2026, 10, 22)},
		{"own weekday full name", "thursday", today, d(2026, 10, 22)},
		{"weekday just before today", "wed", today, d(2026, 10, 21)},
		{"iso date", "2026-11-03", today, d(2026, 11, 3)},
		{"iso date in past", "2020-01-01", today, d(2020, 1, 1)},
		{"iso leap day", "2028-02-29", today, d(2028, 2, 29)},
		{"month rollover", "+1d", d(2026, 10, 31), d(2026, 11, 1)},
		{"year rollover", "+1d", d(2026, 12, 31), d(2027, 1, 1)},
		{"year rollback", "yesterday", d(2027, 1, 1), d(2026, 12, 31)},
		{"leap day reached", "tomorrow", d(2028, 2, 28), d(2028, 2, 29)},
		{"leap day skipped in non-leap year", "tomorrow", d(2027, 2, 28), d(2027, 3, 1)},
		{"after leap day", "tomorrow", d(2028, 2, 29), d(2028, 3, 1)},
		{"max 4 digit days", "+9999d", d(2026, 1, 1), d(2053, 5, 18)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDue(tc.in, tc.today)
			if err != nil {
				t.Fatalf("ParseDue(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseDue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDueRejectsMalformed(t *testing.T) {
	today := d(2026, 10, 15)
	for _, in := range []string{
		"", "   ", "garbage", "2026-13-01", "2026-02-30", "2027-02-29", "2026-1-1",
		"+99999d", "99999w", "-3d", "+d", "3", "+3x", "3 d", "tomorow",
		"2026-10-15T00:00:00Z", "funday", "\n",
		string(make([]byte, 100000)),
	} {
		if got, err := ParseDue(in, today); err == nil {
			t.Errorf("ParseDue(%.20q) = %v, want error", in, got)
		}
	}
}

func TestParseAPIDate(t *testing.T) {
	got, err := ParseAPIDate("2026-10-20T00:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := d(2026, 10, 20); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	// The calendar date is the UTC date, regardless of any offset suffix.
	got, err = ParseAPIDate("2026-10-20T23:30:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	if want := d(2026, 10, 21); got != want {
		t.Errorf("offset input: got %v, want %v", got, want)
	}
}

func TestParseAPIDateMalformed(t *testing.T) {
	for _, in := range []string{"", "garbage", "2026-10-20", "2026-13-01T00:00:00Z", "2026-10-20 00:00:00"} {
		if _, err := ParseAPIDate(in); err == nil {
			t.Errorf("ParseAPIDate(%q) succeeded, want error", in)
		}
	}
}

func TestRFC3339RoundTrip(t *testing.T) {
	for _, in := range []Date{d(2026, 10, 20), d(2028, 2, 29), d(2026, 12, 31), d(2027, 1, 1), d(1999, 1, 1)} {
		got, err := ParseAPIDate(in.RFC3339())
		if err != nil {
			t.Fatalf("%v: %v", in, err)
		}
		if got != in {
			t.Errorf("round trip %v -> %q -> %v", in, in.RFC3339(), got)
		}
	}
}

func TestDateString(t *testing.T) {
	if got := d(2026, 3, 5).String(); got != "2026-03-05" {
		t.Errorf("String() = %q", got)
	}
}

func TestDaysUntil(t *testing.T) {
	tests := []struct {
		name     string
		from, to Date
		want     int
	}{
		{"same day", d(2026, 10, 15), d(2026, 10, 15), 0},
		{"next day", d(2026, 10, 15), d(2026, 10, 16), 1},
		{"previous day", d(2026, 10, 15), d(2026, 10, 14), -1},
		{"month boundary", d(2026, 1, 31), d(2026, 3, 1), 29},
		{"year boundary", d(2026, 12, 31), d(2027, 1, 1), 1},
		{"leap year span", d(2028, 1, 1), d(2029, 1, 1), 366},
		{"non-leap year span", d(2026, 1, 1), d(2027, 1, 1), 365},
		// US DST transitions must not skew day counts.
		{"spring forward week", d(2026, 3, 7), d(2026, 3, 9), 2},
		{"fall back week", d(2026, 10, 31), d(2026, 11, 2), 2},
		{"far past to today", d(1500, 1, 1), d(2026, 10, 15), 192405},
		{"today to far past", d(2026, 10, 15), d(1500, 1, 1), -192405},
		{"year 1 to today", d(1, 1, 1), d(2026, 10, 15), 739903},
		{"year 1 to year 9999", d(1, 1, 1), d(9999, 12, 31), 3652058},
		{"leap day in negative year", d(-4, 2, 28), d(-4, 3, 1), 2},
		{"non-leap negative year", d(-1, 1, 1), d(0, 1, 1), 365},
		{"year zero is leap", d(0, 1, 1), d(1, 1, 1), 366},
		{"negative century not leap", d(-100, 2, 28), d(-100, 3, 1), 1},
		{"400-year cycle across zero", d(-400, 1, 1), d(0, 1, 1), 146097},
		{"1900 not leap", d(1900, 2, 28), d(1900, 3, 1), 1},
		{"2000 leap", d(2000, 2, 28), d(2000, 3, 1), 2},
		{"backwards across months", d(2026, 3, 1), d(2026, 1, 31), -29},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.from.DaysUntil(tc.to); got != tc.want {
				t.Errorf("%v.DaysUntil(%v) = %d, want %d", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestAddDaysInvertsDaysUntil(t *testing.T) {
	base := d(2026, 10, 15)
	for _, n := range []int{-1000, -366, -1, 0, 1, 30, 365, 10000} {
		if got := base.DaysUntil(base.AddDays(n)); got != n {
			t.Errorf("AddDays(%d) then DaysUntil = %d", n, got)
		}
	}
}
