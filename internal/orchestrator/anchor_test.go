package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"samwise/internal/store"
)

// fakeSender records deliveries for precedence tests.
type fakeSender struct {
	calls []string
}

func (f *fakeSender) Send(_ context.Context, userID int64, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("send:%d", userID))
	return nil
}
func (f *fakeSender) SendAgent(_ context.Context, userID, agentID int64, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("agent:%d:%d", userID, agentID))
	return nil
}
func (f *fakeSender) SendBot(_ context.Context, userID, botID int64, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("bot:%d:%d", userID, botID))
	return nil
}
func (f *fakeSender) SendToChat(_ context.Context, userID, botID int64, chatID, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("chat:%d:%d:%s", userID, botID, chatID))
	return nil
}
func (f *fakeSender) SendFile(_ context.Context, userID int64, _ string, _ []byte, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("file:%d", userID))
	return nil
}
func (f *fakeSender) SendFileToChat(_ context.Context, userID, botID int64, chatID, _ string, _ []byte, _ string) error {
	f.calls = append(f.calls, fmt.Sprintf("filechat:%d:%d:%s", userID, botID, chatID))
	return nil
}

// TestAnchorChatSetting: the system setting round-trips and clears.
func TestAnchorChatSetting(t *testing.T) {
	o, ctx := newAnchorOrch(t)
	if got := o.AnchorChat(ctx); got != "" {
		t.Errorf("unset anchor = %q", got)
	}
	if err := o.db.SetSystemSetting(ctx, store.AnchorChatKey, "tg:1:-100200"); err != nil {
		t.Fatal(err)
	}
	if got := o.AnchorChat(ctx); got != "tg:1:-100200" {
		t.Errorf("anchor = %q", got)
	}
	if err := o.db.SetSystemSetting(ctx, store.AnchorChatKey, ""); err != nil {
		t.Fatal(err)
	}
	if got := o.AnchorChat(ctx); got != "" {
		t.Errorf("cleared anchor = %q", got)
	}
}

func newAnchorOrch(t *testing.T) (*Orchestrator, context.Context) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return &Orchestrator{db: db}, context.Background()
}

// TestDeliverRunResultAnchorPrecedence pins the default-branch order:
// job delivery > user's specific default chat > anchor > coarse channel pref.
func TestDeliverRunResultAnchorPrecedence(t *testing.T) {
	o, ctx := newAnchorOrch(t)
	uid, err := o.db.CreateUser(ctx, "alice", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSender{}
	o.RegisterSender("telegram", f)

	// 1) Explicit job delivery always wins, anchor or not.
	_ = o.db.SetSystemSetting(ctx, store.AnchorChatKey, "tg:9:-900")
	if err := o.DeliverRunResult(ctx, uid, "", "tg:5:-500", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != fmt.Sprintf("chat:%d:5:-500", uid) {
		t.Fatalf("job delivery should win: %v", f.calls)
	}

	// 2) No job delivery, user has a SPECIFIC default chat → that wins over anchor.
	st, _ := o.db.GetSettings(ctx, uid)
	st.DeliveryChannel = "tg:7:-700"
	if err := o.db.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := o.DeliverRunResult(ctx, uid, "", "", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != fmt.Sprintf("chat:%d:7:-700", uid) {
		t.Fatalf("specific user default should beat anchor: %v", f.calls)
	}

	// 3) Coarse channel pref ("web") + anchor set → anchor wins.
	st.DeliveryChannel = "web"
	if err := o.db.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := o.DeliverRunResult(ctx, uid, "", "", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || f.calls[0] != fmt.Sprintf("chat:%d:9:-900", uid) {
		t.Fatalf("anchor should beat coarse channel pref: %v", f.calls)
	}

	// 4) Anchor cleared → back to the user's channel pref (web = no telegram call).
	_ = o.db.SetSystemSetting(ctx, store.AnchorChatKey, "")
	f.calls = nil
	if err := o.DeliverRunResult(ctx, uid, "", "", "hi"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("web pref should not hit telegram: %v", f.calls)
	}
}
