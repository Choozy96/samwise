// Package orchestrator owns everything around an agent run: context
// assembly, run dispatch, persistence of the transcript and run record, and
// (for scheduled jobs) delivery. The runtime is a swappable execution engine
// selected per user.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"samwise/internal/config"
	"samwise/internal/runtime"
	"samwise/internal/secretbox"
	"samwise/internal/store"
)

// coreTools are the core MCP tool names (server "core"), pre-allowed on every
// run so unattended runs never stall on a permission prompt.
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
	exePath  string        // this binary (retained for out-of-process tooling)
	dbAbs    string        // absolute DB path (retained for out-of-process tooling)
	mcp       *mcpHost      // in-process, token-scoped core MCP server
	isolate   bool          // run each agent as a per-user uid (resolved in Start)
	claudeDir string        // the shared claude config dir (source of the credential)
	telegram  ChannelSender // optional Telegram delivery sink (MVP step 6)
	// credMu serializes writes to the one shared claude.ai credential file. Every
	// user's run links to the same canonical .credentials.json, so concurrent runs
	// reconciling a refreshed token would otherwise tear that file (truncate +
	// interleaved write) and break auth for everyone.
	credMu sync.Mutex
}

// New constructs an Orchestrator. The fallback runtime is used when a user's
// configured runtime is not registered (e.g. channels/codex not built yet).
func New(cfg *config.Config, db *store.DB, log *slog.Logger, box *secretbox.Box, runtimes ...runtime.AgentRuntime) *Orchestrator {
	m := make(map[string]runtime.AgentRuntime, len(runtimes))
	for _, rt := range runtimes {
		m[rt.Name()] = rt
	}
	o := &Orchestrator{cfg: cfg, db: db, log: log, box: box, runtimes: m, mcp: newMCPHost(db, log)}
	if rt, ok := m["claude-headless"]; ok {
		o.fallback = rt
	} else if len(runtimes) > 0 {
		o.fallback = runtimes[0]
	}
	if exe, err := os.Executable(); err == nil {
		o.exePath = exe
	}
	o.claudeDir = os.Getenv("CLAUDE_CONFIG_DIR")
	if o.claudeDir == "" {
		o.claudeDir = "/home/app/.claude"
	}
	if abs, err := filepath.Abs(cfg.DBPath); err == nil {
		o.dbAbs = abs
	} else {
		o.dbAbs = cfg.DBPath
	}
	return o
}

// Start brings up the in-process core MCP host. It must be called before
// dispatching runs; serve fails loudly if the loopback listener can't bind,
// rather than silently falling back to a less-isolated path.
//
// It also resolves whether per-user uid isolation can run: it needs root on
// Linux (the container). If isolation was requested but we're not root/Linux
// (e.g. native dev), it's disabled with a loud warning rather than failing.
func (o *Orchestrator) Start() error {
	if o.cfg.AgentIsolation {
		if os.Geteuid() == 0 {
			o.isolate = true
			// New files the orchestrator creates (DB sidecars, workspace files,
			// backups) default to owner-only, so nothing the agent uid shouldn't
			// see is born world-readable.
			setRestrictiveUmask()
			if err := o.prepareDataPerms(); err != nil {
				o.log.Error("isolation: preparing /data perms", "err", err)
			}
			o.log.Info("agent isolation enabled", "uid_base", o.cfg.AgentUIDBase, "cred_gid", o.cfg.AgentCredGID)
		} else {
			o.log.Warn("AGENT_ISOLATION requested but process is not root (or not Linux); " +
				"agent host tools will NOT be uid-isolated between users")
		}
	}
	return o.mcp.start()
}

// Isolated reports whether per-user uid isolation is active.
func (o *Orchestrator) Isolated() bool { return o.isolate }

// runIsolation is the per-user OS identity for a run: uid/gid base+userID, with
// the shared credentials gid so the agent can still read the claude.ai auth dir.
func (o *Orchestrator) runIsolation(userID int64) *runtime.RunIsolation {
	id := o.cfg.AgentUIDBase + int(userID)
	return &runtime.RunIsolation{UID: id, GID: id, Groups: []int{o.cfg.AgentCredGID}}
}

