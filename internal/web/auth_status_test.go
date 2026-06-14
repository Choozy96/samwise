package web

import (
	"fmt"
	"testing"
	"time"
)

func TestParseTokenExpiry(t *testing.T) {
	future := time.Now().Add(48 * time.Hour)
	cases := []struct {
		name string
		blob string
		ok   bool
	}{
		{"claude nested ms", fmt.Sprintf(`{"claudeAiOauth":{"expiresAt":%d}}`, future.UnixMilli()), true},
		{"top-level ms", fmt.Sprintf(`{"expiresAt":%d}`, future.UnixMilli()), true},
		{"rfc3339 expiry", `{"expiry":"2030-01-01T00:00:00Z"}`, true},
		{"google refresh-only", `{"refresh_token":"x","client_id":"y","client_secret":"z"}`, false},
		{"garbage", `not json`, false},
	}
	for _, c := range cases {
		_, ok := parseTokenExpiry(c.blob)
		if ok != c.ok {
			t.Errorf("%s: got ok=%v want %v", c.name, ok, c.ok)
		}
	}
}

func TestExpiryStatus(t *testing.T) {
	if st := expiryStatus("x", time.Now().Add(-time.Hour)); !st.Expired {
		t.Error("past expiry should be Expired")
	}
	if st := expiryStatus("x", time.Now().Add(2*time.Hour)); st.Expired || !st.Warn {
		t.Errorf("2h-out should Warn, not Expired: %+v", st)
	}
	if st := expiryStatus("x", time.Now().Add(72*time.Hour)); st.Expired || st.Warn {
		t.Errorf("72h-out should be plain valid: %+v", st)
	}
}

// oauthSecretStatus on a refresh-only blob reports it as stored without expiry.
func TestOAuthSecretStatusNoExpiry(t *testing.T) {
	st, ok := oauthSecretStatus("GOOGLE_OAUTH", `{"refresh_token":"x"}`)
	if !ok || st.Expired {
		t.Errorf("expected ok, not expired: %+v ok=%v", st, ok)
	}
}
