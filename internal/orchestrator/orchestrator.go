// Package orchestrator owns everything around an agent run (spec §1): context
// assembly, run dispatch, persistence of the transcript and run record, and
// (for scheduled jobs) delivery. The runtime is a swappable execution engine
// selected per user.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"samwise/internal/config"
	"samwise/internal/runtime"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// coreTools are the core MCP tool names (server "core"), pre-allowed on every
// run so unattended runs never stall on a permission prompt (spec §10.4).
var coreTools = []string{
	"mcp__core__memory_save",
	"mcp__core__memory_search",
	"mcp__core__memory_forget",
	"mcp__core__memory_list_topics",
	"mcp__core__set_timezone",
	"mcp__core__get_settings",
	"mcp__core__reminder_set",
	"mcp__core__reminder_list",
	"mcp__core__reminder_cancel",
	"mcp__core__job_create",
	"mcp__core__job_list",
	"mcp__core__job_update",
	"mcp__core__job_delete",
}

// Orchestrator dispatches runs through the active runtime and persists results.
type Orchestrator struct {
	cfg      *config.Config
	db       *store.DB
	log      *slog.Logger
	box      *secretbox.Box
	runtimes map[string]runtime.AgentRuntime
	fallback runtime.AgentRuntime
	exePath  string        // this binary, re-invoked as the core MCP server
	dbAbs    string        // absolute DB path for the MCP subprocess
	telegram ChannelSender // optional Telegram delivery sink (MVP step 6)
}

// New constructs an Orchestrator. The fallback runtime is used when a user's
// configured runtime is not registered (e.g. channels/codex not built yet).
func New(cfg *config.Config, db *store.DB, log *slog.Logger, box *secretbox.Box, runtimes ...runtime.AgentRuntime) *Orchestrator {
	m := make(map[string]runtime.AgentRuntime, len(runtimes))
	for _, rt := range runtimes {
		m[rt.Name()] = rt
	}
	o := &Orchestrator{cfg: cfg, db: db, log: log, box: box, runtimes: m}
	if rt, ok := m["claude-headless"]; ok {
		o.fallback = rt
	} else if len(runtimes) > 0 {
		o.fallback = runtimes[0]
	}
	if exe, err := os.Executable(); err == nil {
		o.exePath = exe
	}
	if abs, err := filepath.Abs(cfg.DBPath); err == nil {
		o.dbAbs = abs
	} else {
		o.dbAbs = cfg.DBPath
	}
	return o
}

// Attachment is a file the user attached to a message, saved in their workspace.
// Text is the inlined content for small text files (else empty), so text
// attachments work even without the runtime's file tools.
type Attachment struct {
	Name string
	Path string // absolute path the agent can read with its tools
	Text string
}

// DispatchRequest is one turn to execute. Dispatch resolves the agent (active
// agent if Agent is nil) and that agent's conversation thread on Channel.
type DispatchRequest struct {
	User        *store.User
	Channel     string       // "web" | "telegram"; defaults to "web"
	Agent       *store.Agent // optional; nil => the user's active agent
	UserMessage string
	Attachments []Attachment // optional files attached to this message
	// StoreUserMessage controls whether Dispatch persists the incoming message.
	// Web/Telegram pass true; internal jobs that craft their own prompt set false.
	StoreUserMessage bool
	// Silent runs read the conversation's context but write nothing back to it
	// (no user/assistant/tool messages, no session update) — for internal passes
	// like distillation that must not pollute the transcript or loop on themselves.
	Silent bool
}

