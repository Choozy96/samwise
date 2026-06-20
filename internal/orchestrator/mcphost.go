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

// runScope is the (user, run, agent) a core-MCP bearer token is bound to, plus
// whether the run may write and the Telegram chat it came from (for "deliver
// here").
type runScope struct {
	userID       int64
	runID        int64
	agentID      int64
	readOnly     bool
	originBotID  int64
	originChatID int64
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
type mcpHost struct {
	db   *store.DB
	log  *slog.Logger
	srv  *http.Server
	addr string // host:port, set once started

	mu     sync.RWMutex
	tokens map[string]runScope
}

func newMCPHost(db *store.DB, log *slog.Logger) *mcpHost {
	return &mcpHost{db: db, log: log, tokens: make(map[string]runScope)}
}

// start binds a loopback listener and serves the streamable-HTTP MCP handler.
// Loopback-only: the endpoint is reachable from inside the container but never
// from the network, and every request must carry a live per-run token.
func (h *mcpHost) start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("mcp host: listen: %w", err)
	}
	h.addr = ln.Addr().String()
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(h.getServer, nil))
	h.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := h.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			h.log.Error("mcp host: serve", "err", err)
		}
	}()
	h.log.Info("core mcp host listening", "addr", h.addr)
	return nil
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
		ReadOnly: scope.readOnly, OriginBotID: scope.originBotID, OriginChatID: scope.originChatID,
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

// endpoint is the URL a run's mcp-config points at for the core server.
func (h *mcpHost) endpoint() string { return "http://" + h.addr + "/mcp" }

// ready reports whether the host has bound its listener.
func (h *mcpHost) ready() bool { return h.addr != "" }

func (h *mcpHost) shutdown(ctx context.Context) error {
	if h.srv == nil {
		return nil
	}
	return h.srv.Shutdown(ctx)
}
