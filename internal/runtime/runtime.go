// Package runtime is the agent-runtime adapter layer. One interface,
// AgentRuntime, hides whether a run is served by claude-headless, claude-channels,
// or codex-exec. The orchestrator assembles context and dispatches; the adapter
// executes and emits a stream of events.
//
// MVP ships claude-headless. The other adapters slot in behind the same
// interface in later steps.
package runtime

import (
	"context"
	"os/exec"
)

// ApplyIsolation makes cmd start under the given uid/gid + supplementary groups
// (Linux only; elsewhere it returns an error). Exported so the orchestrator's
// sandboxed skill_run path can reuse the same uid-drop as agent runs.
func ApplyIsolation(cmd *exec.Cmd, iso *RunIsolation) error { return applyIsolation(cmd, iso) }

// ScopedBuiltinTools is the safe default set of Claude Code built-in tools (file
// + shell) enabled when a run allows host tools, so skills with scripts/assets
// can execute. Sandboxing is at the container boundary.
var ScopedBuiltinTools = []string{"Read", "Glob", "Grep", "Bash", "Write", "Edit"}

// ReadOnlyBuiltinTools is the subset used for read-only runs (e.g. an unregistered
// member in a group): file reads only — no Bash/Write/Edit, which can mutate the
// owner's workspace or run arbitrary commands.
var ReadOnlyBuiltinTools = []string{"Read", "Glob", "Grep"}

// ExecToolList are the write/exec built-ins that run with the owner's identity
// and secrets. They are hard-locked to paired users unless the deployment sets
// ALLOW_EXEC_TOOL_OPENING (a stranger running these = a shell as the owner).
var ExecToolList = []string{"Bash", "Write", "Edit"}

var execTools = func() map[string]bool {
	m := map[string]bool{}
	for _, t := range ExecToolList {
		m[t] = true
	}
	return m
}()

// IsExecTool reports whether a tool is a hard-locked write/exec tool.
func IsExecTool(name string) bool { return execTools[name] }

// AudienceConfigurable reports whether a tool's audience may be changed at all
// (everything except the hard-locked exec/write tools).
func AudienceConfigurable(name string) bool { return !execTools[name] }

// DefaultToolAudience is a tool's audience when the user hasn't set one. Reads
// (Read/Glob/Grep) default to 'everyone' (current behavior: unregistered group
// members can already read workspace files); everything else defaults to
// 'paired'. Audience strings match store.Audience* ('everyone' | 'paired').
func DefaultToolAudience(name string) string {
	switch name {
	case "Read", "Glob", "Grep":
		return "everyone"
	default:
		return "paired"
	}
}

// OptionalTool is a Claude Code built-in a user can opt into (per tool) beyond
// the scoped default set. Danger, when set, is an extra-caution warning; Useless
// marks tools with no real effect in this embedded, non-interactive setup.
type OptionalTool struct {
	Name    string
	Desc    string
	Danger  string
	Useless bool
}

// OptionalTools is the catalog of opt-in built-ins shown as checkboxes in
// settings. Order: useful → niche → dangerous → no-effect.
var OptionalTools = []OptionalTool{
	{Name: "WebFetch", Desc: "Fetch the contents of a web page / URL.",
		Danger: "Reaches the network — can send data out and hit internal or cloud-metadata endpoints (SSRF) unless egress is restricted."},
	{Name: "WebSearch", Desc: "Search the web for current information."},
	{Name: "NotebookEdit", Desc: "Read and edit Jupyter notebooks (.ipynb). Niche."},
	{Name: "BashOutput", Desc: "Read output from a long-running background shell command."},
	{Name: "KillShell", Desc: "Terminate a background shell command."},
	{Name: "TodoWrite", Desc: "Keep an internal step-by-step checklist while working (only within a single reply)."},
	{Name: "Task", Desc: "Spawn sub-agents to work in parallel.",
		Danger: "Extra dangerous: can fan out into many agent runs (cost/latency), and sub-agents inherit the same tools and filesystem reach."},
	{Name: "ExitPlanMode", Desc: "Leave plan mode.", Useless: true},
	{Name: "SlashCommand", Desc: "Run one of Claude Code's own slash commands.", Useless: true},
}

// IsKnownBuiltinTool reports whether name is any recognized built-in (a scoped
// default or a catalog opt-in) — used to validate audience changes.
func IsKnownBuiltinTool(name string) bool {
	for _, t := range ScopedBuiltinTools {
		if t == name {
			return true
		}
	}
	return IsOptionalTool(name)
}

// IsOptionalTool reports whether name is a known opt-in tool (used to validate
// user input so only catalog tools can be enabled).
func IsOptionalTool(name string) bool {
	for _, t := range OptionalTools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// EventKind enumerates the streamed run events.
type EventKind int

const (
	// EventText is an incremental chunk of assistant-visible text.
	EventText EventKind = iota
	// EventToolCall summarizes a tool invocation (for the transcript).
	EventToolCall
	// EventError is a non-fatal error notice surfaced mid-run.
	EventError
)

// Event is a single streamed run event.
type Event struct {
	Kind EventKind
	Text string // EventText: the text chunk; EventError: the message
	Tool string // EventToolCall: a one-line summary, e.g. "created reminder id=12"
}

// ToolCall is a recorded tool invocation, persisted into the transcript so a
// rehydrated runtime knows which side effects already happened.
type ToolCall struct {
	Name    string
	Summary string
}

// Request is everything an adapter needs to execute one run.
type Request struct {
	UserID int64
	RunID  int64

	Prompt        string // the new user message / job prompt
	SystemContext string // assembled stable context (identity, profile, memory, summary)
	Transcript    string // rendered recent transcript, may be empty

	Model         string            // optional model override
	MCPConfigJSON string            // MCP config passed to the harness ("{}" disables all)
	AllowedTools  []string          // tool names pre-allowed so unattended runs never stall
	BuiltinTools  []string          // Claude Code built-in tools to enable (empty = none)
	Workspace     string            // cwd for the run (the user's mounted workspace)
	ResumeSession string            // optional native-continuity session id
	Env           map[string]string // per-user secrets injected as env vars for skill scripts

	// Isolation, when non-nil, runs the agent process (and thus its host tools)
	// as this unprivileged per-user uid/gid, so a prompt-injected or malicious
	// agent can't read another user's workspace or the database. Set only when
	// the orchestrator confirmed it can (root on Linux); nil otherwise.
	Isolation *RunIsolation
}

// RunIsolation is the per-run OS identity for the agent process.
type RunIsolation struct {
	UID    int   // run as this user id
	GID    int   // run as this group id
	Groups []int // supplementary gids (e.g. the shared claude-credentials group)
}

// Result is the outcome of a completed run. Token counts are tracked by type
// (the portable metric); CostUSD is whatever the runtime reported, kept for
// reference but not the headline.
type Result struct {
	FinalText  string
	SessionID  string
	Model      string
	DurationMS int64
	CostUSD    float64
	// Token usage by type, from the runtime's final usage report.
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64 // cache write
	CacheReadTokens     int64 // cache read
	IsError             bool
	ErrMsg              string
	ToolCalls           []ToolCall
}

// AgentRuntime is the swappable execution engine. Run streams events
// via onEvent (which may be nil) and returns the final Result.
type AgentRuntime interface {
	Name() string
	Run(ctx context.Context, req Request, onEvent func(Event)) (*Result, error)
}
