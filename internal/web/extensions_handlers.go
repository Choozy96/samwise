package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/schedule"
	"samwise/internal/store"
)

// envNameRe matches a valid environment-variable name.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// handleSecretAdd stores a per-user secret (encrypted at rest). It's injected
// into the user's runs as an environment variable so skill scripts can read it —
// the value never touches memory, the prompt, or the chat transcript, and is
// never rendered back in the UI.
func (s *Server) handleSecretAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	value := r.FormValue("value")
	kind := pick(r.FormValue("kind"), []string{"value", "oauth"}, "value")
	if !envNameRe.MatchString(name) || strings.TrimSpace(value) == "" {
		http.Redirect(w, r, "/extensions?msg=secret_badname", http.StatusSeeOther)
		return
	}
	if !s.box.Enabled() {
		http.Redirect(w, r, "/extensions?msg=secret_nokey", http.StatusSeeOther)
		return
	}
	enc, err := s.box.Encrypt([]byte(value))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.db.SetSecret(r.Context(), u.ID, name, enc, kind); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "secret", "secret_set", name, "ok")
	// For an OAuth credential, refresh it now so an expiry is established and shown
	// immediately (a raw Google token.json has no expiry field until first refresh).
	if kind == "oauth" {
		s.orch.RefreshOAuthSecrets(r.Context(), u.ID)
	}
	http.Redirect(w, r, "/extensions?msg=secret_saved", http.StatusSeeOther)
}

// authStatuses assembles the read-only credential-expiry view: the Claude
// subscription token (from its credentials file) plus any oauth-kind secrets
// (decrypted only to parse expiry; the value itself is never exposed).
func (s *Server) authStatuses(secrets []store.Secret) []AuthStatus {
	var out []AuthStatus
	if st, ok := claudeAuthStatus(); ok {
		out = append(out, st)
	}
	for _, sec := range secrets {
		if sec.Kind != "oauth" {
			continue
		}
		raw, err := s.box.Decrypt(sec.ValueEnc)
		if err != nil {
			out = append(out, AuthStatus{Label: sec.Name, Detail: "stored (couldn't read expiry)"})
			continue
		}
		if st, ok := oauthSecretStatus(sec.Name, string(raw)); ok {
			out = append(out, st)
		}
	}
	return out
}

// handleSecretRefresh forces an immediate refresh of the credentials shown in the
// status panel: the user's oauth-kind secrets (Google etc.) and the Claude login.
func (s *Server) handleSecretRefresh(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	dead := s.orch.RefreshOAuthSecrets(r.Context(), u.ID)
	claudeOK, _ := s.orch.RefreshClaudeAuth(r.Context())
	switch {
	case len(dead) > 0:
		http.Redirect(w, r, "/extensions?msg=secret_reauth", http.StatusSeeOther)
	case !claudeOK:
		http.Redirect(w, r, "/extensions?msg=claude_reauth", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/extensions?msg=secret_refreshed", http.StatusSeeOther)
	}
}

// handleSecretDelete removes a user-owned secret by name.
func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if name := strings.TrimSpace(r.FormValue("name")); name != "" {
		if err := s.db.DeleteSecret(r.Context(), u.ID, name); err != nil {
			s.serverError(w, r, err)
			return
		}
		_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "secret", "secret_delete", name, "ok")
	}
	http.Redirect(w, r, "/extensions?msg=secret_deleted", http.StatusSeeOther)
}

const briefingJobName = "Morning briefing"

