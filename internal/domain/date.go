package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date is a calendar date with no time or zone. Google Tasks stores due
// dates as dates only (the time part of the RFC 3339 value is discarded),
// so modelling them as a civil date avoids timezone off-by-one errors.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the calendar date of t in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

func (d Date) midnightUTC() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// AddDays returns the date n days after d (n may be negative).
func (d Date) AddDays(n int) Date { return DateOf(d.midnightUTC().AddDate(0, 0, n)) }

// DaysUntil returns the number of days from d to other (negative if other is earlier).
func (d Date) DaysUntil(other Date) int { return other.ordinal() - d.ordinal() }

// ordinal is the day count since 1970-01-01 in the proleptic Gregorian
// calendar (Hinnant's days_from_civil). Integer arithmetic avoids the
// ~292-year range limit of time.Duration.
func (d Date) ordinal() int {
	n := d.midnightUTC() // normalises out-of-range month/day values
	y, m, day := n.Year(), int(n.Month()), n.Day()
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func (d Date) Weekday() time.Weekday { return d.midnightUTC().Weekday() }

func (d Date) String() string { return d.midnightUTC().Format(time.DateOnly) }

// RFC3339 formats d the way the Tasks API expects a due date.
func (d Date) RFC3339() string { return d.midnightUTC().Format(time.RFC3339) }

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// ParseAPIDate parses a due timestamp returned by the Tasks API. The API
// stores midnight UTC of the due date, so the UTC calendar date is the due date.
func ParseAPIDate(s string) (Date, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid due timestamp %q: %w", s, err)
	}
	return DateOf(t.UTC()), nil
}

var relativeRe = regexp.MustCompile(`^\+?(\d{1,4})([dw])$`)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// ParseDue interprets a human due-date expression relative to today.
//
// Accepted forms: YYYY-MM-DD, today, tomorrow (tom), yesterday, +Nd / Nd
// (days), +Nw / Nw (weeks), and weekday names (mon, friday, ...). A weekday
// name means its next occurrence strictly after today.
func ParseDue(input string, today Date) (Date, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	switch s {
	case "":
		return Date{}, fmt.Errorf("empty due date")
	case "today":
		return today, nil
	case "tomorrow", "tom", "tmr":
		return today.AddDays(1), nil
	case "yesterday":
		return today.AddDays(-1), nil
	}
	if wd, ok := weekdays[s]; ok {
		delta := (int(wd) - int(today.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		return today.AddDays(delta), nil
	}
	if m := relativeRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1]) // regex guarantees digits, at most 4
		if m[2] == "w" {
			n *= 7
		}
		return today.AddDays(n), nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("unrecognised due date %q (try YYYY-MM-DD, today, tomorrow, +3d, +2w, fri)", input)
	}
	return DateOf(t), nil
}