// Close shuts the core MCP host down.
func (o *Orchestrator) Close(ctx context.Context) error { return o.mcp.shutdown(ctx) }

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
	// Isolated runs in a separate per-(user,agent) "task" thread with NO chat
	// transcript (memory is still loaded). Used by scheduled agent_run jobs so a
	// task never reads or pollutes the interactive conversation, and a task firing
	// at the same time as a message can't merge with it.
	Isolated bool
	// ReadOnly forbids write operations for this turn: the core MCP write tools
	// refuse, and only read built-in tools are enabled. Set for messages from
	// unregistered senders in a group (they can chat/read but not mutate the
	// owner's memory, jobs, or workspace).
	ReadOnly bool
	// OriginBotID/OriginChatID identify the Telegram bot+chat this turn came from
	// (0 for web), so a job created this turn can target "here" for delivery.
	OriginBotID  int64
	OriginChatID int64
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
	var conv *store.Conversation
	if req.Isolated {
		conv, err = o.db.GetOrCreateTaskConversation(ctx, req.User.ID, agent.ID)
	} else {
		conv, err = o.db.GetOrCreateConversation(ctx, req.User.ID, channel, agent.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("resolving conversation: %w", err)
	}

	// Assemble from the transcript as it stands BEFORE this turn's message — the
	// new message is sent as the prompt, not duplicated into the transcript.
	// The incoming message drives memory retrieval.
	asm, err := o.assemble(ctx, req.User, settings, agent, conv, req.UserMessage)
	if err != nil {
		return nil, fmt.Errorf("assembling context: %w", err)
	}
	// Isolated (scheduled task) runs are stateless: memory is loaded, but the chat
	// transcript is never read — so the conversation can't bleed into a task.
	if req.Isolated {
		asm.transcript = ""
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

	mcpScope := runScope{
		userID: req.User.ID, runID: runID, agentID: agent.ID, readOnly: req.ReadOnly,
		originBotID: req.OriginBotID, originChatID: req.OriginChatID,
	}
	mcpJSON, allowedTools, releaseMCP := o.buildMCP(ctx, mcpScope)
	defer releaseMCP()

	// When isolation is active, run the agent as the user's per-user uid and make
	// sure their workspace tree is owned by it first.
	var iso *runtime.RunIsolation
	runEnv := o.userSecretsEnv(ctx, req.User.ID)
	if o.isolate {
		iso = o.runIsolation(req.User.ID)
		if err := o.ensureWorkspaceOwner(req.User.ID, iso); err != nil {
			return nil, fmt.Errorf("preparing isolated workspace: %w", err)
		}
		if err := o.setupRunClaudeDir(req.User.ID, iso); err != nil {
			o.log.Error("preparing per-user claude config dir", "user_id", req.User.ID, "err", err)
		}
		// Each uid gets its own claude config dir + HOME inside its 0700 workspace,
		// so claude's transcripts/state stay private to that user.
		if runEnv == nil {
			runEnv = map[string]string{}
		}
		ws := o.workspace(req.User.ID)
		runEnv["HOME"] = ws
		runEnv["CLAUDE_CONFIG_DIR"] = filepath.Join(ws, ".claude")
	}

	rreq := runtime.Request{
		UserID:        req.User.ID,
		RunID:         runID,
		Prompt:        runtimePrompt,
		SystemContext: asm.systemContext,
		Transcript:    asm.transcript,
		Model:         model,
		Workspace:     o.workspace(req.User.ID),
		MCPConfigJSON: mcpJSON,
		AllowedTools:  allowedTools,
		BuiltinTools:  o.builtinTools(settings, req.ReadOnly),
		Env:           runEnv,
		Isolation:     iso,
	}

	res, runErr := rt.Run(ctx, rreq, onEvent)

	// Persist outcome regardless of error so the transcript and runs table stay
	// truthful.
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
	var metrics store.RunMetrics
	if res != nil {
		metrics = store.RunMetrics{
			DurationMS:          res.DurationMS,
			CostUSD:             res.CostUSD,
			InputTokens:         res.InputTokens,
			OutputTokens:        res.OutputTokens,
			CacheCreationTokens: res.CacheCreationTokens,
			CacheReadTokens:     res.CacheReadTokens,
		}
		// Tool-call summaries go into the transcript so a rehydrated
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
	if err := o.db.FinishRun(ctx, runID, status, errMsg, metrics); err != nil {
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

// builtinTools decides which Claude Code built-in tools a run gets. The
// deployment's ALLOW_AGENT_TOOLS is the master switch (off => no host tools at
// all); on top of that the user's settings opt into individual extra tools (each
// validated against the catalog, so only known tools can ever be enabled).
func (o *Orchestrator) builtinTools(s *store.Settings, readOnly bool) []string {
	if !o.cfg.AllowAgentTools {
		return nil
	}
	if readOnly {
		// No write-capable tools and no opt-in extras for an unregistered sender.
		return append([]string{}, runtime.ReadOnlyBuiltinTools...)
	}
	tools := append([]string{}, runtime.ScopedBuiltinTools...)
	for _, name := range strings.Split(s.ExtraTools, ",") {
		if name = strings.TrimSpace(name); name != "" && runtime.IsOptionalTool(name) {
			tools = append(tools, name)
		}
	}
	return tools
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

// prepareDataPerms locks down the data directory for uid isolation: the DB is
// readable only by root (the orchestrator), the data dirs are traverse-only
// (0711, so an agent can reach its own workspace by exact path but can't list
// siblings), and every existing per-user workspace is made private to its run
// uid. Runs once at startup when isolation is active.
func (o *Orchestrator) prepareDataPerms() error {
	dataDir := filepath.Dir(o.dbAbs)
	wsRoot := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(wsRoot, 0o711); err != nil {
		return err
	}
	// Lock the DB and its WAL/SHM sidecars to root-only: SQLite creates the
	// sidecars 0644 by default, so an agent could otherwise read recent
	// transactions straight out of app.db-wal even with app.db itself at 0600.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := o.dbAbs + suffix
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Chown(p, 0, 0); err != nil {
			return fmt.Errorf("chown %s: %w", p, err)
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	for _, d := range []string{dataDir, wsRoot} {
		if err := os.Chmod(d, 0o711); err != nil {
			return fmt.Errorf("chmod %s: %w", d, err)
		}
	}
	entries, err := os.ReadDir(wsRoot)
	if err != nil {
		return err
	}
	for _, e := range entries {
		uid, err := strconv.ParseInt(e.Name(), 10, 64)
		if !e.IsDir() || err != nil {
			continue // not a numeric per-user workspace
		}
		if err := o.ensureWorkspaceOwner(uid, o.runIsolation(uid)); err != nil {
			o.log.Error("isolation: chown existing workspace", "user_id", uid, "err", err)
		}
	}
	return nil
}

// ensureWorkspaceOwner makes a user's workspace tree owned by, and private to,
// their run uid (0700 at the root). Called before each isolated run so files the
// orchestrator wrote as root (uploads, skill mirrors) are readable by the agent.
func (o *Orchestrator) ensureWorkspaceOwner(userID int64, iso *runtime.RunIsolation) error {
	ws := o.workspace(userID)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return err
	}
	if err := filepath.WalkDir(ws, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Lchown, not Chown: the per-user .claude dir contains a symlink to the
		// SHARED credential — following it would chown the canonical credential to
		// this run's uid and break it for everyone else.
		return os.Lchown(p, iso.UID, iso.GID)
	}); err != nil {
		return err
	}
	return os.Chmod(ws, 0o700)
}

// setupRunClaudeDir gives a run its own claude config dir inside the user's 0700
// workspace, so claude's per-run state (transcripts, .claude.json) is private to
// that uid instead of shared and cross-readable. Only the claude.ai credential is
// shared (one subscription, by design) — symlinked in from the canonical dir.
// If a prior run's token refresh replaced that symlink with a real file, its
// contents are first written back to the canonical, then the symlink restored —
// so refreshes still propagate to the one shared credential.
func (o *Orchestrator) setupRunClaudeDir(userID int64, iso *runtime.RunIsolation) error {
	dir := filepath.Join(o.workspace(userID), ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chown(dir, iso.UID, iso.GID); err != nil {
		return err
	}
	cred := filepath.Join(o.claudeDir, ".credentials.json")
	if _, err := os.Stat(cred); err != nil {
		return nil // no shared credential yet (e.g. claude not authed) — nothing to link
	}
	link := filepath.Join(dir, ".credentials.json")

	// Serialize the whole reconcile: the canonical credential is shared by every
	// user's run, so two runs reconciling at once (e.g. two cron jobs firing
	// together) must not write it concurrently.
	o.credMu.Lock()
	defer o.credMu.Unlock()

	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil // already linked
		}
		// A refresh clobbered the symlink with a real file: persist the new token
		// back to the shared credential, then relink. Only write back a COMPLETE,
		// valid token — a partial read (claude mid-write) or empty file must never
		// overwrite the canonical, or it breaks auth for every user.
		if data, rerr := os.ReadFile(link); rerr == nil {
			if len(data) > 0 && json.Valid(data) {
				if werr := os.WriteFile(cred, data, 0o640); werr != nil {
					o.log.Error("syncing refreshed credential to shared file", "err", werr)
				}
			} else {
				o.log.Warn("skipping credential writeback: refreshed file not valid JSON",
					"user_id", userID, "bytes", len(data))
			}
		}
		_ = os.Remove(link)
	}
	if err := os.Symlink(cred, link); err != nil {
		return err
	}
	_ = os.Lchown(link, iso.UID, iso.GID)
	return nil
}
