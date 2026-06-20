package web

import (
	"net/http"
	"testing"

	"samwise/internal/config"
)

func TestClientIP(t *testing.T) {
	mkReq := func(remote, xff string) *http.Request {
		r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	// TrustProxy off: always the TCP peer; the header is ignored, so a directly
	// connecting client can't spoof its rate-limit identity.
	off := &Server{cfg: &config.Config{TrustProxy: false}}
	if got := off.clientIP(mkReq("203.0.113.9:5555", "1.2.3.4")); got != "203.0.113.9" {
		t.Errorf("proxy off: got %q, want 203.0.113.9", got)
	}

	on := &Server{cfg: &config.Config{TrustProxy: true}}
	// A client prepends a forged value; the trusted proxy appends the real one.
	// We take the right-most entry, so the forged value is ignored.
	if got := on.clientIP(mkReq("10.0.0.2:5555", "9.9.9.9, 203.0.113.9")); got != "203.0.113.9" {
		t.Errorf("proxy on: got %q, want 203.0.113.9 (right-most)", got)
	}
	// On, but no header present → fall back to the TCP peer.
	if got := on.clientIP(mkReq("10.0.0.2:5555", "")); got != "10.0.0.2" {
		t.Errorf("proxy on, no header: got %q, want 10.0.0.2", got)
	}
}
