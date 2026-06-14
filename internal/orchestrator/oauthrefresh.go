package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// oauthRefreshWindow: refresh a token when it's this close to expiry (or when
	// we don't yet know its expiry). The scheduler runs the refresher more often
	// than this, so a token is renewed before it can lapse.
	oauthRefreshWindow = 20 * time.Minute
	googleTokenURI     = "https://oauth2.googleapis.com/token"
)

var oauthHTTP = &http.Client{Timeout: 20 * time.Second}

// RefreshOAuthSecrets refreshes the user's oauth-kind secrets that are near
// expiry, writing the new tokens back encrypted (rotation-aware). It returns the
// names of credentials whose refresh token was rejected (dead/revoked) so the
// caller can alert the user to re-authenticate. Transient errors are ignored
// (retried next cycle), not reported as dead.
func (o *Orchestrator) RefreshOAuthSecrets(ctx context.Context, userID int64) (deadReauth []string) {
	if o.box == nil || !o.box.Enabled() {
		return nil
	}
	secs, err := o.db.ListSecrets(ctx, userID)
	if err != nil {
		o.log.Error("oauth refresh: list secrets", "user_id", userID, "err", err)
		return nil
	}
	for _, s := range secs {
		if s.Kind != "oauth" {
			continue
		}
		raw, derr := o.box.Decrypt(s.ValueEnc)
		if derr != nil {
			continue
		}
		updated, changed, dead := refreshTokenBlob(ctx, string(raw))
		switch {
		case dead:
			deadReauth = append(deadReauth, s.Name)
			o.log.Warn("oauth refresh: refresh token rejected", "user_id", userID, "name", s.Name)
			_ = o.db.AddAuditEvent(ctx, userID, 0, "secret", "oauth_refresh", s.Name, "needs reauth")
		case changed:
			enc, eerr := o.box.Encrypt([]byte(updated))
			if eerr != nil {
				continue
			}
			if serr := o.db.SetSecret(ctx, userID, s.Name, enc, "oauth"); serr != nil {
				o.log.Error("oauth refresh: store", "name", s.Name, "err", serr)
				continue
			}
			o.log.Info("oauth refresh: refreshed", "user_id", userID, "name", s.Name)
			_ = o.db.AddAuditEvent(ctx, userID, 0, "secret", "oauth_refresh", s.Name, "ok")
		}
	}
	return deadReauth
}

// refreshTokenBlob refreshes a standard OAuth2 token JSON if it's near expiry.
// Returns (updatedJSON, changed=true) on a successful refresh, (_, _, dead=true)
// if the provider rejected the refresh token (re-auth required), or all-false on
// a no-op (not yet near expiry, not refreshable, or a transient error).
func refreshTokenBlob(ctx context.Context, blob string) (updated string, changed, dead bool) {
	var m map[string]any
	if json.Unmarshal([]byte(blob), &m) != nil {
		return "", false, false
	}
	rt, _ := m["refresh_token"].(string)
	cid, _ := m["client_id"].(string)
	cs, _ := m["client_secret"].(string)
	if rt == "" || cid == "" {
		return "", false, false // not a refreshable token bundle
	}
	tokenURI, _ := m["token_uri"].(string)
	if tokenURI == "" {
		tokenURI = googleTokenURI
	}
	if exp, ok := blobExpiry(m); ok && time.Until(exp) > oauthRefreshWindow {
		return "", false, false // still fresh
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rt},
		"client_id":     {cid},
	}
	if cs != "" {
		form.Set("client_secret", cs)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return "", false, false // transient (network) — try next cycle
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if out.Error == "invalid_grant" || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return "", false, true // refresh token dead/revoked → re-auth
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", false, false // other/unknown — treat as transient
	}

	m["access_token"] = out.AccessToken
	if out.ExpiresIn > 0 {
		m["expiry"] = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	if out.RefreshToken != "" {
		m["refresh_token"] = out.RefreshToken // provider rotated it — keep the new one
	}
	b, merr := json.Marshal(m)
	if merr != nil {
		return "", false, false
	}
	return string(b), true, false
}

// RefreshClaudeAuth forces the claude CLI to refresh its own OAuth token by making
// a tiny invocation. The CLI refreshes from its refresh token on use — even when
// the access token has already expired — so this is the on-demand recovery path
// for the Claude subscription login: if the refresh token is still alive it
// self-heals; if it's dead, the result tells the user they must re-authenticate.
func (o *Orchestrator) RefreshClaudeAuth(ctx context.Context) (ok bool, msg string) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.cfg.ClaudeBin, "-p", "Reply with exactly: OK")
	out, err := cmd.CombinedOutput()
	if err == nil {
		detail := ""
		if exp, ok := claudeTokenExpiry(); ok {
			detail = " New token valid until " + exp.Format("2006-01-02 15:04 MST") + "."
		}
		return true, "✅ Claude login is healthy — its token was refreshed." + detail
	}
	low := strings.ToLower(string(out) + " " + err.Error())
	for _, sig := range []string{"401", "authenticate", "unauthorized", "oauth", "login", "expired", "credential", "invalid_grant"} {
		if strings.Contains(low, sig) {
			return false, "⚠️ Claude login can't auto-recover — its refresh token is dead/revoked. " +
				"Re-authenticate: copy a fresh ~/.claude/.credentials.json from a machine logged into `claude`, then restart. (See docs/DEPLOY.md.)"
		}
	}
	return false, "Couldn't verify the Claude login (the check didn't complete). Try again shortly."
}

// claudeTokenExpiry reads the runtime's credentials file for the post-refresh
// expiry, best-effort.
func claudeTokenExpiry() (time.Time, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}, false
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return time.Time{}, false
	}
	var c struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &c) != nil || c.ClaudeAiOauth.ExpiresAt == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(c.ClaudeAiOauth.ExpiresAt), true
}

// blobExpiry extracts an expiry from a token map (RFC3339 expiry/expires_at, or
// epoch-ms expiresAt).
func blobExpiry(m map[string]any) (time.Time, bool) {
	for _, k := range []string{"expiry", "expires_at"} {
		if s, ok := m[k].(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t, true
			}
		}
	}
	if v, ok := m["expiresAt"].(float64); ok && v > 0 {
		return time.UnixMilli(int64(v)), true
	}
	return time.Time{}, false
}
