package validate

import (
	"strconv"
	"strings"
	"time"
)

// dateLayout is the normalised written form of a date-only value.
const dateLayout = "2006-01-02"

// DateParse is the outcome of reading a date out of a document.
//
// A struct rather than several return values because the ambiguous case is
// neither success nor failure: the text is a perfectly good date, and the
// reason no value comes back is that two readings of it are equally defensible.
type DateParse struct {
	// Time is the parsed value, meaningful only when OK.
	Time time.Time

	// OK reports whether a single value could be determined.
	OK bool

	// Ambiguity holds both readings when the date parsed but the order of its
	// day and month could not be settled. It is nil otherwise, and OK is false
	// whenever it is set: picking one would be a guess with a 50% error rate
	// (docs/schema.md §format, docs/rules.md §8.5).
	Ambiguity *DateAmbiguity

	// Reason says why there is no value, and is empty when OK. It never
	// contains the text it was given.
	Reason string
}

// ParseDate reads a date and returns it at midnight UTC, as docs/schema.md
// specifies for format=date.
//
// A value that also carries a time is accepted and truncated to the date as
// written, not as converted into some other zone: an invoice issued on 3 April
// in Kampala was issued on 3 April, whatever a UTC clock said at the time.
func ParseDate(s string, order DateOrder) DateParse {
	p := parse(s, order)
	if p.OK {
		t := p.Time
		p.Time = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	if p.Ambiguity != nil {
		p.Ambiguity = &DateAmbiguity{
			DayFirst:   midnight(p.Ambiguity.DayFirst),
			MonthFirst: midnight(p.Ambiguity.MonthFirst),
		}
	}
	return p
}

// ParseDateTime reads a date with a time, as docs/schema.md specifies for
// format=datetime.
//
// A value with no time component is accepted at midnight UTC. Rejecting it
// would flag every document that prints a date where the schema wanted a
// timestamp, and midnight is the same convention format=date already uses — it
// is a representation of the day, not an invented clock reading.
//
// A zone offset is honoured. A zone abbreviation other than UTC or GMT is not:
// resolving "EST" needs a zone database, ovrin has no dependencies, and
// guessing which of two zones share an abbreviation is the kind of confident
// wrongness this package refuses elsewhere.
func ParseDateTime(s string, order DateOrder) DateParse { return parse(s, order) }

// midnight is the date part of t, at midnight UTC.
func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// parse is the shared body of [ParseDate] and [ParseDateTime].
func parse(s string, order DateOrder) DateParse {
	tokens, ok := tokenise(s)
	if !ok {
		return DateParse{Reason: "not a recognisable date"}
	}

	clock, zone, dateTokens, ok := splitTime(tokens)
	if !ok {
		return DateParse{Reason: "the time of day is not a valid time"}
	}

	parts, ok := dateParts(dateTokens)
	if !ok {
		return DateParse{Reason: "not a recognisable date"}
	}

	y, m, d, amb, reason := resolve(parts, order)
	if amb != nil {
		return DateParse{
			Ambiguity: &DateAmbiguity{
				DayFirst:   time.Date(amb.year, time.Month(amb.dayFirstMonth), amb.dayFirstDay, clock.hour, clock.min, clock.sec, clock.nsec, zone),
				MonthFirst: time.Date(amb.year, time.Month(amb.monthFirstMonth), amb.monthFirstDay, clock.hour, clock.min, clock.sec, clock.nsec, zone),
			},
			Reason: "the day and month could not be told apart; set a date order to resolve it",
		}
	}
	if reason != "" {
		return DateParse{Reason: reason}
	}

	// time.Date normalises 31 February into 3 March without complaint, which is
	// exactly the silent correction this library must not make, so the calendar
	// date is checked on its own before the clock is added to it. Adding the
	// clock afterwards is also what makes 23:59:60 the leap second it is rather
	// than a date that does not exist.
	day := time.Date(y, time.Month(m), d, 0, 0, 0, 0, zone)
	if day.Year() != y || int(day.Month()) != m || day.Day() != d {
		return DateParse{Reason: "no such date in the calendar"}
	}
	t := day.Add(time.Duration(clock.hour)*time.Hour +
		time.Duration(clock.min)*time.Minute +
		time.Duration(clock.sec)*time.Second +
		time.Duration(clock.nsec)*time.Nanosecond)
	return DateParse{Time: t, OK: true}
}

// clockTime is a time of day, defaulting to midnight.
type clockTime struct {
	hour, min, sec, nsec int
}

// ambiguous carries both readings of a date whose day and month cannot be
// distinguished.
type ambiguous struct {
	year                           int
	dayFirstDay, dayFirstMonth     int
	monthFirstDay, monthFirstMonth int
}

// tokenise splits the text into the words a date is written from.
//
// Commas, ordinal suffixes and the "T" of an ISO timestamp are separators, not
// content. Everything else is left for the classifiers, so an unexpected token
// causes a refusal rather than a wrong reading.
func tokenise(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	// Split an ISO timestamp at its T, which is only a separator when it sits
	// between two digits. "Oct 3" must not lose its t.
	b := []byte(s)
	for i := 1; i < len(b)-1; i++ {
		if (b[i] == 'T' || b[i] == 't') && isDigit(b[i-1]) && isDigit(b[i+1]) {
			b[i] = ' '
		}
	}
	s = string(b)
	s = strings.NewReplacer(",", " ", "\t", " ", "\n", " ").Replace(s)

	var out []string
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, ".")
		if f == "" {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// splitTime pulls the time of day and the zone out of the tokens, leaving the
// date.
func splitTime(tokens []string) (clockTime, *time.Location, []string, bool) {
	clock := clockTime{}
	zone := time.UTC
	var rest []string
	var meridiem string
	haveClock := false

	for _, tok := range tokens {
		up := strings.ToUpper(tok)
		switch {
		case up == "AM" || up == "A.M" || up == "PM" || up == "P.M":
			meridiem = up[:1]
			continue
		case up == "Z" || up == "UTC" || up == "GMT" || up == "UT":
			continue
		case strings.ContainsRune(tok, ':') && !haveClock:
			body, off, hasOff := splitOffset(tok)
			c, ok := parseClock(body)
			if !ok {
				return clock, zone, nil, false
			}
			clock, haveClock = c, true
			if hasOff {
				loc, ok := parseZone(off)
				if !ok {
					return clock, zone, nil, false
				}
				zone = loc
			}
			continue
		case (tok[0] == '+' || tok[0] == '-') && haveClock:
			loc, ok := parseZone(tok)
			if !ok {
				return clock, zone, nil, false
			}
			zone = loc
			continue
		}
		rest = append(rest, tok)
	}

	if meridiem != "" {
		if clock.hour < 1 || clock.hour > 12 {
			return clock, zone, nil, false
		}
		if meridiem == "P" && clock.hour != 12 {
			clock.hour += 12
		}
		if meridiem == "A" && clock.hour == 12 {
			clock.hour = 0
		}
	}
	return clock, zone, rest, true
}

// splitOffset separates a zone offset written onto the end of a time.
func splitOffset(tok string) (body, offset string, ok bool) {
	for i := 1; i < len(tok); i++ {
		if tok[i] == '+' || tok[i] == '-' {
			return tok[:i], tok[i:], true
		}
	}
	if n := len(tok); n > 0 && (tok[n-1] == 'Z' || tok[n-1] == 'z') {
		return tok[:n-1], "", false
	}
	return tok, "", false
}

// parseClock reads HH:MM, HH:MM:SS and a fractional second.
func parseClock(s string) (clockTime, bool) {
	var c clockTime
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return c, false
	}
	h, ok := atoiRange(parts[0], 0, 23)
	if !ok {
		return c, false
	}
	m, ok := atoiRange(parts[1], 0, 59)
	if !ok {
		return c, false
	}
	c.hour, c.min = h, m
	if len(parts) == 3 {
		sec := parts[2]
		if dot := strings.IndexByte(sec, '.'); dot >= 0 {
			frac := sec[dot+1:]
			sec = sec[:dot]
			if len(frac) == 0 || len(frac) > 9 || !allDigits(frac) {
				return c, false
			}
			n, err := strconv.Atoi(frac + strings.Repeat("0", 9-len(frac)))
			if err != nil {
				return c, false
			}
			c.nsec = n
		}
		// 60 is a leap second, which real timestamps do contain.
		secs, ok := atoiRange(sec, 0, 60)
		if !ok {
			return c, false
		}
		c.sec = secs
	}
	return c, true
}

// parseZone reads +HH:MM, +HHMM and +HH.
func parseZone(s string) (*time.Location, bool) {
	if s == "" {
		return time.UTC, true
	}
	sign := 1
	switch s[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return nil, false
	}
	digits := strings.ReplaceAll(s[1:], ":", "")
	var h, m int
	var ok bool
	switch len(digits) {
	case 2:
		h, ok = atoiRange(digits, 0, 23)
	case 4:
		h, ok = atoiRange(digits[:2], 0, 23)
		if ok {
			m, ok = atoiRange(digits[2:], 0, 59)
		}
	default:
		return nil, false
	}
	if !ok {
		return nil, false
	}
	off := sign * (h*3600 + m*60)
	if off == 0 {
		return time.UTC, true
	}
	return time.FixedZone("", off), true
}

// dateParts reduces the date tokens to exactly three components.
//
// Two components — "April 2026" — are refused rather than completed with a
// first of the month, because the day would be invented (docs/rules.md §8.5).
func dateParts(tokens []string) ([]string, bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	var parts []string
	for _, tok := range tokens {
		if isMonthName(tok) || allDigits(tok) {
			parts = append(parts, tok)
			continue
		}
		if n, ok := ordinal(tok); ok {
			parts = append(parts, n)
			continue
		}
		split := strings.FieldsFunc(tok, func(r rune) bool {
			return r == '/' || r == '-' || r == '.' || r == '\\'
		})
		if len(split) < 2 {
			return nil, false
		}
		for _, p := range split {
			if n, ok := ordinal(p); ok {
				parts = append(parts, n)
				continue
			}
			if !isMonthName(p) && !allDigits(p) {
				return nil, false
			}
			parts = append(parts, p)
		}
	}

	// A compact date is one token carrying three components. 20260403 is
	// year first when its leading four digits can be a year and the rest a
	// month and a day; otherwise it is read as two short components and a
	// year, which leaves the day and month to the ordinary ambiguity rules.
	if len(parts) == 1 && len(parts[0]) == 8 && allDigits(parts[0]) {
		p := parts[0]
		if yearFirstCompact(p) {
			return []string{p[:4], p[4:6], p[6:]}, true
		}
		return []string{p[:2], p[2:4], p[4:]}, true
	}
	if len(parts) != 3 {
		return nil, false
	}
	return parts, true
}

// yearFirstCompact reports whether an eight-digit date reads as YYYYMMDD.
func yearFirstCompact(p string) bool {
	year, _ := strconv.Atoi(p[:4])
	month, _ := strconv.Atoi(p[4:6])
	day, _ := strconv.Atoi(p[6:])
	return year >= 1000 && month >= 1 && month <= 12 && day >= 1 && day <= 31
}

// ordinal strips an English ordinal suffix: 3rd is the 3rd.
func ordinal(tok string) (string, bool) {
	if len(tok) < 3 {
		return "", false
	}
	suffix := strings.ToLower(tok[len(tok)-2:])
	switch suffix {
	case "st", "nd", "rd", "th":
	default:
		return "", false
	}
	head := tok[:len(tok)-2]
	if !allDigits(head) {
		return "", false
	}
	return head, true
}

// resolve turns three components into a year, month and day, or reports that
// the day and month cannot be told apart.
func resolve(parts []string, order DateOrder) (year, month, day int, amb *ambiguous, reason string) {
	// A written month name settles the order on its own, which is why a
	// document that spells the month is worth more than one that does not.
	for i, p := range parts {
		if m, ok := monthNumber(p); ok {
			return withMonthName(parts, i, m)
		}
	}
	for _, p := range parts {
		if !allDigits(p) || len(p) > 4 {
			return 0, 0, 0, nil, "not a recognisable date"
		}
	}
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	c, _ := strconv.Atoi(parts[2])

	switch {
	case len(parts[0]) == 4:
		// A four-digit year first is ISO 8601 and reads year, month, day.
		// Y-D-M is not a convention anyone writes, so an impossible month is
		// an impossible date rather than an invitation to swap the two.
		return a, b, c, nil, ""

	case len(parts[2]) == 4:
		return dayMonth(c, a, b, order)

	case order == YearFirst:
		return expandYear(a), b, c, nil, ""

	default:
		return dayMonth(expandYear(c), a, b, order)
	}
}

// withMonthName places the day and year around a written month.
func withMonthName(parts []string, at, month int) (year, mon, day int, amb *ambiguous, reason string) {
	var nums []string
	for i, p := range parts {
		if i == at {
			continue
		}
		if !allDigits(p) || len(p) > 4 {
			return 0, 0, 0, nil, "not a recognisable date"
		}
		nums = append(nums, p)
	}
	if len(nums) != 2 {
		return 0, 0, 0, nil, "not a recognisable date"
	}
	first, _ := strconv.Atoi(nums[0])
	second, _ := strconv.Atoi(nums[1])

	switch {
	case len(nums[0]) == 4:
		return first, month, second, nil, ""
	case len(nums[1]) == 4:
		return second, month, first, nil, ""
	case first > 31 && second <= 31:
		return expandYear(first), month, second, nil, ""
	default:
		// Two short numbers around a month name: the year is written last.
		return expandYear(second), month, first, nil, ""
	}
}

// dayMonth settles a day and a month written as two numbers, or reports them
// ambiguous.
//
// Ambiguity is decided by the calendar first: 25/12 can only be a day and a
// month in that order, whatever the local convention. Only when both numbers
// are 12 or less, and they differ, is the reading genuinely undecidable.
func dayMonth(year, first, second int, order DateOrder) (y, m, d int, amb *ambiguous, reason string) {
	switch {
	case first > 12 && second > 12:
		return 0, 0, 0, nil, "no such date in the calendar"
	case first > 12:
		return year, second, first, nil, ""
	case second > 12:
		return year, first, second, nil, ""
	case first == second:
		// 03/03 is the third of March either way round.
		return year, first, second, nil, ""
	case first == 0 || second == 0:
		return 0, 0, 0, nil, "no such date in the calendar"
	}
	switch order {
	case DayFirst:
		return year, second, first, nil, ""
	case MonthFirst:
		return year, first, second, nil, ""
	case YearFirst:
		// The year was written last, so a year-first corpus says nothing about
		// the two remaining numbers.
	}
	return 0, 0, 0, &ambiguous{
		year:            year,
		dayFirstDay:     first,
		dayFirstMonth:   second,
		monthFirstDay:   second,
		monthFirstMonth: first,
	}, ""
}

// expandYear turns a two-digit year into a four-digit one.
//
// 69 and above is the twentieth century, matching the standard library's own
// two-digit year layout and POSIX. It is a convention, not a fact, and a
// document from 1965 will be read as 2065.
func expandYear(y int) int {
	switch {
	case y >= 100:
		return y
	case y >= 69:
		return 1900 + y
	default:
		return 2000 + y
	}
}

// months maps every spelling of a month a document uses.
//
// A table rather than a parse of time.Month because the abbreviations documents
// actually print — "Sept", a trailing full stop — are not the ones the standard
// library accepts.
var months = map[string]int{
	"jan": 1, "january": 1,
	"feb": 2, "february": 2,
	"mar": 3, "march": 3,
	"apr": 4, "april": 4,
	"may": 5,
	"jun": 6, "june": 6,
	"jul": 7, "july": 7,
	"aug": 8, "august": 8,
	"sep": 9, "sept": 9, "september": 9,
	"oct": 10, "october": 10,
	"nov": 11, "november": 11,
	"dec": 12, "december": 12,
}

// monthNumber returns the month a word names.
func monthNumber(s string) (int, bool) {
	m, ok := months[strings.ToLower(strings.Trim(s, "."))]
	return m, ok
}

// isMonthName reports whether a token names a month.
func isMonthName(s string) bool {
	_, ok := monthNumber(s)
	return ok
}

// atoiRange parses a bounded decimal, rejecting anything outside it.
func atoiRange(s string, lo, hi int) (int, bool) {
	if s == "" || len(s) > 2 || !allDigits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

// allDigits reports whether s is one or more ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
