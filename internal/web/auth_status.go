package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AuthStatus is a read-only view of a credential's expiry for the Secrets page.
// The value itself is never included — only its name and expiry state.
type AuthStatus struct {
	Label   string // "Claude subscription" or the secret name
	Detail  string // human-readable status line
	Expired bool
	Warn    bool // expires soon (< 24h)
}

// claudeAuthStatus reads the runtime's OAuth credentials file and reports the
// subscription token's expiry. Returns ok=false when there's no such file (e.g.
// the runtime is using an API key instead) — then nothing is shown.
func claudeAuthStatus() (AuthStatus, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return AuthStatus{}, false
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return AuthStatus{}, false
	}
	var c struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &c) != nil || c.ClaudeAiOauth.ExpiresAt == 0 {
		return AuthStatus{}, false
	}
	return expiryStatus("Claude subscription", time.UnixMilli(c.ClaudeAiOauth.ExpiresAt)), true
}

// oauthSecretStatus parses a decrypted token JSON blob for an expiry and reports
// it read-only. ok=false when the blob has no recognizable expiry field (e.g. a
// Google token with only a refresh token).
func oauthSecretStatus(name, jsonBlob string) (AuthStatus, bool) {
	exp, ok := parseTokenExpiry(jsonBlob)
	if !ok {
		return AuthStatus{Label: name, Detail: "OAuth credential stored (no expiry field — refresh-token based)"}, true
	}
	return expiryStatus(name, exp), true
}

// parseTokenExpiry best-effort extracts an expiry time from common token shapes:
// expiresAt/expires_at as epoch ms, or expiry/expires_at as an RFC3339 string.
func parseTokenExpiry(jsonBlob string) (time.Time, bool) {
	var m map[string]any
	if json.Unmarshal([]byte(jsonBlob), &m) != nil {
		return time.Time{}, false
	}
	// Nested claudeAiOauth (in case a claude creds file is stored as a secret).
	if inner, ok := m["claudeAiOauth"].(map[string]any); ok {
		m = inner
	}
	for _, k := range []string{"expiresAt", "expires_at_ms"} {
		if v, ok := m[k].(float64); ok && v > 0 {
			return time.UnixMilli(int64(v)), true
		}
	}
	for _, k := range []string{"expiry", "expires_at", "expiry_date"} {
		if s, ok := m[k].(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t, true
			}
		}
		// expiry_date is sometimes epoch ms as a number.
		if v, ok := m[k].(float64); ok && v > 1e12 {
			return time.UnixMilli(int64(v)), true
		}
	}
	return time.Time{}, false
}

func expiryStatus(label string, exp time.Time) AuthStatus {
	now := time.Now()
	st := AuthStatus{Label: label}
	switch {
	case now.After(exp):
		st.Expired = true
		st.Detail = fmt.Sprintf("EXPIRED %s ago (%s)", roundDur(now.Sub(exp)), exp.Format("2006-01-02 15:04 MST"))
	case exp.Sub(now) < 24*time.Hour:
		st.Warn = true
		st.Detail = fmt.Sprintf("expires in %s (%s)", roundDur(exp.Sub(now)), exp.Format("2006-01-02 15:04 MST"))
	default:
		st.Detail = fmt.Sprintf("valid — expires %s", exp.Format("2006-01-02 15:04 MST"))
	}
	return st
}

func roundDur(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
