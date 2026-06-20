// Package config loads application configuration and bootstrap secrets.
//
// Two tiers of secrets exist: bootstrap secrets live here, loaded
// from the environment / .env file (MASTER_KEY, SESSION_KEY, bot token). Every
// other secret is encrypted at rest inside the SQLite DB under MASTER_KEY.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all runtime configuration. Values come from environment
// variables, which may be seeded from a .env file (env vars take precedence).
type Config struct {
	Env       string // "dev" | "prod"
	HTTPAddr  string // e.g. ":8080"
	DBPath    string // path to the SQLite file
	ClaudeBin string // path/name of the claude CLI for the headless runtime
	LogLevel  string // debug | info | warn | error

	// AllowAgentTools enables the runtime's scoped built-in tools (Read, Glob,
	// Grep, Bash, Write, Edit) so skills with scripts can execute. Acceptable
	// because runs are sandboxed at the container boundary.
	// Defaults on in prod (container), off for native dev; override with
	// ALLOW_AGENT_TOOLS.
	AllowAgentTools bool
	MasterKey       []byte // 32-byte AES key for encrypting DB-stored secrets (may be nil in dev)
	SessionKey      []byte // HMAC key for signed session cookies (auto-generated in dev if unset)

	// AgentIsolation runs each agent's host tools (bash/read/…) as a distinct,
	// unprivileged per-user OS uid so one user's run cannot read another user's
	// workspace or the database. Requires the process to run as root on Linux
	// (the container does); a no-op with a warning elsewhere (e.g. native dev).
	// Defaults on in prod, off for native dev; override with AGENT_ISOLATION.
	AgentIsolation bool
	// AgentUIDBase is the base for per-user run uids/gids: user N runs as
	// AgentUIDBase+N. Default 20000. Must not collide with the app uid (10001).
	AgentUIDBase int
	// AgentCredGID is a shared gid added to every run so the agent can still read
	// the shared claude.ai credentials dir while being unable to read the DB.
	AgentCredGID int

	TelegramBotToken string // MVP step 6; empty until configured

	// TrustProxy makes the portal derive the client IP from the X-Forwarded-For
	// header (for login rate-limiting and audit). Enable ONLY when the app sits
	// behind a trusted reverse proxy (e.g. the bundled nginx) and is not directly
	// reachable — otherwise a client could spoof the header. Default off.
	TrustProxy bool

	// CookieSecure marks session cookies Secure (HTTPS-only). Defaults to on in
	// prod. Set false ONLY when intentionally serving the portal over plain HTTP
	// (e.g. on a private network), otherwise the browser won't send the cookie
	// over http:// and login silently fails.
	CookieSecure bool
}

// Load reads configuration from the environment, first sourcing an optional
// .env file at envPath (if it exists). Explicit environment variables always
// win over .env values.
func Load(envPath string) (*Config, error) {
	if err := loadDotEnv(envPath); err != nil {
		return nil, err
	}

	c := &Config{
		Env:              getenv("APP_ENV", "dev"),
		HTTPAddr:         getenv("HTTP_ADDR", ":8080"),
		DBPath:           getenv("DB_PATH", filepath.Join("data", "app.db")),
		ClaudeBin:        getenv("CLAUDE_BIN", "claude"),
		LogLevel:         getenv("LOG_LEVEL", "info"),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
	}

	if v := os.Getenv("MASTER_KEY"); v != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("MASTER_KEY is not valid base64: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("MASTER_KEY must decode to 32 bytes, got %d", len(key))
		}
		c.MasterKey = key
	}

	c.AllowAgentTools = getenvBool("ALLOW_AGENT_TOOLS", c.IsProd())
	c.AgentIsolation = getenvBool("AGENT_ISOLATION", c.IsProd())
	c.AgentUIDBase = getenvInt("AGENT_UID_BASE", 20000)
	c.AgentCredGID = getenvInt("AGENT_CRED_GID", 10002)
	c.TrustProxy = getenvBool("TRUST_PROXY", false)
	c.CookieSecure = getenvBool("COOKIE_SECURE", c.IsProd())

	if err := c.resolveSessionKey(); err != nil {
		return nil, err
	}

	return c, nil
}

// getenvBool parses a boolean env var (true/1/on/yes), falling back to def.
func getenvBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
		return def
	}
}

// getenvInt parses an integer env var, falling back to def on empty/invalid.
func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// IsProd reports whether the app is running in production mode.
func (c *Config) IsProd() bool { return c.Env == "prod" }

// resolveSessionKey loads SESSION_KEY, or in dev generates one and persists it
// next to the DB so portal sessions survive restarts during development. In
// prod a SESSION_KEY must be provided explicitly.
func (c *Config) resolveSessionKey() error {
	if v := os.Getenv("SESSION_KEY"); v != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("SESSION_KEY is not valid base64: %w", err)
		}
		if len(key) < 32 {
			return fmt.Errorf("SESSION_KEY must decode to at least 32 bytes, got %d", len(key))
		}
		c.SessionKey = key
		return nil
	}

	if c.IsProd() {
		return fmt.Errorf("SESSION_KEY is required in prod (APP_ENV=prod)")
	}

	// Dev convenience: generate once, persist beside the DB.
	keyPath := filepath.Join(filepath.Dir(c.DBPath), ".session_key")
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err == nil && len(key) >= 32 {
			c.SessionKey = key
			return nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generating dev session key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("creating data dir for session key: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return fmt.Errorf("persisting dev session key: %w", err)
	}
	c.SessionKey = key
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv reads KEY=VALUE lines from path into the process environment,
// without overriding variables that are already set. Missing file is not an
// error. Supports # comments and surrounding quotes on values.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}
