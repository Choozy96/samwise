// Package schedule holds the timezone-aware fire-time math for the scheduler
// (spec §8.2). All functions are pure given a clock instant, so they are easy to
// reason about and reuse from both the tick loop and the set_timezone tool.
//
// next_fire is materialized (computed and stored), then recomputed on a
// timezone change rather than evaluated every tick.
package schedule

import (
	"context"
	"fmt"
	"strings"
	"time"

	"samwise/internal/store"
)

// Kind enumerates supported schedule shapes.
type Kind int

const (
	Once Kind = iota
	Daily
	Weekly
)

// Spec is a parsed schedule_spec.
type Spec struct {
	Kind  Kind
	Hour  int
	Min   int
	DOW   time.Weekday // Weekly
	Local time.Time    // Once: wall-clock (year..minute), zone ignored
}

// onceLayout is the local wall-clock format for one-shot schedules.
const onceLayout = "2006-01-02T15:04"

// Parse parses a schedule_spec:
//
//	once@2026-06-13T15:00     one-shot at a local wall time
//	daily@07:00               every day at a local time
//	weekly@Mon 09:00          every week on a weekday at a local time
func Parse(spec string) (Spec, error) {
	at := strings.SplitN(strings.TrimSpace(spec), "@", 2)
	if len(at) != 2 {
		return Spec{}, fmt.Errorf("schedule: missing '@' in %q", spec)
	}
	kind, body := strings.ToLower(at[0]), strings.TrimSpace(at[1])
	switch kind {
	case "once":
		t, err := time.Parse(onceLayout, body)
		if err != nil {
			return Spec{}, fmt.Errorf("schedule: bad once time %q (want %s)", body, onceLayout)
		}
		return Spec{Kind: Once, Local: t}, nil
	case "daily":
		h, m, err := parseHHMM(body)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: Daily, Hour: h, Min: m}, nil
	case "weekly":
		parts := strings.Fields(body)
		if len(parts) != 2 {
			return Spec{}, fmt.Errorf("schedule: weekly wants 'DOW HH:MM', got %q", body)
		}
		dow, ok := parseDOW(parts[0])
		if !ok {
			return Spec{}, fmt.Errorf("schedule: bad weekday %q", parts[0])
		}
		h, m, err := parseHHMM(parts[1])
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: Weekly, DOW: dow, Hour: h, Min: m}, nil
	default:
		return Spec{}, fmt.Errorf("schedule: unknown kind %q", kind)
	}
}

// LocationFor resolves the location a job's wall times are interpreted in
// (spec §8.2). Unknown zones fall back to UTC.
func LocationFor(tzMode, tzRef, userTZ string) *time.Location {
	switch tzMode {
	case "fixed_utc":
		return time.UTC
	case "fixed_tz":
		if loc, err := time.LoadLocation(tzRef); err == nil {
			return loc
		}
		return time.UTC
	default: // user_local
		if loc, err := time.LoadLocation(userTZ); err == nil {
			return loc
		}
		return time.UTC
	}
}

// NextFireUTC returns the first occurrence strictly after `after`, in UTC.
// ok is false when there is no future occurrence (a one-shot already in the
// past). Using time.Date to build wall times makes this DST-correct.
func NextFireUTC(spec Spec, loc *time.Location, after time.Time) (time.Time, bool) {
	switch spec.Kind {
	case Once:
		t := time.Date(spec.Local.Year(), spec.Local.Month(), spec.Local.Day(),
			spec.Local.Hour(), spec.Local.Minute(), 0, 0, loc)
		if t.After(after) {
			return t.UTC(), true
		}
		return time.Time{}, false
	case Daily:
		base := after.In(loc)
		for d := 0; d <= 8; d++ {
			c := time.Date(base.Year(), base.Month(), base.Day()+d, spec.Hour, spec.Min, 0, 0, loc)
			if c.After(after) {
				return c.UTC(), true
			}
		}
	case Weekly:
		base := after.In(loc)
		for d := 0; d <= 8; d++ {
			c := time.Date(base.Year(), base.Month(), base.Day()+d, spec.Hour, spec.Min, 0, 0, loc)
			if c.Weekday() == spec.DOW && c.After(after) {
				return c.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// PeriodKey identifies the period a fire instant belongs to, used for the
// double-fire guard (spec §8.3). Daily/Weekly use the local date; Once is a
// single fire so its period is constant.
func PeriodKey(spec Spec, t time.Time, loc *time.Location) string {
	if spec.Kind == Once {
		return "once"
	}
	return t.In(loc).Format("2006-01-02")
}

// RecomputeUserLocal recomputes next_fire_utc for all of a user's enabled
// user_local jobs against their current timezone (spec §8.2). Intended to run in
// the same logical step as the timezone change. now is the clock instant to
// schedule after.
func RecomputeUserLocal(ctx context.Context, db *store.DB, userID int64, now time.Time) error {
	settings, err := db.GetSettings(ctx, userID)
	if err != nil {
		return err
	}
	jobs, err := db.ListUserLocalJobs(ctx, userID)
	if err != nil {
		return err
	}
	loc := LocationFor("user_local", "", settings.Timezone)
	for _, j := range jobs {
		spec, err := Parse(j.ScheduleSpec)
		if err != nil {
			continue // leave malformed jobs untouched
		}
		next, ok := NextFireUTC(spec, loc, now)
		nextStr := ""
		if ok {
			nextStr = next.UTC().Format(time.RFC3339)
		}
		if err := db.UpdateJobSchedule(ctx, j.ID, nextStr, j.LastFiredPeriod); err != nil {
			return err
		}
	}
	return nil
}

func parseHHMM(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("schedule: bad time %q (want HH:MM)", s)
	}
	return t.Hour(), t.Minute(), nil
}

func parseDOW(s string) (time.Weekday, bool) {
	switch strings.ToLower(s) {
	case "sun", "sunday":
		return time.Sunday, true
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tuesday":
		return time.Tuesday, true
	case "wed", "wednesday":
		return time.Wednesday, true
	case "thu", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	}
	return 0, false
}
