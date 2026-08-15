package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSendDocument verifies the multipart upload: the request hits sendDocument
// with the chat id, caption, and the file bytes under the "document" field.
func TestSendDocument(t *testing.T) {
	var gotPath, gotChat, gotCaption, gotFilename string
	var gotBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		gotChat = r.FormValue("chat_id")
		gotCaption = r.FormValue("caption")
		if f, fh, err := r.FormFile("document"); err == nil {
			gotFilename = fh.Filename
			gotBytes, _ = io.ReadAll(f)
			f.Close()
		} else {
			t.Errorf("no document field: %v", err)
		}
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer srv.Close()

	c := &Client{token: "t", baseURL: srv.URL + "/bott", http: srv.Client()}
	if err := c.SendDocument(context.Background(), 4242, "report.xlsx", []byte("PK\x03\x04data"), "Here you go"); err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/sendDocument") {
		t.Errorf("path = %q, want .../sendDocument", gotPath)
	}
	if gotChat != "4242" {
		t.Errorf("chat_id = %q, want 4242", gotChat)
	}
	if gotCaption != "Here you go" {
		t.Errorf("caption = %q", gotCaption)
	}
	if gotFilename != "report.xlsx" {
		t.Errorf("filename = %q", gotFilename)
	}
	if string(gotBytes) != "PK\x03\x04data" {
		t.Errorf("bytes = %q", gotBytes)
	}
}

// TestSendDocumentError surfaces a non-200 from the API.
func TestSendDocumentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"description":"file too big"}`)
	}))
	defer srv.Close()
	c := &Client{token: "t", baseURL: srv.URL + "/bott", http: srv.Client()}
	if err := c.SendDocument(context.Background(), 1, "x.txt", []byte("y"), ""); err == nil {
		t.Fatal("expected an error on 400")
	}
}
