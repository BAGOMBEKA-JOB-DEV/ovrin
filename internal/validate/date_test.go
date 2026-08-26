package validate

import (
	"testing"
	"time"
)

func utc(y, m, d, hh, mm, ss int) time.Time {
	return time.Date(y, time.Month(m), d, hh, mm, ss, 0, time.UTC)
}

func TestParseDateUnambiguousForms(t *testing.T) {
	t.Parallel()
	want := utc(2026, 4, 3, 0, 0, 0)
	cases := []struct {
		name string
		in   string
	}{
		{"ISO 8601", "2026-04-03"},
		{"ISO with slashes", "2026/04/03"},
		{"ISO with dots", "2026.04.03"},
		{"compact eight digits", "20260403"},
		{"an ISO timestamp truncated to its date", "2026-04-03T14:30:00Z"},
		{"a written month, day first", "3 April 2026"},
		{"a written month, month first", "April 3, 2026"},
		{"an abbreviated month", "3 Apr 2026"},
		{"an abbreviated month with a full stop", "3 Apr. 2026"},
		{"an ordinal day", "3rd April 2026"},
		{"an ordinal day after the month", "April 3rd, 2026"},
		{"an uppercase month", "3 APRIL 2026"},
		{"a two-digit year with a written month", "3 April 26"},
		{"surrounding whitespace", "  2026-04-03  "},
		{"the year written first with the month name last", "2026 3 April"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := ParseDate(c.in, DateOrderUnknown)
			if !p.OK {
				t.Fatalf("did not parse: %s", p.Reason)
			}
			if !p.Time.Equal(want) {
				t.Errorf("got %v, want %v", p.Time, want)
			}
			if p.Time.Location() != time.UTC {
				t.Errorf("location = %v, want UTC", p.Time.Location())
			}
		})
	}
}

func TestParseDateOrderSettlesTheDayAndMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    string
		order DateOrder
		want  time.Time
	}{
		{"day first reads 03/04 as 3 April", "03/04/2026", DayFirst, utc(2026, 4, 3, 0, 0, 0)},
		{"month first reads 03/04 as 4 March", "03/04/2026", MonthFirst, utc(2026, 3, 4, 0, 0, 0)},
		{"year first reads 2026/03/04 as 4 March", "2026/03/04", YearFirst, utc(2026, 3, 4, 0, 0, 0)},
		{"year first reads a short year first", "26/03/04", YearFirst, utc(2026, 3, 4, 0, 0, 0)},
		{"day first with a two-digit year", "03/04/26", DayFirst, utc(2026, 4, 3, 0, 0, 0)},
		{"month first with a two-digit year", "03/04/26", MonthFirst, utc(2026, 3, 4, 0, 0, 0)},
		{"day first with hyphens", "03-04-2026", DayFirst, utc(2026, 4, 3, 0, 0, 0)},
		{"a day past the twelfth ignores a contrary order", "25/04/2026", MonthFirst, utc(2026, 4, 25, 0, 0, 0)},
		{"a month slot past the twelfth is read the only way it parses", "04/25/2026", DayFirst, utc(2026, 4, 25, 0, 0, 0)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := ParseDate(c.in, c.order)
			if !p.OK {
				t.Fatalf("did not parse: %s (ambiguity %+v)", p.Reason, p.Ambiguity)
			}
			if !p.Time.Equal(c.want) {
				t.Errorf("got %v, want %v", p.Time, c.want)
			}
		})
	}
}

func TestParseDateAmbiguity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		in             string
		wantDayFirst   time.Time
		wantMonthFirst time.Time
	}{
		{"the canonical example", "03/04/2026", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
		{"with hyphens", "03-04-2026", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
		{"with dots", "03.04.2026", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
		{"with a two-digit year", "03/04/26", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
		{"single digits", "3/4/2026", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
		{"the first of the second", "01/02/2026", utc(2026, 2, 1, 0, 0, 0), utc(2026, 1, 2, 0, 0, 0)},
		{"the twelfth of the eleventh", "12/11/2026", utc(2026, 11, 12, 0, 0, 0), utc(2026, 12, 11, 0, 0, 0)},
		{"a year-first order says nothing about a year-last date", "03/04/2026", utc(2026, 4, 3, 0, 0, 0), utc(2026, 3, 4, 0, 0, 0)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			order := DateOrderUnknown
			if c.name == "a year-first order says nothing about a year-last date" {
				order = YearFirst
			}
			p := ParseDate(c.in, order)
			if p.OK {
				t.Fatalf("resolved %v, but the date is ambiguous and must not be guessed", p.Time)
			}
			if p.Ambiguity == nil {
				t.Fatalf("no ambiguity reported: %s", p.Reason)
			}
			if !p.Ambiguity.DayFirst.Equal(c.wantDayFirst) {
				t.Errorf("DayFirst = %v, want %v", p.Ambiguity.DayFirst, c.wantDayFirst)
			}
			if !p.Ambiguity.MonthFirst.Equal(c.wantMonthFirst) {
				t.Errorf("MonthFirst = %v, want %v", p.Ambiguity.MonthFirst, c.wantMonthFirst)
			}
			if p.Reason == "" {
				t.Error("an ambiguous date must say why it produced no value")
			}
		})
	}
}

func TestParseDateNotAmbiguousWhenBothReadingsAgree(t *testing.T) {
	t.Parallel()
	p := ParseDate("03/03/2026", DateOrderUnknown)
	if !p.OK || p.Ambiguity != nil {
		t.Fatalf("3 March is 3 March either way round: %+v", p)
	}
	if !p.Time.Equal(utc(2026, 3, 3, 0, 0, 0)) {
		t.Errorf("got %v", p.Time)
	}
}

func TestParseDateRejections(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"only whitespace", "   "},
		{"prose", "sometime last week"},
		{"a month and a year with no day", "April 2026"},
		{"a day that does not exist", "31/02/2026"},
		{"a month that does not exist", "2026-13-01"},
		{"two numbers that can both only be days", "25/26/2026"},
		{"a zero day", "00/04/2026"},
		{"a zero month", "04/00/2026"},
		{"a bare year", "2026"},
		{"a number with no date in it", "12345"},
		{"four components", "1/2/3/4"},
		{"a made-up month name", "3 Aprol 2026"},
		{"a time with no date", "14:30"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := ParseDate(c.in, DayFirst)
			if p.OK {
				t.Fatalf("parsed as %v, want a refusal", p.Time)
			}
			if p.Reason == "" {
				t.Error("a refusal must say why")
			}
		})
	}
}

func TestParseDateTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{"RFC 3339 in UTC", "2026-04-03T14:30:00Z", utc(2026, 4, 3, 14, 30, 0)},
		{"RFC 3339 with an offset", "2026-04-03T14:30:00+03:00", utc(2026, 4, 3, 11, 30, 0)},
		{"an offset with no colon", "2026-04-03T14:30:00+0300", utc(2026, 4, 3, 11, 30, 0)},
		{"a negative offset", "2026-04-03T09:30:00-05:00", utc(2026, 4, 3, 14, 30, 0)},
		{"a fractional second", "2026-04-03T14:30:00.500Z", utc(2026, 4, 3, 14, 30, 0).Add(500 * time.Millisecond)},
		{"a space instead of a T", "2026-04-03 14:30:00", utc(2026, 4, 3, 14, 30, 0)},
		{"no seconds", "2026-04-03 14:30", utc(2026, 4, 3, 14, 30, 0)},
		{"a twelve-hour clock", "3 April 2026 2:30 PM", utc(2026, 4, 3, 14, 30, 0)},
		{"a twelve-hour clock before noon", "3 April 2026 9:05 am", utc(2026, 4, 3, 9, 5, 0)},
		{"midnight on a twelve-hour clock", "3 April 2026 12:15 AM", utc(2026, 4, 3, 0, 15, 0)},
		{"noon on a twelve-hour clock", "3 April 2026 12:15 PM", utc(2026, 4, 3, 12, 15, 0)},
		{"a named UTC zone", "2026-04-03 14:30:00 UTC", utc(2026, 4, 3, 14, 30, 0)},
		{"GMT", "2026-04-03 14:30:00 GMT", utc(2026, 4, 3, 14, 30, 0)},
		{"a date with no time at all", "2026-04-03", utc(2026, 4, 3, 0, 0, 0)},
		{"a leap second", "2026-04-03T23:59:60Z", utc(2026, 4, 4, 0, 0, 0)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := ParseDateTime(c.in, DateOrderUnknown)
			if !p.OK {
				t.Fatalf("did not parse: %s", p.Reason)
			}
			if !p.Time.Equal(c.want) {
				t.Errorf("got %v, want %v", p.Time.UTC(), c.want)
			}
		})
	}
}

func TestParseDateTimeRejections(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"an hour that does not exist", "2026-04-03 25:00:00"},
		{"a minute that does not exist", "2026-04-03 14:75"},
		{"an offset that does not exist", "2026-04-03T14:30:00+99:00"},
		{"a malformed offset", "2026-04-03T14:30:00+3"},
		{"one o'clock in the afternoon of a thirteen-hour clock", "2026-04-03 13:00 PM"},
		{"a zone abbreviation needing a database", "2026-04-03 14:30 EST"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := ParseDateTime(c.in, DayFirst)
			if p.OK {
				t.Fatalf("parsed as %v, want a refusal", p.Time)
			}
		})
	}
}

func TestParseDateTruncatesATimeToTheDateAsWritten(t *testing.T) {
	t.Parallel()
	// 23:00 in Kampala on 3 April is 20:00 UTC on 3 April, and both are the
	// third. A document dated the third must not become the second.
	p := ParseDate("2026-04-03T23:00:00+03:00", DateOrderUnknown)
	if !p.OK {
		t.Fatalf("did not parse: %s", p.Reason)
	}
	if !p.Time.Equal(utc(2026, 4, 3, 0, 0, 0)) {
		t.Errorf("got %v, want 3 April at midnight UTC", p.Time)
	}
}

func TestExpandYear(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"the pivot year", 69, 1969},
		{"just below the pivot", 68, 2068},
		{"this decade", 26, 2026},
		{"the end of the last century", 99, 1999},
		{"a four-digit year is left alone", 1926, 1926},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := expandYear(c.in); got != c.want {
				t.Errorf("expandYear(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
