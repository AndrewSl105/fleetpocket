// Package cron parses five-field cron expressions and finds their next
// firing in an IANA time zone (docs/SPEC-server.md, "Schedules").
//
// Fields: minute (0-59), hour (0-23), day of month (1-31), month (1-12 or
// JAN-DEC), day of week (0-7 or SUN-SAT; 0 and 7 are Sunday). Each field
// takes *, a value, a range a-b, a list a,b,c and a step */n or a-b/n.
// Macros: @hourly, @daily (@midnight), @weekly, @monthly, @yearly
// (@annually). When both day fields are restricted a day matches either
// (classic cron); otherwise both must match.
//
// Daylight saving time: times are wall-clock times in the schedule's zone.
// A time that does not exist (the hour skipped in spring) fires at the
// first instant after the gap; a time that happens twice (the hour repeated
// in autumn) fires once, at its first occurrence.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	// The server image has no system zoneinfo; embed it.
	_ "time/tzdata"
)

// Schedule is a parsed expression.
type Schedule struct {
	expr                    string
	minute, hour, dom, mon  uint64 // bit i set: value i matches
	dow                     uint64 // 0-6, Sunday = 0
	domRestricted, dowRestr bool
}

// horizon bounds the search for the next firing (e.g. "0 0 30 2 *" never fires).
const horizon = 5 * 366

var macros = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

var monthNames = map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}
var dayNames = map[string]int{"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6}

// Parse parses a five-field expression or a macro.
func Parse(expr string) (*Schedule, error) {
	e := strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(e)]; ok {
		e = m
	}
	f := strings.Fields(e)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron: %q needs 5 fields (minute hour day-of-month month day-of-week)", expr)
	}
	s := &Schedule{expr: strings.Join(f, " ")}
	var err error
	if s.minute, _, err = field(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	if s.hour, _, err = field(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	if s.dom, s.domRestricted, err = field(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("cron day of month: %w", err)
	}
	if s.mon, _, err = field(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	if s.dow, s.dowRestr, err = field(f[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("cron day of week: %w", err)
	}
	if s.dow&(1<<7) != 0 {
		s.dow = s.dow&^(1<<7) | 1 // 7 is Sunday too
	}
	return s, nil
}

// String is the normalised expression (macros expanded).
func (s *Schedule) String() string { return s.expr }

// field parses one field into a bitset; restricted is false for "*" (and "*/1").
func field(src string, lo, hi int, names map[string]int) (uint64, bool, error) {
	var bits uint64
	restricted := true
	for _, part := range strings.Split(src, ",") {
		if part == "" {
			return 0, false, errors.New("empty list item")
		}
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return 0, false, fmt.Errorf("bad step %q", stepStr)
			}
			step = n
		}
		a, b := lo, hi
		if rng == "*" {
			if step == 1 && len(src) == len(part) {
				restricted = false
			}
		} else {
			from, to, isRange := strings.Cut(rng, "-")
			var err error
			if a, err = value(from, lo, hi, names); err != nil {
				return 0, false, err
			}
			b = a
			if isRange {
				if b, err = value(to, lo, hi, names); err != nil {
					return 0, false, err
				}
				if b < a {
					return 0, false, fmt.Errorf("range %q runs backwards", rng)
				}
			} else if hasStep {
				b = hi // "a/n": from a to the end
			}
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, restricted, nil
}

func value(s string, lo, hi int, names map[string]int) (int, error) {
	if v, ok := names[strings.ToUpper(s)]; ok {
		return v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("value %d out of range %d-%d", n, lo, hi)
	}
	return n, nil
}

func (s *Schedule) dayMatches(d time.Time) bool {
	if s.mon&(1<<uint(d.Month())) == 0 {
		return false
	}
	dom := s.dom&(1<<uint(d.Day())) != 0
	dow := s.dow&(1<<uint(d.Weekday())) != 0
	if s.domRestricted && s.dowRestr {
		return dom || dow
	}
	return dom && dow
}

// Next is the first firing strictly after `after`, in loc. Zero when the
// expression has no firing within five years.
func (s *Schedule) Next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := after.In(loc)
	y, m, d := local.Date()
	for i := 0; i < horizon; i++ {
		// Noon is never inside a DST gap, so it names the calendar day safely.
		day := time.Date(y, m, d+i, 12, 0, 0, 0, loc)
		if !s.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if s.hour&(1<<uint(h)) == 0 {
				continue
			}
			for mi := 0; mi < 60; mi++ {
				if s.minute&(1<<uint(mi)) == 0 {
					continue
				}
				at := wallTime(day.Year(), day.Month(), day.Day(), h, mi, loc)
				if at.After(after) {
					return at
				}
			}
		}
	}
	return time.Time{}
}

// wallTime is the first instant whose wall clock in loc reads the given
// time; for a time inside a DST gap, the first instant after the gap.
func wallTime(y int, mo time.Month, d, h, mi int, loc *time.Location) time.Time {
	t := time.Date(y, mo, d, h, mi, 0, 0, loc)
	if t.Hour() != h || t.Minute() != mi {
		// In a gap Go may return a time before or after it; the gap ends at
		// the next zone transition after the wall time minus a day.
		return gapEnd(t, loc)
	}
	// A repeated wall time: prefer the earlier instant.
	for _, back := range []time.Duration{time.Hour, 30 * time.Minute} {
		e := t.Add(-back).In(loc)
		if e.Hour() == h && e.Minute() == mi && e.Day() == d {
			return e
		}
	}
	return t
}

// gapEnd finds the zone transition next to t (within a few hours) and
// returns the instant it happens.
func gapEnd(t time.Time, loc *time.Location) time.Time {
	start := t.Add(-3 * time.Hour)
	_, off0 := start.In(loc).Zone()
	for c := start; c.Before(t.Add(3 * time.Hour)); c = c.Add(time.Minute) {
		if _, off := c.In(loc).Zone(); off != off0 {
			return c.Truncate(time.Minute)
		}
	}
	return t
}

// LoadLocation loads an IANA zone ("Europe/Kyiv", "UTC"); "" and "Local"
// are refused so a schedule never depends on the server's zone.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("time zone %q: use an IANA name such as Europe/Berlin or UTC", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("time zone %q: %w", name, err)
	}
	return loc, nil
}

// Due decides a firing that is due: the schedule was to fire at `at` and it
// is now `now` (at <= now). A firing fires only if it is less than one
// interval late (now is before the firing after it); otherwise it was
// missed (the server was down) and is skipped. Either way the next firing
// is the first one after now, so at most one late firing runs.
func (s *Schedule) Due(at, now time.Time, loc *time.Location) (fire bool, next time.Time) {
	following := s.Next(at, loc)
	fire = following.IsZero() || now.Before(following)
	return fire, s.Next(now, loc)
}
