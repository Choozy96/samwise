package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"samwise/internal/orchestrator"
	"samwise/internal/runtime"
)

var errNoFlush = errors.New("streaming unsupported by this server")

// handleChat renders the chat page for the active agent's conversation.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	agent, err := s.db.GetActiveAgent(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	conv, err := s.db.GetOrCreateConversation(r.Context(), u.ID, "web", agent.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	msgs, err := s.db.RecentMessages(r.Context(), conv.ID, 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	agents, _ := s.db.ListAgents(r.Context(), u.ID)
	data := pageData{"Title": "Chat", "Messages": msgs, "Agents": agents, "ActiveAgent": agent}
	if st, err := s.db.GetSettings(r.Context(), u.ID); err == nil {
		rt := agent.Runtime
		if rt == "" {
			rt = st.ActiveRuntime
		}
		model := agent.Model
		if model == "" {
			model = orchestrator.ModelHintFor(st.ModelHints, "chat")
		}
		data["RuntimeLabel"] = orchestrator.RuntimeLabel(rt)
		data["ModelLabel"] = orchestrator.ModelLabel(model)
		data["RuntimeAvailable"] = s.orch.IsRuntimeAvailable(rt)
	}
	s.render(w, r, "chat", data)
}

// chatEvent is one streamed line in the /chat/send response (NDJSON).
type chatEvent struct {
	Type string `json:"type"` // text | tool | done | error
	Text string `json:"text,omitempty"`
}

// handleChatSend executes a turn and streams the assistant's output back as
// newline-delimited JSON. Delivery/formatting live here; the agent
// has no send tool.
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	_ = r.ParseMultipartForm(maxAttachTotalBytes)
	msg := strings.TrimSpace(r.FormValue("message"))

	// Save any attachments to the user's workspace before streaming begins, so a
	// failure can return a normal error.
	var atts []orchestrator.Attachment
	if r.MultipartForm != nil && len(r.MultipartForm.File["attachments"]) > 0 {
		var err error
		atts, err = s.saveAttachments(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if msg == "" && len(atts) == 0 {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.serverError(w, r, errNoFlush)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	send := func(ev chatEvent) {
		_ = enc.Encode(ev)
		flusher.Flush()
	}

	// Slash commands (e.g. /model, /runtime) are handled inline without invoking
	// the agent. Skipped when there are attachments.
	if len(atts) == 0 {
		if reply, handled := s.orch.TryCommand(r.Context(), u.ID, msg); handled {
			send(chatEvent{Type: "text", Text: reply})
			send(chatEvent{Type: "done"})
			return
		}
	}

	_, err := s.orch.Dispatch(r.Context(), orchestrator.DispatchRequest{
		User:             u,
		Channel:          "web",
		UserMessage:      msg,
		Attachments:      atts,
		StoreUserMessage: true,
	}, func(ev runtime.Event) {
		switch ev.Kind {
		case runtime.EventText:
			send(chatEvent{Type: "text", Text: ev.Text})
		case runtime.EventToolCall:
			send(chatEvent{Type: "tool", Text: ev.Tool})
		case runtime.EventError:
			send(chatEvent{Type: "error", Text: ev.Text})
		}
	})
	if err != nil {
		s.log.Error("chat dispatch", "user_id", u.ID, "err", err)
		send(chatEvent{Type: "error", Text: "The assistant run failed. Check the logs."})
		return
	}
	send(chatEvent{Type: "done"})
}