// handleExtensions renders the Extensions page: MCP registry + skills + the
// morning-briefing reference extension.
func (s *Server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	mcps, err := s.db.ListMCPServersForUser(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	skills, err := s.db.ListSkillsForUser(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	skillViews := make([]skillView, len(skills))
	for i, sk := range skills {
		skillViews[i] = skillView{Skill: sk, Bundle: s.skillBundleFiles(u.ID, sk)}
	}
	secrets, _ := s.db.ListSecrets(r.Context(), u.ID)

	data := pageData{
		"Title":        "Extensions",
		"MCPs":         mcps,
		"Skills":       skillViews,
		"Secrets":      secrets,
		"AuthStatuses": s.authStatuses(secrets),
		"BoxEnabled":   s.box.Enabled(),
		"AgentToolsOn": s.cfg.AllowAgentTools,
	}
	switch r.URL.Query().Get("msg") {
	case "mcp_added":
		data["Flash"], data["FlashKind"] = "MCP server added.", "ok"
	case "mcp_nokey":
		data["Flash"], data["FlashKind"] = "Set MASTER_KEY in your env file to store credentials.", "error"
	case "mcp_bad":
		data["Flash"], data["FlashKind"] = "Provide a name and a command (stdio) or URL (http).", "error"
	case "skill_saved":
		data["Flash"], data["FlashKind"] = "Skill saved.", "ok"
	case "skill_imported":
		data["Flash"], data["FlashKind"] = "Skill imported. Toggle it 'always on' to use it in chat.", "ok"
	case "import_nomd":
		data["Flash"], data["FlashKind"] = "No SKILL.md found at the root of the zip.", "error"
	case "import_bad":
		data["Flash"], data["FlashKind"] = "Couldn't read that upload. Provide a .zip with a SKILL.md.", "error"
	case "import_zip":
		data["Flash"], data["FlashKind"] = "The zip couldn't be extracted (unsafe paths or too large).", "error"
	case "secret_saved":
		data["Flash"], data["FlashKind"] = "Secret saved — it's injected into runs as an env var.", "ok"
	case "secret_nokey":
		data["Flash"], data["FlashKind"] = "Set MASTER_KEY in your env file to store secrets.", "error"
	case "secret_badname":
		data["Flash"], data["FlashKind"] = "Name must be a valid env var (A–Z, 0–9, underscore; not starting with a digit), e.g. TODOIST_TOKEN.", "error"
	case "secret_deleted":
		data["Flash"], data["FlashKind"] = "Secret deleted.", "ok"
	case "secret_refreshed":
		data["Flash"], data["FlashKind"] = "OAuth credentials refreshed.", "ok"
	case "secret_reauth":
		data["Flash"], data["FlashKind"] = "Some OAuth credentials couldn't be refreshed — re-authenticate and update them below.", "error"
	case "claude_reauth":
		data["Flash"], data["FlashKind"] = "The Claude login can't be refreshed — re-authenticate it (copy a fresh ~/.claude/.credentials.json). See deploy docs.", "error"
	}
	s.render(w, r, "extensions", data)
}

// handleMCPAdd registers an MCP server, encrypting any credentials at rest.
func (s *Server) handleMCPAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	name := slugish(r.FormValue("name"))
	transport := pick(r.FormValue("transport"), []string{"stdio", "http"}, "stdio")
	command := strings.TrimSpace(r.FormValue("command"))
	url := strings.TrimSpace(r.FormValue("url"))

	if name == "" || (transport == "stdio" && command == "") || (transport == "http" && url == "") {
		http.Redirect(w, r, "/extensions?msg=mcp_bad", http.StatusSeeOther)
		return
	}

	var argsJSON string
	if fields := strings.Fields(r.FormValue("args")); len(fields) > 0 {
		b, _ := json.Marshal(fields)
		argsJSON = string(b)
	}

	// Credentials: KEY=value lines become env (stdio) or headers (http).
	kv := parseKVLines(r.FormValue("secrets"))
	var secretEnc string
	if len(kv) > 0 {
		if !s.box.Enabled() {
			http.Redirect(w, r, "/extensions?msg=mcp_nokey", http.StatusSeeOther)
			return
		}
		var raw []byte
		if transport == "http" {
			raw, _ = orchestrator.EncodeSecret(nil, kv)
		} else {
			raw, _ = orchestrator.EncodeSecret(kv, nil)
		}
		enc, err := s.box.Encrypt(raw)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		secretEnc = enc
	}

	if _, err := s.db.CreateMCPServer(r.Context(), store.MCPServer{
		UserID:    u.ID,
		Name:      name,
		Transport: transport,
		Command:   command,
		ArgsJSON:  argsJSON,
		URL:       url,
		SecretEnc: secretEnc,
		Enabled:   true,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/extensions?msg=mcp_added", http.StatusSeeOther)
}

// handleMCPToggle enables/disables a user-owned server.
func (s *Server) handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		_ = s.db.SetMCPServerEnabled(r.Context(), u.ID, id, r.FormValue("enabled") == "1")
	}
	http.Redirect(w, r, "/extensions", http.StatusSeeOther)
}

// handleMCPDelete removes a user-owned server.
func (s *Server) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		_ = s.db.DeleteMCPServer(r.Context(), u.ID, id)
	}
	http.Redirect(w, r, "/extensions", http.StatusSeeOther)
}

