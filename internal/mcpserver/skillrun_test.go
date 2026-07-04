package mcpserver

import (
	"context"
	"strings"
	"testing"
)

func TestSkillRunTool(t *testing.T) {
	h, ctx := newJobHandlers(t)

	// No executor wired → reports unavailable.
	SetSkillExecutor(nil)
	r, _, _ := h.skillRun(ctx, nil, skillRunIn{Name: "x"})
	if s := resultText(r); !strings.Contains(s, "isn't available") {
		t.Errorf("nil executor should report unavailable: %q", s)
	}

	// Stub executor: confirm name/input/readOnly are passed through and output relayed.
	var gotName, gotInput string
	var gotRO bool
	SetSkillExecutor(func(_ context.Context, _ int64, name, input string, readOnly bool) (string, error) {
		gotName, gotInput, gotRO = name, input, readOnly
		return "RESULT:" + input, nil
	})
	t.Cleanup(func() { SetSkillExecutor(nil) })

	h.readOnly = true
	r, _, _ = h.skillRun(ctx, nil, skillRunIn{Name: "bus", Input: "12345"})
	if s := resultText(r); s != "RESULT:12345" {
		t.Errorf("output not relayed: %q", s)
	}
	if gotName != "bus" || gotInput != "12345" || !gotRO {
		t.Errorf("args not threaded: name=%q input=%q readOnly=%v", gotName, gotInput, gotRO)
	}

	// Executor error surfaces as a failure.
	SetSkillExecutor(func(_ context.Context, _ int64, _, _ string, _ bool) (string, error) {
		return "", context.DeadlineExceeded
	})
	r, _, _ = h.skillRun(ctx, nil, skillRunIn{Name: "slow"})
	if s := resultText(r); !strings.Contains(strings.ToLower(s), "deadline") {
		t.Errorf("executor error should surface: %q", s)
	}
}
