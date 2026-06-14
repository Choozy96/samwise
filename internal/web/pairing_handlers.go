package web

import (
	"errors"
	"net/http"
	"strings"

	"samwise/internal/store"
)

// handlePairingSubmit redeems a pairing code, linking the Telegram sender to the
// logged-in user (spec §4.1). It lives on the Agents page now.
func (s *Server) handlePairingSubmit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("code")))
	if code == "" {
		http.Redirect(w, r, "/agents?msg=badcode", http.StatusSeeOther)
		return
	}
	pc, err := s.db.ConsumePairingCode(r.Context(), code)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("consume pairing code", "err", err)
		}
		http.Redirect(w, r, "/agents?msg=badcode", http.StatusSeeOther)
		return
	}
	if err := s.db.CreateIdentity(r.Context(), store.ChannelIdentity{
		UserID:     u.ID,
		Channel:    pc.Channel,
		BotID:      pc.BotID,
		ExternalID: pc.ExternalID,
		ChatID:     pc.ChatID,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("telegram paired", "user_id", u.ID, "external_id", pc.ExternalID, "bot_id", pc.BotID)
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "auth", "telegram_paired", "linked telegram sender "+pc.ExternalID, "ok")
	// Best-effort confirmation back to the chat, via the bot the user just paired.
	_ = s.orch.NotifyTelegramBot(r.Context(), u.ID, pc.BotID, "✅ Paired! You're connected as "+u.Username+". How can I help?")
	http.Redirect(w, r, "/agents?msg=paired", http.StatusSeeOther)
}
