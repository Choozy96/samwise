package orchestrator

import "testing"

func TestLocalDayRangeUTC(t *testing.T) {
	// Asia/Singapore is UTC+8: 2026-06-14 local 00:00 = 2026-06-13 16:00 UTC.
	start, end := localDayRangeUTC("2026-06-14", "Asia/Singapore")
	if start != "2026-06-13 16:00:00" {
		t.Errorf("start: got %q", start)
	}
	if end != "2026-06-14 16:00:00" {
		t.Errorf("end: got %q", end)
	}
}

func TestRecentSinceDate(t *testing.T) {
	// A 2-day window's oldest date is exactly one day before the window's newest.
	got := recentSinceDate("UTC", 2)
	// Can't assert the absolute date without freezing time, but it must be < today.
	today := recentSinceDate("UTC", 1)
	if got >= today {
		t.Errorf("2-day window since (%s) should be before today (%s)", got, today)
	}
}
