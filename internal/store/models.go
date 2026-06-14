package store

import "errors"

// ErrNotFound is returned by single-row getters when no row matches.
var ErrNotFound = errors.New("store: not found")

// User is a portal account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	Disabled     bool
	CreatedAt    string
}

// Settings holds a user's per-user preferences (spec §9).
type Settings struct {
	UserID            int64
	Timezone          string // IANA name
	ActiveRuntime     string // claude-headless | claude-channels | codex-exec
	DeliveryChannel   string // web | telegram
	ModelHints        string // JSON map of job-type -> model
	BriefingTime      string // local HH:MM
	RestartTime       string // local HH:MM
	DistillationTime  string // local HH:MM
	TranscriptWindowN int
	RetrievalK        int
	TgFormat          string // markdown | html | plain (how Telegram messages are formatted)
	DistillNotify     bool   // send a message after end-of-day distillation (default true)
}
