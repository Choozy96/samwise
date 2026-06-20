package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"samwise/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearerRT injects an Authorization header, the way claude's mcp-config does for
// an http-type server.
type bearerRT struct {
	token string
	base  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(r)
}

func dialMCP(t *testing.T, endpoint, token string) (*mcp.ClientSession, error) {
	t.Helper()
	hc := &http.Client{Transport: bearerRT{token: token, base: http.DefaultTransport}}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           hc,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
}

func newTestHost(t *testing.T) (*mcpHost, *store.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	h := newMCPHost(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.shutdown(ctx) })
	return h, db, ctx
}

func hasMemory(t *testing.T, db *store.DB, ctx context.Context, userID int64, content string) bool {
	t.Helper()
	rows, err := db.ListSemantic(ctx, userID, store.AllAgents, 100)
	if err != nil {
		t.Fatalf("ListSemantic: %v", err)
	}
	for _, r := range rows {
		if r.Content == content {
			return true
		}
	}
	return false
}

func save(t *testing.T, sess *mcp.ClientSession, ctx context.Context, content string) {
	t.Helper()
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memory_save",
		Arguments: map[string]any{"content": content, "topic": "t", "kind": "fact"},
	}); err != nil {
		t.Fatalf("memory_save: %v", err)
	}
}

// TestMCPHostTokenScoping is the core multi-user isolation guarantee: the user a
// run can touch is fixed by its bearer token server-side, so one user's run can
// never read or write another user's data — there is no --user-id to spoof.
func TestMCPHostTokenScoping(t *testing.T) {
	h, db, ctx := newTestHost(t)
	u1, err := db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := db.CreateUser(ctx, "bob", "h", false)
	if err != nil {
		t.Fatal(err)
	}

	tok1, revoke1, err := h.register(runScope{userID: u1})
	if err != nil {
		t.Fatal(err)
	}
	tok2, _, err := h.register(runScope{userID: u2})
	if err != nil {
		t.Fatal(err)
	}

	// Each user saves through their own token-scoped session.
	s1, err := dialMCP(t, h.endpoint(), tok1)
	if err != nil {
		t.Fatalf("connect u1: %v", err)
	}
	save(t, s1, ctx, "alpha-secret")
	_ = s1.Close()

	s2, err := dialMCP(t, h.endpoint(), tok2)
	if err != nil {
		t.Fatalf("connect u2: %v", err)
	}
	save(t, s2, ctx, "beta-secret")
	_ = s2.Close()

	// The token's user got the write; the other user did not — both directions.
	// (Assert via ListSemantic with exact-content equality, not FTS search, so
	// shared word-tokens can't produce a misleading match.)
	if !hasMemory(t, db, ctx, u1, "alpha-secret") {
		t.Error("user1 should have their own saved memory")
	}
	if hasMemory(t, db, ctx, u2, "alpha-secret") {
		t.Error("CROSS-USER LEAK: user2 must not have user1's memory")
	}
	if hasMemory(t, db, ctx, u1, "beta-secret") {
		t.Error("CROSS-USER LEAK: user1 must not have user2's memory")
	}
	if !hasMemory(t, db, ctx, u2, "beta-secret") {
		t.Error("user2 should have their own saved memory")
	}

	// A revoked token can't be replayed.
	revoke1()
	if _, err := dialMCP(t, h.endpoint(), tok1); err == nil {
		t.Error("revoked token should be rejected")
	}
	_ = tok2
}

// TestMCPHostRejectsBadToken confirms a run with no token, or a guessed/garbage
// token, gets no server at all (so no tools, no data).
func TestMCPHostRejectsBadToken(t *testing.T) {
	h, _, _ := newTestHost(t)
	for _, tok := range []string{"", "deadbeef-not-a-real-token"} {
		if _, err := dialMCP(t, h.endpoint(), tok); err == nil {
			t.Errorf("token %q should be rejected", tok)
		}
	}
}
