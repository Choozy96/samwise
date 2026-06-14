package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshTokenBlob(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("refresh_token") == "dead" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"new-at","expires_in":3600}`))
	}))
	defer srv.Close()

	// Near-expiry token → refreshes; new access token + future expiry written back.
	blob := `{"refresh_token":"rt","client_id":"c","client_secret":"s","token_uri":"` + srv.URL + `","expiry":"2000-01-01T00:00:00Z"}`
	upd, changed, dead := refreshTokenBlob(ctx, blob)
	if !changed || dead {
		t.Fatalf("expected a refresh, got changed=%v dead=%v", changed, dead)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(upd), &m); err != nil {
		t.Fatal(err)
	}
	if m["access_token"] != "new-at" {
		t.Errorf("access_token not updated: %v", m["access_token"])
	}
	if _, ok := m["expiry"].(string); !ok {
		t.Error("expiry not set after refresh")
	}

	// Dead refresh token → re-auth needed.
	deadBlob := `{"refresh_token":"dead","client_id":"c","token_uri":"` + srv.URL + `","expiry":"2000-01-01T00:00:00Z"}`
	if _, _, dead := refreshTokenBlob(ctx, deadBlob); !dead {
		t.Error("expected dead=true for invalid_grant")
	}

	// Still-fresh token → no-op (token_uri intentionally unreachable; must not be hit).
	fresh := `{"refresh_token":"rt","client_id":"c","token_uri":"http://127.0.0.1:1/none","expiry":"2999-01-01T00:00:00Z"}`
	if _, changed, _ := refreshTokenBlob(ctx, fresh); changed {
		t.Error("fresh token should not refresh")
	}

	// Not a refreshable bundle (no refresh_token) → no-op.
	if _, changed, _ := refreshTokenBlob(ctx, `{"access_token":"x"}`); changed {
		t.Error("non-refreshable blob should be a no-op")
	}
}
