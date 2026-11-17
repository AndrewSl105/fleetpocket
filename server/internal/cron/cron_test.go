package cron

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseErrors(t *testing.T) {
	for _, e := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8",
		"5-1 * * * *", "*/0 * * * *", "a * * * *", "1,,2 * * * *", "* * * * * *"} {
		if _, err := Parse(e); err == nil {
			t.Errorf("Parse(%q) accepted", e)
		}
	}
	for _, e := range []string{"* * * * *", "*/15 9-17 * * MON-FRI", "0 9 * * 1-5", "@daily", "@WEEKLY", "0 0 1 jan,jul *", "30 4 1,15 * 7", "5/20 * * * *"} {
		if _, err := Parse(e); err != nil {
			t.Errorf("Parse(%q): %v", e, err)
		}
	}
}

func TestLoadLocation(t *testing.T) {
	for _, n := range []string{"", "Local", "Mars/Olympus"} {
		if _, err := LoadLocation(n); err == nil {
			t.Errorf("LoadLocation(%q) accepted", n)
		}
	}
	mustLoc(t, "Europe/Kyiv")
	mustLoc(t, "UTC")
}

func TestNext(t *testing.T) {
	utc := time.UTC
	ny := mustLoc(t, "America/New_York")
	cases := []struct {
		expr, from, want string
		loc              *time.Location
	}{
		{"0 9 * * *", "2026-11-17 08:59", "2026-11-17 09:00", ny},
		{"0 9 * * *", "2026-11-17 09:00", "2026-11-18 09:00", ny},
		// Weekdays: Friday 9:00 → Monday 9:00.
		{"0 9 * * 1-5", "2026-11-20 09:00", "2026-11-23 09:00", utc},
		{"0 9 * * MON", "2026-11-17 10:00", "2026-11-23 09:00", utc},
		{"*/15 * * * *", "2026-11-17 10:01", "2026-11-17 10:15", utc},
		{"5/20 * * * *", "2026-11-17 10:26", "2026-11-17 10:45", utc},
		{"0 0 1 jan,jul *", "2026-11-17 10:00", "2027-01-01 00:00", utc},
		// Both day fields restricted: either matches (the 1st or a Sunday).
		{"0 6 1 * 0", "2026-11-17 00:00", "2026-11-22 06:00", utc},
		// 7 is Sunday.
		{"0 6 * * 7", "2026-11-17 00:00", "2026-11-22 06:00", utc},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00", utc},
		{"@weekly", "2026-11-17 00:00", "2026-11-22 00:00", utc},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		got := s.Next(at(t, c.loc, c.from), c.loc)
		if want := at(t, c.loc, c.want); !got.Equal(want) {
			t.Errorf("%s after %s: got %s, want %s", c.expr, c.from, got.In(c.loc), want)
		}
	}
	never, _ := Parse("0 0 30 2 *")
	if got := never.Next(time.Now(), utc); !got.IsZero() {
		t.Errorf("Feb 30 fired at %s", got)
	}
}

func TestNextDSTSpringForward(t *testing.T) {
	// 2026-03-08 in New York: 02:00 EST jumps to 03:00 EDT.
	ny := mustLoc(t, "America/New_York")
	s, _ := Parse("30 2 * * *")
	got := s.Next(at(t, ny, "2026-03-07 03:00"), ny)
	want := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC) // 03:00 EDT, right after the gap
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.In(ny), want.In(ny))
	}
	// The day after, 02:30 exists again.
	got = s.Next(got, ny)
	if want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got.In(ny), want.In(ny))
	}
	// A daily job outside the gap keeps its wall time across the change.
	nine, _ := Parse("0 9 * * *")
	a := nine.Next(at(t, ny, "2026-03-07 10:00"), ny)
	if a.In(ny).Hour() != 9 || a.Sub(nine.Next(at(t, ny, "2026-03-06 10:00"), ny)) != 23*time.Hour {
		t.Fatalf("9:00 across spring forward: %s", a.In(ny))
	}
}

func TestNextDSTFallBack(t *testing.T) {
	// 2026-11-01 in New York: 02:00 EDT falls back to 01:00 EST; 01:30 happens twice.
	ny := mustLoc(t, "America/New_York")
	s, _ := Parse("30 1 * * *")
	first := s.Next(at(t, ny, "2026-10-31 12:00"), ny)
	if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !first.Equal(want) { // 01:30 EDT
		t.Fatalf("got %s (%s), want 01:30 EDT", first.In(ny), first.UTC())
	}
	// Once only: the repeated 01:30 EST is skipped.
	second := s.Next(first, ny)
	if want := time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC); !second.Equal(want) {
		t.Fatalf("got %s (%s), want 2026-11-02 01:30 EST", second.In(ny), second.UTC())
	}
}

func TestNextDSTKyiv(t *testing.T) {
	// Europe/Kyiv: 2026-03-29 03:00 EET → 04:00 EEST.
	kyiv := mustLoc(t, "Europe/Kyiv")
	s, _ := Parse("15 3 * * *")
	got := s.Next(at(t, kyiv, "2026-03-28 12:00"), kyiv)
	if want := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC); !got.Equal(want) { // 04:00 EEST
		t.Fatalf("got %s, want 04:00 EEST", got.In(kyiv))
	}
}

func TestDueMissedFiring(t *testing.T) {
	s, _ := Parse("0 * * * *") // hourly
	utc := time.UTC
	due := at(t, utc, "2026-11-17 10:00")
	// 20 minutes late (server was down): fires once; next is 11:00.
	fire, next := s.Due(due, at(t, utc, "2026-11-17 10:20"), utc)
	if !fire || !next.Equal(at(t, utc, "2026-11-17 11:00")) {
		t.Fatalf("20 min late: fire=%v next=%s", fire, next)
	}
	// Three hours late: more than one interval, skipped; next is after now.
	fire, next = s.Due(due, at(t, utc, "2026-11-17 13:05"), utc)
	if fire || !next.Equal(at(t, utc, "2026-11-17 14:00")) {
		t.Fatalf("3 h late: fire=%v next=%s", fire, next)
	}
	// Exactly one interval late: the following firing is due itself, so this one is missed.
	fire, _ = s.Due(due, at(t, utc, "2026-11-17 11:00"), utc)
	if fire {
		t.Fatal("one interval late fired")
	}
	// Weekly: two days late is still within the interval.
	w, _ := Parse("0 9 * * 1")
	fire, next = w.Due(at(t, utc, "2026-11-16 09:00"), at(t, utc, "2026-11-18 08:00"), utc)
	if !fire || !next.Equal(at(t, utc, "2026-11-23 09:00")) {
		t.Fatalf("weekly 2 days late: fire=%v next=%s", fire, next)
	}
}
