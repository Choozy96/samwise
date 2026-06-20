// Package mcpserver implements the core MCP server: the tools the
// assistant uses to read/write memory and settings. It runs as the `mcp`
// subcommand over stdio, spawned by a runtime adapter with the user_id/run_id
// the orchestrator chose — so every tool call is bound to a run context and the
// model never selects whose data it touches.
//
// IMPORTANT: stdout carries the MCP protocol. All diagnostics go to stderr.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"samwise/internal/schedule"
	"samwise/internal/store"
)

// Config binds the server to a run context.
type Config struct {
	DBPath  string
	UserID  int64
	RunID   int64
	AgentID int64
}

// NewServer builds an in-process core MCP server bound to a single run context
// (userID/runID) over the orchestrator's already-open DB. The user id is fixed
// here by the trusted caller — never taken from agent input — so a run can only
// ever touch its own user's data. This is how the orchestrator hosts the core
// tools itself (token-scoped HTTP) instead of spawning a per-run child that
// would run under the agent's uid with direct DB access.
// Binding is the per-run context a core server is bound to.
type Binding struct {
	UserID, RunID, AgentID int64
	ReadOnly               bool
	// OriginBotID/OriginChatID are the Telegram bot+chat the run came from (0 =
	// web/none), so job_create can resolve a "here" delivery destination.
	OriginBotID, OriginChatID int64
}

func NewServer(db *store.DB, b Binding) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "core", Version: "0.1.0"}, nil)
	(&handlers{
		db: db, userID: b.UserID, runID: b.RunID, agentID: b.AgentID, readOnly: b.ReadOnly,
		originBotID: b.OriginBotID, originChatID: b.OriginChatID,
	}).register(srv)
	return srv
}

// Run opens the shared DB and serves the core tools over stdio until the client
// disconnects or ctx is cancelled. Migrations are owned by the orchestrator, so
// this only opens the existing database. Retained for out-of-process/debug use;
// the orchestrator now hosts the core server in-process via NewServer.
func Run(ctx context.Context, cfg Config) error {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("mcp: open db: %w", err)
	}
	defer db.Close()

	return NewServer(db, Binding{UserID: cfg.UserID, RunID: cfg.RunID, AgentID: cfg.AgentID}).Run(ctx, &mcp.StdioTransport{})
}

type handlers struct {
	db           *store.DB
	userID       int64
	runID        int64
	agentID      int64 // the running agent; agent-scoped memory is tagged with it
	readOnly     bool  // write tools refuse when set (unregistered group sender)
	originBotID  int64 // telegram bot+chat the run came from (0 = web/none), for
	originChatID int64 // resolving a job's "here" delivery destination
}

// denyWrite refuses a write tool for a read-only run with a clear, relayable
// message, and audits the denial.
func (h *handlers) denyWrite(tool string) *mcp.CallToolResult {
	h.audit(tool, "read-only run", "denied")
	return textResult("Not permitted: in this group chat, only members whose Telegram account is registered with the assistant can change memory, jobs, or settings. (You can still read and chat.)")
}

// ── tool argument types (schemas inferred from these structs) ────────────────

type memorySaveIn struct {
	Content string `json:"content" jsonschema:"the durable fact, preference, or event to remember"`
	Topic   string `json:"topic" jsonschema:"a short topic label, e.g. work, health, family"`
	Kind    string `json:"kind" jsonschema:"one of: fact, preference, event"`
	Scope   string `json:"scope,omitempty" jsonschema:"'user' (default) for facts about the user that all of your personas should share; 'agent' for facts specific to this persona/role only"`
}

type memorySearchIn struct {
	Query  string `json:"query" jsonschema:"free-text search over the user's memory"`
	Topic  string `json:"topic,omitempty" jsonschema:"optional exact topic filter"`
	After  string `json:"after,omitempty" jsonschema:"optional lower bound date YYYY-MM-DD"`
	Before string `json:"before,omitempty" jsonschema:"optional upper bound date YYYY-MM-DD"`
}

