// Package runtime is the agent-runtime adapter layer (spec §5). One interface,
// AgentRuntime, hides whether a run is served by claude-headless, claude-channels,
// or codex-exec. The orchestrator assembles context and dispatches; the adapter
// executes and emits a stream of events.
//
// MVP ships claude-headless. The other adapters slot in behind the same
// interface in later steps.
package runtime

import "context"

// ScopedBuiltinTools is the set of Claude Code built-in tools enabled when a run
// allows host tools (so skills with scripts/assets can execute). Sandboxing is
// at the container boundary (spec §5.2). Deliberately excludes browser/computer
// and other tools the assistant doesn't need.
var ScopedBuiltinTools = []string{"Read", "Glob", "Grep", "Bash", "Write", "Edit"}

// EventKind enumerates the streamed run events (spec §5.1 RunEvent).
type EventKind int

const (
	// EventText is an incremental chunk of assistant-visible text.
	EventText EventKind = iota
	// EventToolCall summarizes a tool invocation (for the transcript, spec §5.5).
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

// Request is everything an adapter needs to execute one run (spec §5.1).
type Request struct {
	UserID int64
	RunID  int64

	Prompt        string // the new user message / job prompt
	SystemContext string // assembled stable context (identity, profile, memory, summary)
	Transcript    string // rendered recent transcript, may be empty

	Model          string            // optional model override
	MCPConfigJSON  string            // MCP config passed to the harness ("{}" disables all)
	AllowedTools   []string          // tool names pre-allowed so unattended runs never stall
	AllowHostTools bool              // enable ScopedBuiltinTools (Read/Bash/…) for script-bearing skills
	Workspace      string            // cwd for the run (the user's mounted workspace)
	ResumeSession  string            // optional native-continuity session id (spec §5.4)
	Env            map[string]string // per-user secrets injected as env vars for skill scripts
}

// Result is the outcome of a completed run.
type Result struct {
	FinalText  string
	SessionID  string
	Model      string
	DurationMS int64
	CostUSD    float64
	IsError    bool
	ErrMsg     string
	ToolCalls  []ToolCall
}

// AgentRuntime is the swappable execution engine (spec §5.1). Run streams events
// via onEvent (which may be nil) and returns the final Result.
type AgentRuntime interface {
	Name() string
	Run(ctx context.Context, req Request, onEvent func(Event)) (*Result, error)
}
