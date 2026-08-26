package runtime

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// TestAgentEnvStripsSecrets verifies app secrets (notably MASTER_KEY) are removed
// from the agent's environment so a Bash/Read tool can't read them or decrypt the
// DB's secrets, while normal vars and the user's own skill secrets pass through.
func TestAgentEnvStripsSecrets(t *testing.T) {
	t.Setenv("MASTER_KEY", "super-secret-key")
	t.Setenv("SESSION_KEY", "sess")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("HARMLESS_VAR", "ok")

	env := agentEnv(map[string]string{"TODOIST_TOKEN": "user-tok"})
	has := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	for _, secret := range []string{"MASTER_KEY=", "SESSION_KEY=", "TELEGRAM_BOT_TOKEN="} {
		if has(secret) {
			t.Errorf("app secret %q must be stripped from the agent env", secret)
		}
	}
	if !has("HARMLESS_VAR=ok") {
		t.Error("normal env vars should pass through to the agent")
	}
	if !has("TODOIST_TOKEN=user-tok") {
		t.Error("the user's own skill secrets should be present")
	}
}

// TestHeadlessParsesUsageTokens verifies the result event's usage object is
// parsed into per-type token counts on the Result.
func TestHeadlessParsesUsageTokens(t *testing.T) {
	line := `{"type":"result","subtype":"success","duration_ms":1234,"total_cost_usd":0.05,` +
		`"result":"done","usage":{"input_tokens":1000,"output_tokens":200,` +
		`"cache_creation_input_tokens":5000,"cache_read_input_tokens":12000}}`

	var ev streamLine
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	c := &ClaudeHeadless{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	res := &Result{}
	var acc strings.Builder
	c.handleEvent(ev, res, &acc, func(Event) {})

	if res.InputTokens != 1000 || res.OutputTokens != 200 {
		t.Errorf("in/out: got %d/%d want 1000/200", res.InputTokens, res.OutputTokens)
	}
	if res.CacheCreationTokens != 5000 || res.CacheReadTokens != 12000 {
		t.Errorf("cache: got write=%d read=%d want 5000/12000", res.CacheCreationTokens, res.CacheReadTokens)
	}
	if res.CostUSD != 0.05 || res.DurationMS != 1234 {
		t.Errorf("cost/dur: got %v/%d", res.CostUSD, res.DurationMS)
	}
}

// TestHeadlessCapturesErrorResult confirms claude's own error text (from an
// is_error result event) is captured into res.ErrMsg — that's what Run now
// surfaces instead of a bare "exit status 1".
func TestHeadlessCapturesErrorResult(t *testing.T) {
	line := `{"type":"result","subtype":"error","is_error":true,` +
		`"result":"Claude AI usage limit reached. Resets at 3pm."}`
	var ev streamLine
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	c := &ClaudeHeadless{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	res := &Result{}
	var sawErr string
	c.handleEvent(ev, res, &strings.Builder{}, func(e Event) {
		if e.Kind == EventError {
			sawErr = e.Text
		}
	})
	if !res.IsError {
		t.Error("res.IsError should be set")
	}
	if res.ErrMsg != "Claude AI usage limit reached. Resets at 3pm." {
		t.Errorf("ErrMsg not captured: %q", res.ErrMsg)
	}
	if sawErr == "" {
		t.Error("an EventError should have been emitted")
	}
}

// TestInitReportsMCPFailures: an init event with a failed MCP server logs an
// error (observability for the occasional attach failures) and doesn't disturb
// the rest of init parsing.
func TestInitReportsMCPFailures(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s1","model":"m",` +
		`"mcp_servers":[{"name":"core","status":"connected"},{"name":"notion","status":"failed"}]}`
	var ev streamLine
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	c := &ClaudeHeadless{log: slog.New(slog.NewTextHandler(&buf, nil))}
	res := &Result{}
	c.handleEvent(ev, res, &strings.Builder{}, func(Event) {})
	if res.SessionID != "s1" {
		t.Errorf("session = %q", res.SessionID)
	}
	out := buf.String()
	if !strings.Contains(out, "notion") || !strings.Contains(out, "failed") {
		t.Errorf("failed server not logged: %s", out)
	}
	if strings.Contains(out, `server=core`) {
		t.Errorf("connected server should not be logged as failure: %s", out)
	}
}

// TestAgentEnvMCPTimeouts: default MCP timeouts are injected unless overridden.
func TestAgentEnvMCPTimeouts(t *testing.T) {
	env := agentEnv(nil)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "MCP_TIMEOUT=60000") || !strings.Contains(joined, "MCP_TOOL_TIMEOUT=120000") {
		t.Errorf("default MCP timeouts missing:\n%s", joined)
	}
	env = agentEnv(map[string]string{"MCP_TIMEOUT": "5000"})
	joined = strings.Join(env, "\n")
	if !strings.Contains(joined, "MCP_TIMEOUT=5000") || strings.Contains(joined, "MCP_TIMEOUT=60000") {
		t.Errorf("user override not respected:\n%s", joined)
	}
}
