package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"samwise/internal/mcpserver"
	"samwise/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSessionTimeout reaps idle MCP sessions. Without it the SDK keeps every
// session FOREVER — and claude processes are often killed without sending the
// session DELETE, so a weeks-long deployment accumulated thousands of dead
// sessions until the host degraded (runs then lost their core tools).
const mcpSessionTimeout = 30 * time.Minute

// runScope is the (user, run, agent) a core-MCP bearer token is bound to, plus
// whether the run may write and the chat it came from (for "deliver here").
type runScope struct {
	userID   int64
	runID    int64
	agentID  int64
	readOnly bool
	origin   string // stored Address form; "" = web/none
}

// mcpHost serves the core MCP server in-process over a loopback HTTP endpoint,
// one logical server per run, selected by a bearer token.
//
// This is the security pivot for multi-user isolation: the core tools used to be
// a `samwise mcp --user-id N` child that claude spawned, which (a) ran under the
// agent's own uid with direct read/write of the SQLite DB, and (b) took the user
// id from a command-line flag the agent could change to read anyone's data.
// Hosting the server here means the DB is only ever touched by the trusted
// orchestrator, the user id is resolved server-side from the token (never from
// agent input), and the agent's uid never needs DB access at all.
//
// The host is SELF-HEALING: Samwise runs for weeks/months, and a listener that
// dies (fd exhaustion, accept errors) must not silently strand every later run
// without tools. The serve goroutine records liveness, and ensureAlive —
// called before each run — rebinds a fresh listener when the old one died.
type mcpHost struct {
	db  *store.DB
	log *slog.Logger

	lifeMu sync.Mutex // guards srv/addr/alive (bind/rebind/shutdown)
	srv    *http.Server
	addr   string // host:port of the CURRENT listener
	alive  bool
	closed bool // shutdown requested; stop rebinding

	mu     sync.RWMutex // guards tokens
	tokens map[string]runScope
}

func newMCPHost(db *store.DB, log *slog.Logger) *mcpHost {
	return &mcpHost{db: db, log: log, tokens: make(map[string]runScope)}
}

// start binds the first listener. Later failures are handled by ensureAlive.
func (h *mcpHost) start() error {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	return h.bindLocked()
}

// bindLocked binds a loopback listener and serves the streamable-HTTP MCP
// handler. Loopback-only: reachable from inside the container but never from
// the network, and every request must carry a live per-run token. Callers hold
// lifeMu.
func (h *mcpHost) bindLocked() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("mcp host: listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(h.getServer, &mcp.StreamableHTTPOptions{
		SessionTimeout: mcpSessionTimeout,
	}))
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Reap leaked keep-alive connections from killed claude processes —
		// without this they pile up over weeks and exhaust file descriptors.
		IdleTimeout: 5 * time.Minute,
	}
	h.srv = srv
	h.addr = ln.Addr().String()
	h.alive = true
	go func() {
		err := srv.Serve(ln)
		h.lifeMu.Lock()
		// Only mark dead if WE are still the current server (a rebind may have
		// already replaced us).
		if h.srv == srv {
			h.alive = false
		}
		h.lifeMu.Unlock()
		if err != nil && err != http.ErrServerClosed {
			h.log.Error("mcp host: serve exited — will rebind before the next run", "err", err)
		}
	}()
	h.log.Info("core mcp host listening", "addr", h.addr)
	return nil
}

// ensureAlive reports whether the host can serve, rebinding a fresh listener
// if the previous one died. Called before composing each run's mcp-config.
func (h *mcpHost) ensureAlive() bool {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	if h.closed {
		return false
	}
	if h.alive {
		return true
	}
	h.log.Warn("mcp host: listener dead, rebinding")
	if h.srv != nil {
		_ = h.srv.Close() // best-effort cleanup of the dead server
	}
	if err := h.bindLocked(); err != nil {
		h.log.Error("mcp host: rebind failed; core tools unavailable", "err", err)
		return false
	}
	return true
}

// getServer resolves the bearer token to a run scope and returns a core server
// bound to that user. An unknown or missing token yields nil, which the SDK
// turns into a 400 — so a run with no/garbage token gets no tools, and a run
// can never reach a different user's data.
func (h *mcpHost) getServer(req *http.Request) *mcp.Server {
	tok := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	if tok == "" {
		return nil
	}
	h.mu.RLock()
	scope, ok := h.tokens[tok]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	return mcpserver.NewServer(h.db, mcpserver.Binding{
		UserID: scope.userID, RunID: scope.runID, AgentID: scope.agentID,
		ReadOnly: scope.readOnly, Origin: scope.origin,
	})
}

// register issues a single-run bearer token bound to (userID, runID). The
// returned revoke func removes it; call it when the run ends so a leaked token
// can't be replayed after the fact.
func (h *mcpHost) register(scope runScope) (token string, revoke func(), err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("mcp host: token: %w", err)
	}
	token = hex.EncodeToString(buf)
	h.mu.Lock()
	h.tokens[token] = scope
	h.mu.Unlock()
	return token, func() {
		h.mu.Lock()
		delete(h.tokens, token)
		h.mu.Unlock()
	}, nil
}

// endpoint is the URL a run's mcp-config points at for the core server. Read
// under lifeMu so a concurrent rebind can't hand out a torn addr.
func (h *mcpHost) endpoint() string {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	return "http://" + h.addr + "/mcp"
}

// ready reports whether the host can serve runs, self-healing if needed.
func (h *mcpHost) ready() bool { return h.ensureAlive() }

// status reports the host's address and liveness (for the admin health panel).
func (h *mcpHost) status() (addr string, alive bool) {
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	return h.addr, h.alive
}

func (h *mcpHost) shutdown(ctx context.Context) error {
	h.lifeMu.Lock()
	h.closed = true
	srv := h.srv
	h.lifeMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
