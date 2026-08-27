package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	slackapi "github.com/slack-go/slack"
)

func testClient(srv *httptest.Server) *Client {
	return NewClient("xoxb-test",
		slackapi.OptionAPIURL(srv.URL+"/api/"),
		slackapi.OptionHTTPClient(srv.Client()))
}

// TestAuthTest: the wrapper surfaces bot user id + team name.
func TestAuthTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.test" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "user_id": "U0BOT", "team": "Acme", "url": "https://acme.slack.com/",
		})
	}))
	defer srv.Close()
	uid, team, err := testClient(srv).AuthTest(context.Background())
	if err != nil || uid != "U0BOT" || team != "Acme" {
		t.Fatalf("got (%q,%q,%v)", uid, team, err)
	}
}

// TestDeliverConvertsAndThreads: deliver converts markdown to mrkdwn and passes
// the thread ts through.
func TestDeliverConvertsAndThreads(t *testing.T) {
	var gotText, gotThread, gotChannel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat.postMessage" {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		_ = r.ParseForm()
		gotText, gotThread, gotChannel = r.Form.Get("text"), r.Form.Get("thread_ts"), r.Form.Get("channel")
		io.WriteString(w, `{"ok":true,"channel":"C1","ts":"1.2"}`)
	}))
	defer srv.Close()
	if err := deliver(context.Background(), testClient(srv), "C1", "111.222", "hello **world**", nil); err != nil {
		t.Fatal(err)
	}
	if gotText != "hello *world*" || gotThread != "111.222" || gotChannel != "C1" {
		t.Fatalf("text=%q thread=%q channel=%q", gotText, gotThread, gotChannel)
	}
}

// TestPostNoBlindResend: an ambiguous transport failure must not re-send (the
// at-most-once rule carried over from the Telegram fix).
func TestPostNoBlindResend(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()
	if err := deliver(context.Background(), testClient(srv), "C1", "", "hi", nil); err == nil {
		t.Fatal("expected error")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("ambiguous failure must not re-send: %d calls", n)
	}
}

// TestThreadParent: the thread-root fetch returns the parent's text + file refs
// (download URL preferred) — the "reply in a thread pointing at a file" case.
func TestThreadParent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/conversations.replies" {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("channel") != "C1" || r.Form.Get("ts") != "111.222" {
			t.Errorf("params: %v", r.Form)
		}
		io.WriteString(w, `{"ok":true,"messages":[
			{"type":"message","text":"monthly report attached","ts":"111.222",
			 "files":[{"name":"report.pdf","url_private":"https://p/x","url_private_download":"https://d/x"}]},
			{"type":"message","text":"@bot summarize this","ts":"111.333"}
		],"has_more":false}`)
	}))
	defer srv.Close()
	text, files, err := testClient(srv).ThreadParent(context.Background(), "C1", "111.222")
	if err != nil {
		t.Fatal(err)
	}
	if text != "monthly report attached" {
		t.Errorf("parent text: %q", text)
	}
	if len(files) != 1 || files[0].Name != "report.pdf" || files[0].URL != "https://d/x" {
		t.Errorf("parent files: %+v", files)
	}
}

// TestInThread: only true for replies inside a thread, not the root itself.
func TestInThread(t *testing.T) {
	if inThread("", "2.2") || inThread("1.1", "1.1") {
		t.Error("root/unthreaded must not count as in-thread")
	}
	if !inThread("1.1", "2.2") {
		t.Error("thread reply must count")
	}
}