// handleSkillSave creates or updates a skill.
func (s *Server) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	name := slugish(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/extensions", http.StatusSeeOther)
		return
	}
	sk := store.Skill{
		UserID:      u.ID,
		Name:        name,
		Description: strings.TrimSpace(r.FormValue("description")),
		Content:     r.FormValue("content"),
		AlwaysOn:    r.FormValue("always_on") == "1",
		Enabled:     true,
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	var err error
	if id > 0 {
		sk.ID = id
		err = s.db.UpdateSkill(r.Context(), sk)
	} else {
		_, err = s.db.CreateSkill(r.Context(), sk)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/extensions?msg=skill_saved", http.StatusSeeOther)
}

// handleSkillToggle enables/disables a user-owned skill.
func (s *Server) handleSkillToggle(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		_ = s.db.SetSkillEnabled(r.Context(), u.ID, id, r.FormValue("enabled") == "1")
	}
	http.Redirect(w, r, "/extensions", http.StatusSeeOther)
}

// handleSkillDelete removes a user-owned skill.
func (s *Server) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		_ = s.db.DeleteSkill(r.Context(), u.ID, id)
	}
	http.Redirect(w, r, "/extensions", http.StatusSeeOther)
}

// handleSkillImport imports a skill from an uploaded .zip (a SKILL.md plus
// optional scripts/assets). It extracts the bundle to the user's workspace and
// stores the skill; the agent can run its scripts when host tools are enabled.
func (s *Server) handleSkillImport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		http.Redirect(w, r, "/extensions?msg=import_bad", http.StatusSeeOther)
		return
	}
	f, hdr, err := r.FormFile("skillzip")
	if err != nil {
		http.Redirect(w, r, "/extensions?msg=import_bad", http.StatusSeeOther)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (25<<20)+1))
	if err != nil || len(data) > 25<<20 {
		http.Redirect(w, r, "/extensions?msg=import_bad", http.StatusSeeOther)
		return
	}

	md, err := findSkillMD(data)
	if err != nil {
		http.Redirect(w, r, "/extensions?msg=import_nomd", http.StatusSeeOther)
		return
	}
	name, desc, body := parseFrontMatter(md)
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".zip")
	}
	slug := slugish(name)
	if slug == "" {
		http.Redirect(w, r, "/extensions?msg=import_bad", http.StatusSeeOther)
		return
	}

	destDir := s.orch.SkillBundleDir(u.ID, slug)
	files, err := extractSkillZip(data, destDir)
	if err != nil {
		s.log.Error("skill import extract", "user_id", u.ID, "err", err)
		http.Redirect(w, r, "/extensions?msg=import_zip", http.StatusSeeOther)
		return
	}

	if _, err := s.db.UpsertSkillByName(r.Context(), store.Skill{
		UserID:      u.ID,
		Name:        slug,
		Description: desc,
		Content:     body,
		Enabled:     true,
		HasBundle:   true,
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "skill", slug, fmt.Sprintf("imported zip (%d files)", files), "ok")
	http.Redirect(w, r, "/extensions?msg=skill_imported", http.StatusSeeOther)
}

// ensureMorningBriefing creates the morning-briefing skill (if missing) and a
// daily agent_run job (if not already present), returning whether it created
// the job. Used by the onboarding wizard's optional briefing step.
func (s *Server) ensureMorningBriefing(ctx context.Context, userID int64, settings *store.Settings) (bool, error) {
	jobs, _ := s.db.ListJobs(ctx, userID)
	for _, j := range jobs {
		if j.Name == briefingJobName {
			return false, nil
		}
	}
	if existing, _ := s.db.GetSkillByName(ctx, userID, "morning-briefing"); existing == nil {
		if _, err := s.db.CreateSkill(ctx, store.Skill{
			UserID:      userID,
			Name:        "morning-briefing",
			Description: "How to assemble a concise morning briefing.",
			Content:     briefingSkillContent,
			Enabled:     true,
		}); err != nil {
			return false, err
		}
	}
	specStr := "daily@" + settings.BriefingTime
	spec, perr := schedule.Parse(specStr)
	if perr != nil {
		specStr = "daily@07:00"
		spec, _ = schedule.Parse(specStr)
	}
	loc := schedule.LocationFor("user_local", "", settings.Timezone)
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		return false, perr
	}
	payload, _ := json.Marshal(map[string]string{"prompt": briefingPrompt, "skill": "morning-briefing"})
	_, err := s.db.CreateJob(ctx, store.Job{
		UserID:       userID,
		Name:         briefingJobName,
		Type:         "agent_run",
		ScheduleSpec: specStr,
		TZMode:       "user_local",
		Payload:      string(payload),
		Enabled:      true,
		CatchUp:      true,
		NextFireUTC:  next.UTC().Format(time.RFC3339),
	})
	return err == nil, err
}

// parseKVLines parses "KEY=value" lines into a map, ignoring blanks/comments.
func parseKVLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// slugish trims and collapses a name to a safe, lowercased token.
func slugish(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
