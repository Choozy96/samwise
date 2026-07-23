package web

import (
	"fmt"
	"strings"
	"testing"
)

// TestTZOptions verifies the timezone datalist: every whole-hour UTC offset a
// clock can be pinned to is represented, labels carry the offset, and the list
// is sorted west → east.
func TestTZOptions(t *testing.T) {
	opts := tzOptions()
	if len(opts) < 30 {
		t.Fatalf("catalog unexpectedly small: %d", len(opts))
	}
	seen := map[string]bool{}
	for _, o := range opts {
		if !strings.HasPrefix(o.Label, "UTC+") && !strings.HasPrefix(o.Label, "UTC-") {
			t.Errorf("bad label %q for %s", o.Label, o.Name)
		}
		seen[o.Label] = true
	}
	// Coverage probes at DST-invariant anchors (DST can shift other cities ±1h).
	for _, want := range []string{
		"UTC-12:00", "UTC-11:00", "UTC-10:00", "UTC+00:00", "UTC+03:00",
		"UTC+05:30", "UTC+07:00", "UTC+08:00", "UTC+09:00", "UTC+14:00",
	} {
		if !seen[want] {
			t.Errorf("no zone with offset %s in the catalog", want)
		}
	}
	// Sorted by offset (west → east).
	prev := -1 << 30
	for _, o := range opts {
		off := parseOffsetLabel(t, o.Label)
		if off < prev {
			t.Fatalf("catalog not sorted at %s (%s)", o.Name, o.Label)
		}
		prev = off
	}
}

func parseOffsetLabel(t *testing.T, label string) int {
	t.Helper()
	var h, m int
	sign := 1
	body := strings.TrimPrefix(label, "UTC")
	if strings.HasPrefix(body, "-") {
		sign = -1
	}
	if _, err := fmt.Sscanf(strings.TrimLeft(body, "+-"), "%d:%d", &h, &m); err != nil {
		t.Fatalf("unparsable label %q", label)
	}
	return sign * (h*60 + m)
}
