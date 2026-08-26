package orchestrator

import "testing"

// TestAddressRoundTrip: parse/format round-trips, telegram keeps its legacy
// "tg:" stored form, and non-addresses are rejected.
func TestAddressRoundTrip(t *testing.T) {
	// Legacy telegram form parses and re-renders identically (stored data!).
	a, ok := ParseAddress("tg:1:-100200")
	if !ok || a.Channel != "telegram" || a.BotID != 1 || a.ChatID != "-100200" {
		t.Fatalf("tg parse: %+v %v", a, ok)
	}
	if a.String() != "tg:1:-100200" {
		t.Errorf("telegram must keep the legacy tg: form, got %q", a.String())
	}
	// "telegram:" spelling is accepted, normalized to the same address.
	if b, ok := ParseAddress("telegram:1:-100200"); !ok || b != a {
		t.Errorf("telegram: spelling should normalize: %+v %v", b, ok)
	}
	// Slack: string chat ids work.
	sl, ok := ParseAddress("slack:2:C0123AB")
	if !ok || sl.Channel != "slack" || sl.ChatID != "C0123AB" {
		t.Fatalf("slack parse: %+v %v", sl, ok)
	}
	if sl.String() != "slack:2:C0123AB" {
		t.Errorf("slack form: %q", sl.String())
	}
	// Non-addresses.
	for _, bad := range []string{"", "web", "telegram", "tg:1", "tg:x:5", "smoke:1:2", "tg:1:"} {
		if _, ok := ParseAddress(bad); ok {
			t.Errorf("%q should not parse as an address", bad)
		}
	}
	if !(Address{}).IsZero() || (Address{Channel: "telegram"}).IsZero() {
		t.Error("IsZero wrong")
	}
	if (Address{}).String() != "" {
		t.Error("zero address should render empty")
	}
}
