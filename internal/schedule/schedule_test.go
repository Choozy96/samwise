package schedule

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		ok   bool
	}{
		{"daily@07:00", Daily, true},
		{"weekly@Mon 09:30", Weekly, true},
		{"once@2026-06-20T15:00", Once, true},
		{"daily@7am", 0, false},
		{"weekly@Funday 09:00", 0, false},
		{"hourly@00", 0, false},
		{"daily", 0, false},
	}
	for _, c := range cases {
		s, err := Parse(c.in)
		if c.ok && err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Errorf("Parse(%q) expected error, got none", c.in)
		}
		if c.ok && s.Kind != c.kind {
			t.Errorf("Parse(%q) kind = %v, want %v", c.in, s.Kind, c.kind)
		}
	}
}

func TestNextFireDaily(t *testing.T) {
	sg := mustLoc(t, "Asia/Singapore")
	spec, _ := Parse("daily@07:00")

	// 06:00 local -> fire today 07:00.
	from := time.Date(2026, 6, 13, 6, 0, 0, 0, sg)
	got, ok := NextFireUTC(spec, sg, from)
	if !ok {
		t.Fatal("expected a fire time")
	}
	want := time.Date(2026, 6, 13, 7, 0, 0, 0, sg)
	if !got.Equal(want) {
		t.Errorf("daily before slot: got %v, want %v", got.In(sg), want)
	}

	// 08:00 local -> fire tomorrow 07:00.
	from = time.Date(2026, 6, 13, 8, 0, 0, 0, sg)
	got, _ = NextFireUTC(spec, sg, from)
	want = time.Date(2026, 6, 14, 7, 0, 0, 0, sg)
	if !got.Equal(want) {
		t.Errorf("daily after slot: got %v, want %v", got.In(sg), want)
	}
}

func TestNextFireWeekly(t *testing.T) {
	sg := mustLoc(t, "Asia/Singapore")
	spec, _ := Parse("weekly@Mon 09:00")
	// 2026-06-13 is a Saturday; next Monday is 2026-06-15.
	from := time.Date(2026, 6, 13, 12, 0, 0, 0, sg)
	got, ok := NextFireUTC(spec, sg, from)
	if !ok {
		t.Fatal("expected a fire time")
	}
	want := time.Date(2026, 6, 15, 9, 0, 0, 0, sg)
	if !got.Equal(want) {
		t.Errorf("weekly: got %v, want %v", got.In(sg), want)
	}
	if got.In(sg).Weekday() != time.Monday {
		t.Errorf("weekly fell on %v, want Monday", got.In(sg).Weekday())
	}
}

func TestNextFireOncePast(t *testing.T) {
	utc := time.UTC
	spec, _ := Parse("once@2020-01-01T00:00")
	if _, ok := NextFireUTC(spec, utc, time.Now()); ok {
		t.Error("expected no future fire for a past one-shot")
	}
}

// TestDailyDSTCorrect verifies fire times track wall-clock across a DST
// transition rather than drifting by an hour. US/Eastern springs forward on
// 2026-03-08.
func TestDailyDSTCorrect(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	spec, _ := Parse("daily@09:00")
	// Day before DST change, after the slot.
	from := time.Date(2026, 3, 7, 10, 0, 0, 0, ny)
	got, _ := NextFireUTC(spec, ny, from)
	if h := got.In(ny).Hour(); h != 9 {
		t.Errorf("post-DST fire hour = %d local, want 9", h)
	}
	if got.In(ny).Day() != 8 {
		t.Errorf("expected fire on the 8th, got %v", got.In(ny))
	}
}

// TestPeriodKeyDistinctDays ensures the double-fire guard key changes per day.
func TestPeriodKeyDistinctDays(t *testing.T) {
	sg := mustLoc(t, "Asia/Singapore")
	spec, _ := Parse("daily@07:00")
	d1 := PeriodKey(spec, time.Date(2026, 6, 13, 7, 0, 0, 0, sg), sg)
	d2 := PeriodKey(spec, time.Date(2026, 6, 14, 7, 0, 0, 0, sg), sg)
	if d1 == d2 {
		t.Errorf("period keys should differ across days: %s == %s", d1, d2)
	}
	if d1 != "2026-06-13" {
		t.Errorf("period key = %s, want 2026-06-13", d1)
	}
}