type memoryForgetIn struct {
	ID    int64  `json:"id" jsonschema:"the id of the memory row to delete (from memory_search)"`
	Layer string `json:"layer,omitempty" jsonschema:"which layer the id is in: 'semantic' (a fact/preference) or 'episodic' (a dated daily note); omit to try both"`
}

type setTimezoneIn struct {
	Timezone string `json:"timezone" jsonschema:"IANA timezone name, e.g. Asia/Singapore"`
}

type emptyIn struct{}

func (h *handlers) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "memory_save",
		Description: "Save a durable fact, preference, or event to long-term memory. Use when you learn something worth remembering across conversations.",
	}, h.memorySave)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "memory_search",
		Description: "Search the user's long-term memory (facts, preferences, events, and dated daily summaries). Returns the most relevant entries.",
	}, h.memorySearch)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "memory_forget",
		Description: "Delete a memory entry by id (a fact/preference or a dated daily note) when the user says it's wrong or no longer true. Use the id and layer shown by memory_search.",
	}, h.memoryForget)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "memory_list_topics",
		Description: "List the distinct topics present in the user's memory.",
	}, h.memoryListTopics)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_timezone",
		Description: "Set the user's timezone (IANA name). Use when the user says where they are, e.g. 'I've landed in London'.",
	}, h.setTimezone)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_settings",
		Description: "Get the user's current settings (timezone, delivery channel, active runtime, schedule times).",
	}, h.getSettings)

	h.registerReminders(s)
	h.registerJobs(s)
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *handlers) memorySave(ctx context.Context, _ *mcp.CallToolRequest, in memorySaveIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("memory_save"), nil, nil
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return h.fail("memory_save", "topic="+in.Topic, "content is required"), nil, nil
	}
	kind := normalizeKind(in.Kind)
	// Default to user-scope (shared across the user's agents); "agent" tags it to
	// the running agent so only this persona sees it.
	scope := int64(0)
	scopeLabel := "user"
	if strings.EqualFold(strings.TrimSpace(in.Scope), "agent") {
		scope = h.agentID
		scopeLabel = "agent"
	}
	id, err := h.db.SaveSemantic(ctx, h.userID, scope, strings.TrimSpace(in.Topic), kind, content, "assistant")
	if err != nil {
		return h.fail("memory_save", "topic="+in.Topic, err.Error()), nil, nil
	}
	h.audit("memory_save", fmt.Sprintf("kind=%s topic=%s scope=%s", kind, in.Topic, scopeLabel), "ok")
	return textResult(fmt.Sprintf("Saved %s memory id=%d (%s).", scopeLabel, id, kind)), nil, nil
}

func (h *handlers) memorySearch(ctx context.Context, _ *mcp.CallToolRequest, in memorySearchIn) (*mcp.CallToolResult, any, error) {
	k := h.retrievalK(ctx)
	hits, err := h.db.SearchMemory(ctx, h.userID, h.agentID, in.Query, strings.TrimSpace(in.Topic), in.After, in.Before, k)
	if err != nil {
		return h.fail("memory_search", "query~"+snippet(in.Query), err.Error()), nil, nil
	}
	h.audit("memory_search", fmt.Sprintf("query~%s hits=%d", snippet(in.Query), len(hits)), "ok")
	if len(hits) == 0 {
		return textResult("No matching memories."), nil, nil
	}
	var b strings.Builder
	for _, hit := range hits {
		if hit.Layer == "episodic" {
			fmt.Fprintf(&b, "- (id=%d, layer=episodic, %s) %s\n", hit.RefID, hit.TS, hit.Content)
		} else {
			fmt.Fprintf(&b, "- (id=%d, layer=semantic, %s, topic=%s) %s\n", hit.RefID, hit.Kind, hit.Topic, hit.Content)
		}
	}
	return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
}

