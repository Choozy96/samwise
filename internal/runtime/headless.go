package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ClaudeHeadless runs each turn as a fresh `claude -p` process (spec §5.4). It
// is stateless-rehydrated-from-memory: the orchestrator assembles the full
// context every run, so correctness never depends on a live session. Native
// continuity via --resume is an optimization layered on later.
type ClaudeHeadless struct {
	bin string
	log *slog.Logger
}

// NewClaudeHeadless constructs the adapter. bin is the claude CLI path/name.
func NewClaudeHeadless(bin string, log *slog.Logger) *ClaudeHeadless {
	return &ClaudeHeadless{bin: bin, log: log}
}

// Name identifies the runtime in settings and the runs table.
func (c *ClaudeHeadless) Name() string { return "claude-headless" }

// streamLine mirrors the subset of `--output-format stream-json` we consume.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`

	Message *struct {
		Model   string `json:"model"`
		Role    string `json:"role"`
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`

	// result event
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMS   int64   `json:"duration_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// Run spawns claude, streams events, and returns the final Result.
func (c *ClaudeHeadless) Run(ctx context.Context, req Request, onEvent func(Event)) (*Result, error) {
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	if req.Workspace == "" {
		return nil, fmt.Errorf("headless: workspace is required")
	}
	if err := os.MkdirAll(req.Workspace, 0o700); err != nil {
		return nil, fmt.Errorf("headless: workspace: %w", err)
	}

	// Write the assembled system context to a file to avoid command-line length
	// limits (transcripts/memory can be large).
	sysPath, cleanup, err := c.writeSystemFile(req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	mcpConfig := req.MCPConfigJSON
	if mcpConfig == "" {
		mcpConfig = `{"mcpServers":{}}`
	}

	// Built-in tools: disabled by default; a scoped set (Read/Bash/…) when the
	// run allows host tools, so skills with scripts can execute. The same tool
	// names are added to --allowedTools so unattended runs never prompt.
	builtinTools := ""
	allowed := req.AllowedTools
	if req.AllowHostTools {
		builtinTools = strings.Join(ScopedBuiltinTools, ",")
		allowed = append(append([]string{}, req.AllowedTools...), ScopedBuiltinTools...)
	}

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--tools", builtinTools,
		"--strict-mcp-config", // ignore the host's inherited MCP servers
		"--mcp-config", mcpConfig,
		// Full replacement (not append): the orchestrator owns the assistant's
		// identity and behavior (spec §5.5), not Claude Code's default coding
		// persona.
		"--system-prompt-file", sysPath,
		"--permission-mode", "default",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if len(allowed) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowed, " "))
	}
	if req.ResumeSession != "" {
		args = append(args, "--resume", req.ResumeSession)
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = req.Workspace
	cmd.Stdin = strings.NewReader(buildPrompt(req))
	// Inject the user's secrets as env vars (on top of the inherited environment)
	// so skill scripts can read them without the secret touching the prompt/memory.
	if len(req.Env) > 0 {
		env := os.Environ()
		for k, v := range req.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("headless: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("headless: start %s: %w", c.bin, err)
	}

	res := &Result{Model: req.Model}
	var accumulated strings.Builder

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow long JSON lines
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev streamLine
		if err := json.Unmarshal(line, &ev); err != nil {
			c.log.Debug("headless: unparsable stream line", "err", err)
			continue
		}
		c.handleEvent(ev, res, &accumulated, onEvent)
	}
	scanErr := sc.Err()

	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		// A result event with is_error already populated res; prefer surfacing
		// the process error so the caller records a failed run.
		return res, fmt.Errorf("headless: claude failed: %s", msg)
	}
	if scanErr != nil {
		return res, fmt.Errorf("headless: reading output: %w", scanErr)
	}

	if res.FinalText == "" {
		res.FinalText = strings.TrimSpace(accumulated.String())
	}
	return res, nil
}

func (c *ClaudeHeadless) handleEvent(ev streamLine, res *Result, acc *strings.Builder, onEvent func(Event)) {
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			res.SessionID = ev.SessionID
			if res.Model == "" {
				res.Model = ev.Model
			}
		}
	case "assistant":
		if ev.Message == nil {
			return
		}
		for _, block := range ev.Message.Content {
			switch block.Type {
			case "text":
				if block.Text == "" {
					continue
				}
				acc.WriteString(block.Text)
				onEvent(Event{Kind: EventText, Text: block.Text})
			case "tool_use":
				summary := summarizeTool(block.Name, block.Input)
				res.ToolCalls = append(res.ToolCalls, ToolCall{Name: block.Name, Summary: summary})
				onEvent(Event{Kind: EventToolCall, Tool: summary})
			}
		}
	case "result":
		res.DurationMS = ev.DurationMS
		res.CostUSD = ev.TotalCostUSD
		if ev.SessionID != "" {
			res.SessionID = ev.SessionID
		}
		if ev.IsError {
			res.IsError = true
			res.ErrMsg = strings.TrimSpace(ev.Result)
			if res.ErrMsg == "" {
				res.ErrMsg = "runtime reported an error"
			}
			onEvent(Event{Kind: EventError, Text: res.ErrMsg})
			return
		}
		if t := strings.TrimSpace(ev.Result); t != "" {
			res.FinalText = t
		}
	}
}

// writeSystemFile persists the assembled context to a per-run file under the
// workspace, returning its path and a cleanup func.
func (c *ClaudeHeadless) writeSystemFile(req Request) (string, func(), error) {
	dir := filepath.Join(req.Workspace, ".runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("headless: run dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("run-%d.sys.md", req.RunID))
	if err := os.WriteFile(path, []byte(req.SystemContext), 0o600); err != nil {
		return "", func() {}, fmt.Errorf("headless: writing system file: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// buildPrompt renders the stdin prompt: recent transcript (context) followed by
// the new message. Stateless rehydration (spec §5.5).
func buildPrompt(req Request) string {
	if strings.TrimSpace(req.Transcript) == "" {
		return req.Prompt
	}
	var b strings.Builder
	b.WriteString("# Earlier conversation (context only — respond to the new message)\n\n")
	b.WriteString(req.Transcript)
	b.WriteString("\n\n# New message\n\n")
	b.WriteString(req.Prompt)
	return b.String()
}

func summarizeTool(name string, input json.RawMessage) string {
	s := strings.TrimSpace(string(input))
	const max = 140
	if len(s) > max {
		s = s[:max] + "…"
	}
	if s == "" || s == "null" {
		return name
	}
	return name + " " + s
}