// Dispatch runs one turn end-to-end: persist the user message, assemble context,
// execute the run, persist the assistant reply + tool-call summaries, and record
// the run. onEvent (may be nil) streams text/tool events for live UIs.
func (o *Orchestrator) Dispatch(ctx context.Context, req DispatchRequest, onEvent func(runtime.Event)) (*runtime.Result, error) {
	settings, err := o.db.GetSettings(ctx, req.User.ID)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	// Resolve the agent (active agent if unspecified) and its conversation thread.
	agent := req.Agent
	if agent == nil {
		agent, err = o.db.GetActiveAgent(ctx, req.User.ID)
		if err != nil {
			return nil, fmt.Errorf("resolving agent: %w", err)
		}
	}
	channel := req.Channel
	if channel == "" {
		channel = "web"
	}
	conv, err := o.db.GetOrCreateConversation(ctx, req.User.ID, channel, agent.ID)
	if err != nil {
		return nil, fmt.Errorf("resolving conversation: %w", err)
	}

	// Assemble from the transcript as it stands BEFORE this turn's message — the
	// new message is sent as the prompt, not duplicated into the transcript.
	// The incoming message drives memory retrieval (spec §5.5).
	asm, err := o.assemble(ctx, req.User, settings, agent, conv, req.UserMessage)
	if err != nil {
		return nil, fmt.Errorf("assembling context: %w", err)
	}

	// Attachments augment both the stored transcript entry (names only) and the
	// prompt the runtime sees (paths to read, plus inlined text content).
	storedMsg, runtimePrompt := composeWithAttachments(req.UserMessage, req.Attachments)

	if req.StoreUserMessage {
		if _, err := o.db.AddMessage(ctx, conv.ID, req.User.ID, channel, "user", storedMsg); err != nil {
			return nil, fmt.Errorf("storing user message: %w", err)
		}
	}

	rt := o.selectRuntime(agentRuntimeName(agent, settings))
	model := agentModel(agent, settings)

	runID, err := o.db.CreateRun(ctx, req.User.ID, conv.ID, rt.Name(), model)
	if err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	// Audit the inbound user message (not internal job prompts).
	if req.StoreUserMessage {
		_ = o.db.AddAuditEvent(ctx, req.User.ID, runID, "message", channel,
			fmt.Sprintf("agent=%s: %s", agent.Name, auditSnippet(req.UserMessage)), "ok")
	}

	mcpJSON, allowedTools := o.buildMCP(ctx, req.User.ID, runID)
	rreq := runtime.Request{
		UserID:         req.User.ID,
		RunID:          runID,
		Prompt:         runtimePrompt,
		SystemContext:  asm.systemContext,
		Transcript:     asm.transcript,
		Model:          model,
		Workspace:      o.workspace(req.User.ID),
		MCPConfigJSON:  mcpJSON,
		AllowedTools:   allowedTools,
		AllowHostTools: o.cfg.AllowAgentTools,
		Env:            o.userSecretsEnv(ctx, req.User.ID),
	}

	res, runErr := rt.Run(ctx, rreq, onEvent)

	// Persist outcome regardless of error so the transcript and runs table stay
	// truthful (spec §11: no silent failures).
	o.persistOutcome(ctx, conv, channel, req.User.ID, runID, res, runErr, req.Silent)

	if runErr != nil {
		return res, runErr
	}
	if res.IsError {
		return res, fmt.Errorf("runtime error: %s", res.ErrMsg)
	}
	return res, nil
}

func (o *Orchestrator) persistOutcome(ctx context.Context, conv *store.Conversation, channel string, userID, runID int64, res *runtime.Result, runErr error, silent bool) {
	status, errMsg := "success", ""
	var dur int64
	var cost float64
	if res != nil {
		dur, cost = res.DurationMS, res.CostUSD
		// Tool-call summaries go into the transcript (spec §5.5) so a rehydrated
		// runtime knows which writes already happened. Silent runs skip all
		// conversation writes but still audit tools and record the run.
		for _, tc := range res.ToolCalls {
			if !silent {
				if _, err := o.db.AddMessage(ctx, conv.ID, userID, channel, "tool", "[tool] "+tc.Summary); err != nil {
					o.log.Error("storing tool summary", "err", err)
				}
			}
			// Audit external tool calls here; core tools are audited by the MCP
			// subprocess (with richer args summaries) to avoid double-logging.
			if !strings.HasPrefix(tc.Name, "mcp__core__") {
				_ = o.db.AddAuditEvent(ctx, userID, runID, "tool", tc.Name, auditSnippet(tc.Summary), "ok")
			}
		}
		if !silent && res.FinalText != "" {
			if _, err := o.db.AddMessage(ctx, conv.ID, userID, channel, "assistant", res.FinalText); err != nil {
				o.log.Error("storing assistant message", "err", err)
			}
		}
		if !silent && res.SessionID != "" && conv.HarnessSessionID != res.SessionID {
			if err := o.db.SetConversationSession(ctx, conv.ID, res.SessionID); err != nil {
				o.log.Error("storing session id", "err", err)
			}
		}
	}
	if runErr != nil {
		status, errMsg = "error", runErr.Error()
	} else if res != nil && res.IsError {
		status, errMsg = "error", res.ErrMsg
	}
	if err := o.db.FinishRun(ctx, runID, status, errMsg, dur, cost); err != nil {
		o.log.Error("finishing run", "err", err)
	}
}