func (h *handlers) memoryForget(ctx context.Context, _ *mcp.CallToolRequest, in memoryForgetIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("memory_forget"), nil, nil
	}
	// Semantic and episodic ids live in separate tables. Honor an explicit layer;
	// if none is given, try semantic first, then episodic.
	layer := strings.ToLower(strings.TrimSpace(in.Layer))
	var ok bool
	var err error
	switch layer {
	case "episodic", "daily", "day":
		ok, err = h.db.ForgetEpisodic(ctx, h.userID, in.ID)
		layer = "episodic"
	case "semantic", "fact", "preference", "event":
		ok, err = h.db.ForgetSemantic(ctx, h.userID, h.agentID, in.ID)
		layer = "semantic"
	default:
		if ok, err = h.db.ForgetSemantic(ctx, h.userID, h.agentID, in.ID); err == nil && !ok {
			ok, err = h.db.ForgetEpisodic(ctx, h.userID, in.ID)
		}
		layer = "memory"
	}
	if err != nil {
		return h.fail("memory_forget", fmt.Sprintf("id=%d", in.ID), err.Error()), nil, nil
	}
	status := "ok"
	if !ok {
		status = "not_found"
	}
	h.audit("memory_forget", fmt.Sprintf("id=%d layer=%s", in.ID, layer), status)
	if !ok {
		return textResult(fmt.Sprintf("No %s entry with id=%d.", layer, in.ID)), nil, nil
	}
	return textResult(fmt.Sprintf("Deleted memory id=%d.", in.ID)), nil, nil
}

func (h *handlers) memoryListTopics(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	topics, err := h.db.ListTopics(ctx, h.userID, h.agentID)
	if err != nil {
		return h.fail("memory_list_topics", "", err.Error()), nil, nil
	}
	h.audit("memory_list_topics", fmt.Sprintf("n=%d", len(topics)), "ok")
	if len(topics) == 0 {
		return textResult("No topics yet."), nil, nil
	}
	return textResult(strings.Join(topics, ", ")), nil, nil
}

func (h *handlers) setTimezone(ctx context.Context, _ *mcp.CallToolRequest, in setTimezoneIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("set_timezone"), nil, nil
	}
	tz := strings.TrimSpace(in.Timezone)
	if _, err := time.LoadLocation(tz); err != nil {
		return h.fail("set_timezone", "tz="+tz, "unknown timezone"), nil, nil
	}
	if err := h.db.UpdateTimezone(ctx, h.userID, tz); err != nil {
		return h.fail("set_timezone", "tz="+tz, err.Error()), nil, nil
	}
	// A timezone change recomputes the user's user_local jobs.
	if err := schedule.RecomputeUserLocal(ctx, h.db, h.userID, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: recompute jobs after tz change: %v\n", err)
	}
	h.audit("set_timezone", "tz="+tz, "ok")
	return textResult("Timezone set to " + tz + ". Your time-based reminders now follow " + tz + "."), nil, nil
}

func (h *handlers) getSettings(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, any, error) {
	s, err := h.db.GetSettings(ctx, h.userID)
	if err != nil {
		return h.fail("get_settings", "", err.Error()), nil, nil
	}
	h.audit("get_settings", "", "ok")
	out := map[string]any{
		"timezone":          s.Timezone,
		"local_time":        localNow(s.Timezone),
		"delivery_channel":  s.DeliveryChannel,
		"active_runtime":    s.ActiveRuntime,
		"distillation_time": s.DistillationTime,
	}
	js, _ := json.MarshalIndent(out, "", "  ")
	return textResult(string(js)), nil, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *handlers) retrievalK(ctx context.Context) int {
	if s, err := h.db.GetSettings(ctx, h.userID); err == nil && s.RetrievalK > 0 {
		return s.RetrievalK
	}
	return 8
}

func (h *handlers) audit(tool, summary, status string) {
	if err := h.db.AddAudit(context.Background(), h.userID, h.runID, tool, summary, status); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: audit write failed: %v\n", err)
	}
}

func (h *handlers) fail(tool, summary, msg string) *mcp.CallToolResult {
	h.audit(tool, summary, "error")
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "error: " + msg}},
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "preference", "pref":
		return "preference"
	case "event":
		return "event"
	default:
		return "fact"
	}
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

func localNow(tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format("Mon 2006-01-02 15:04 MST")
}
