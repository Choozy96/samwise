package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "sid"
	sessionTTL    = 7 * 24 * time.Hour
)

// issueSession sets a signed session cookie binding the browser to userID.
//
// The cookie value is "<uid>.<expiryUnix>.<base64(HMAC)>" signed with the
// configured SESSION_KEY — stateless, no server-side session table. Tampering
// or expiry invalidates it on the next request.
func (s *Server) issueSession(w http.ResponseWriter, userID int64) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	val := payload + "." + s.signSession(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.IsProd(),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// clearSession removes the session cookie (logout).
func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.IsProd(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// readSession validates the session cookie and returns the user id. ok is false
// for a missing, malformed, tampered, or expired cookie.
func (s *Server) readSession(r *http.Request) (userID int64, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return 0, false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(s.signSession(payload))) {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return uid, true
}

func (s *Server) signSession(payload string) string {
	mac := hmac.New(sha256.New, s.cfg.SessionKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