// UploadDir returns the directory where a user's chat attachments are saved
// (under their workspace, so the agent's tools can read them).
func (o *Orchestrator) UploadDir(userID int64) string {
	return filepath.Join(o.workspace(userID), "uploads")
}

// composeWithAttachments builds the transcript entry (names only) and the runtime
// prompt (file paths to read + inlined text) for a message with attachments.
func composeWithAttachments(msg string, atts []Attachment) (stored, prompt string) {
	if len(atts) == 0 {
		return msg, msg
	}
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		names = append(names, a.Name)
	}
	stored = strings.TrimSpace(msg + "\n\n[attached: " + strings.Join(names, ", ") + "]")

	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("\n\n--- Attached files ---\n")
	for _, a := range atts {
		switch {
		case a.Text != "":
			fmt.Fprintf(&b, "\n### %s\n%s\n", a.Name, a.Text)
		default:
			switch attachmentKind(a.Name) {
			case "image":
				fmt.Fprintf(&b, "- %s — an image at %s. View it with your Read tool to see its contents.\n", a.Name, a.Path)
			case "pdf":
				fmt.Fprintf(&b, "- %s — a PDF at %s. Read it with your tools.\n", a.Name, a.Path)
			case "video":
				fmt.Fprintf(&b, "- %s — a video at %s. You cannot watch video; do not guess its contents — tell the user you can't analyze video here (use any caption they gave).\n", a.Name, a.Path)
			case "audio":
				fmt.Fprintf(&b, "- %s — an audio file at %s. You cannot listen to audio; do not guess its contents — tell the user you can't transcribe it here (use any caption they gave).\n", a.Name, a.Path)
			default:
				fmt.Fprintf(&b, "- %s — a file at %s. Read it with your tools if it's text-like.\n", a.Name, a.Path)
			}
		}
	}
	b.WriteString("\nUse your file tools to read the listed paths when relevant. If a file can't be read (video/audio), say so plainly rather than guessing its contents.")
	return stored, b.String()
}

// auditSnippet truncates text to a short, audit-log-friendly length.
func auditSnippet(s string) string {
	s = strings.TrimSpace(s)
	const max = 80
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func (o *Orchestrator) selectRuntime(name string) runtime.AgentRuntime {
	if rt, ok := o.runtimes[name]; ok {
		return rt
	}
	if name != "" && name != o.fallback.Name() {
		o.log.Warn("runtime not available, using fallback", "requested", name, "fallback", o.fallback.Name())
	}
	return o.fallback
}

// SkillBundleDir returns the on-disk directory for a user's skill bundle
// (scripts/assets from a ZIP import), under the workspace's skills/ dir.
//
// Deliberately NOT under .claude/: the Claude Code harness guards its own
// .claude/ config tree and refuses agent Write/Edit/Bash into it, which would
// block the agent from managing a skill's own files (patching a script, writing
// credentials a script reads). Skills are injected into the prompt by path, so
// claude doesn't need them under .claude/ for discovery.
func (o *Orchestrator) SkillBundleDir(userID int64, name string) string {
	return filepath.Join(o.workspace(userID), "skills", name)
}

// workspace returns the per-user run directory (cwd for headless runs) as an
// absolute path. Absolute matters because the adapter sets the process cwd to
// this dir and also passes file paths (e.g. --append-system-prompt-file) the
// harness resolves against that cwd — a relative path would be joined twice.
func (o *Orchestrator) workspace(userID int64) string {
	root := filepath.Join(filepath.Dir(o.cfg.DBPath), "workspaces")
	p := filepath.Join(root, strconv.FormatInt(userID, 10))
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
