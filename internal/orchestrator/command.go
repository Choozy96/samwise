package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"samwise/internal/auth"
	"samwise/internal/schedule"
	"samwise/internal/store"
)

// TryCommand intercepts slash commands typed in any channel (web + Telegram).
// It returns the reply text and handled=true when the message was a
// recognized command; otherwise handled=false and the caller dispatches the
// message to the agent as normal. Unrecognized "/..." messages are passed
// through to the agent (handled=false) so they aren't swallowed.
func (o *Orchestrator) TryCommand(ctx context.Context, userID int64, msg string) (reply string, handled bool) {
	msg = strings.TrimSpace(msg)
	if !strings.HasPrefix(msg, "/") {
		return "", false
	}
	fields := strings.Fields(msg)
	cmd := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(msg, fields[0]))

	switch cmd {
	case "/help", "/commands", "/?":
		return helpText(), true
	case "/status", "/whoami":
		return o.statusText(ctx, userID), true
	case "/model":
		return o.cmdModel(ctx, userID, arg), true
	case "/runtime", "/access":
		return o.cmdRuntime(ctx, userID, arg), true
	case "/agent", "/agents":
		return o.cmdAgent(ctx, userID, arg), true
	case "/refresh-claude", "/refreshclaude":
		_, msg := o.RefreshClaudeAuth(ctx)
		return msg, true
	case "/format", "/formatting":
		return o.cmdFormat(ctx, userID, arg), true
	case "/groupreply", "/groupmode":
		return o.cmdGroupReply(ctx, userID, arg), true
	case "/timezone", "/tz":
		return o.cmdTimezone(ctx, userID, arg), true
	case "/delivery":
		return o.cmdDelivery(ctx, userID, arg), true
	case "/jobs":
		return o.cmdJobs(ctx, userID), true
	case "/reminders":
		return o.cmdReminders(ctx, userID), true
	case "/recall", "/search":
		return o.cmdRecall(ctx, userID, arg), true
	case "/new", "/reset":
		return o.cmdNew(ctx, userID), true
	case "/usage":
		return o.cmdUsage(ctx, userID), true
	case "/remind":
		return o.cmdRemind(ctx, userID, arg), true
	case "/bots":
		return o.cmdBots(ctx, userID), true
	case "/bind":
		return o.cmdBind(ctx, userID, arg), true
	case "/password", "/passwd":
		return o.cmdPassword(ctx, userID, arg), true
	case "/admin", "/users":
		return o.cmdAdmin(ctx, userID, arg), true
	default:
		return "", false
	}
}

// cmdFormat shows or sets the user's Telegram message format.
func (o *Orchestrator) cmdFormat(ctx context.Context, userID int64, arg string) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		cur := s.TgFormat
		if cur == "" || cur == "telegram" {
			cur = "markdown"
		}
		return fmt.Sprintf("Telegram message format: %s.\nSet with /format <markdown|html|plain>.\n"+
			"• markdown — renders bold/italic/code/links (parse_mode=MarkdownV2) — recommended\n"+
			"• html — same, using parse_mode=HTML\n"+
			"• plain — no formatting (send text as-is)", cur)
	case "markdown", "md", "rich", "tg", "telegram":
		s.TgFormat = "markdown"
	case "html":
		s.TgFormat = "html"
	case "plain", "raw", "none":
		s.TgFormat = "plain"
	default:
		return "Unknown format. Use: /format markdown, /format html, or /format plain."
	}
	if err := o.db.UpdateSettings(ctx, s); err != nil {
		return "Couldn't save that."
	}
	return "Telegram message format set to " + s.TgFormat + " — applies to your next message."
}

// cmdGroupReply shows or sets how the bot replies in group chats.
func (o *Orchestrator) cmdGroupReply(ctx context.Context, userID int64, arg string) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		cur := s.GroupReplyMode
		if cur == "" {
			cur = "mention"
		}
		return fmt.Sprintf("Group reply mode: %s.\nSet with /groupreply <mention|all>.\n"+
			"• mention — reply only when the bot is @mentioned, replied to, or sent a command (default)\n"+
			"• all — reply to every group message (also needs the bot's Telegram privacy mode turned off in @BotFather)", cur)
	case "mention", "mentioned", "mentions":
		s.GroupReplyMode = "mention"
	case "all", "everything", "any":
		s.GroupReplyMode = "all"
	default:
		return "Unknown mode. Use: /groupreply mention  or  /groupreply all."
	}
	if err := o.db.UpdateSettings(ctx, s); err != nil {
		return "Couldn't save that."
	}
	return "Group reply mode set to " + s.GroupReplyMode + "."
}

// cmdTimezone shows or sets the user's timezone (and recomputes local schedules).
func (o *Orchestrator) cmdTimezone(ctx context.Context, userID int64, arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		s, err := o.db.GetSettings(ctx, userID)
		if err != nil {
			return "Couldn't read your settings."
		}
		return "Timezone: " + s.Timezone + ".\nSet with /timezone <IANA name>, e.g. /timezone Asia/Singapore."
	}
	if _, err := time.LoadLocation(arg); err != nil {
		return "Unknown timezone. Use an IANA name like Asia/Singapore or America/New_York."
	}
	if err := o.db.UpdateTimezone(ctx, userID, arg); err != nil {
		return "Couldn't save that."
	}
	_ = schedule.RecomputeUserLocal(ctx, o.db, userID, time.Now())
	return "Timezone set to " + arg + ". Your local-time schedules were recomputed."
}

// cmdDelivery shows or sets the delivery channel for scheduled output.
func (o *Orchestrator) cmdDelivery(ctx context.Context, userID int64, arg string) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		return "Delivery channel: " + s.DeliveryChannel + ".\nSet with /delivery <web|telegram>."
	case "web":
		s.DeliveryChannel = "web"
	case "telegram", "tg":
		s.DeliveryChannel = "telegram"
	default:
		return "Use: /delivery web  or  /delivery telegram."
	}
	if err := o.db.UpdateSettings(ctx, s); err != nil {
		return "Couldn't save that."
	}
	return "Delivery channel set to " + s.DeliveryChannel + "."
}

// cmdJobs lists the user's recurring scheduled (agent_run) jobs.
func (o *Orchestrator) cmdJobs(ctx context.Context, userID int64) string {
	jobs, err := o.db.ListJobs(ctx, userID)
	if err != nil {
		return "Couldn't read your jobs."
	}
	s, _ := o.db.GetSettings(ctx, userID)
	loc := schedule.LocationFor("user_local", "", s.Timezone)
	var b strings.Builder
	n := 0
	for _, j := range jobs {
		if j.Type != "agent_run" {
			continue
		}
		n++
		status := ""
		if !j.Enabled {
			status = " [paused]"
		}
		fmt.Fprintf(&b, "• %q [%s] next %s%s\n", j.Name, j.ScheduleSpec, localFire(j.NextFireUTC, loc), status)
	}
	if n == 0 {
		return "No scheduled jobs yet. Ask me to set one up (e.g. \"a daily briefing at 8am\")."
	}
	return "Scheduled jobs:\n" + strings.TrimRight(b.String(), "\n")
}

// cmdReminders lists the user's active reminders (direct_message jobs).
func (o *Orchestrator) cmdReminders(ctx context.Context, userID int64) string {
	jobs, err := o.db.ListJobs(ctx, userID)
	if err != nil {
		return "Couldn't read your reminders."
	}
	s, _ := o.db.GetSettings(ctx, userID)
	loc := schedule.LocationFor("user_local", "", s.Timezone)
	var b strings.Builder
	n := 0
	for _, j := range jobs {
		if j.Type != "direct_message" || !j.Enabled {
			continue
		}
		n++
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(j.Payload), &p)
		fmt.Fprintf(&b, "• #%d %s — %s\n", j.ID, localFire(j.NextFireUTC, loc), p.Message)
	}
	if n == 0 {
		return "No active reminders. Set one with /remind, or just ask me."
	}
	return "Reminders:\n" + strings.TrimRight(b.String(), "\n")
}

// cmdRecall searches the user's memory and returns matching entries directly.
func (o *Orchestrator) cmdRecall(ctx context.Context, userID int64, arg string) string {
	q := strings.TrimSpace(arg)
	if q == "" {
		return "Usage: /recall <what to search for>."
	}
	hits, err := o.db.SearchMemory(ctx, userID, store.AllAgents, q, "", "", "", 10)
	if err != nil {
		return "Search failed."
	}
	if len(hits) == 0 {
		return fmt.Sprintf("Nothing in memory matches %q.", q)
	}
	var b strings.Builder
	for _, h := range hits {
		if h.Layer == "episodic" {
			fmt.Fprintf(&b, "• [%s] %s\n", h.TS, h.Content)
		} else {
			fmt.Fprintf(&b, "• %s\n", h.Content)
		}
	}
	return fmt.Sprintf("Memory matches for %q:\n%s", q, strings.TrimRight(b.String(), "\n"))
}

// cmdNew starts a fresh conversation thread for the active agent.
func (o *Orchestrator) cmdNew(ctx context.Context, userID int64) string {
	agent, err := o.db.GetActiveAgent(ctx, userID)
	if err != nil {
		return "Couldn't start a new conversation."
	}
	if err := o.db.NewConversation(ctx, userID, "web", agent.ID); err != nil {
		return "Couldn't start a new conversation."
	}
	return "🆕 Started a fresh conversation — earlier context is cleared (your memory and history are kept)."
}

// cmdUsage summarizes recent run counts and token usage by type. Tokens are the
// tracked metric (portable across models and pricing); dollar cost isn't shown.
func (o *Orchestrator) cmdUsage(ctx context.Context, userID int64) string {
	now := time.Now().UTC()
	const f = "2006-01-02 15:04:05"
	day, _ := o.db.UsageSince(ctx, userID, now.Add(-24*time.Hour).Format(f))
	week, _ := o.db.UsageSince(ctx, userID, now.Add(-7*24*time.Hour).Format(f))
	return "Token usage (input · output · cache-write · cache-read):\n" +
		"• last 24h: " + fmtUsage(day) + "\n" +
		"• last 7d: " + fmtUsage(week)
}

// fmtUsage renders one usage window as runs + tokens by type.
func fmtUsage(u store.Usage) string {
	return fmt.Sprintf("%d runs%s — in %s · out %s · cache-write %s · cache-read %s",
		u.Runs, errSuffix(u.Errors),
		fmtTokens(u.InputTokens), fmtTokens(u.OutputTokens),
		fmtTokens(u.CacheCreationTokens), fmtTokens(u.CacheReadTokens))
}

// fmtTokens renders a token count compactly (e.g. 1.2k, 3.4M).
func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// cmdRemind sets a reminder. Time forms: 'HH:MM' (today/tomorrow), 'daily HH:MM',
// or 'YYYY-MM-DD HH:MM'.
func (o *Orchestrator) cmdRemind(ctx context.Context, userID int64, arg string) string {
	arg = strings.TrimSpace(arg)
	fields := strings.Fields(arg)
	if len(fields) < 2 {
		return "Usage: /remind <time> <message>. Time: HH:MM, 'daily HH:MM', or 'YYYY-MM-DD HH:MM'."
	}
	s, _ := o.db.GetSettings(ctx, userID)
	loc := schedule.LocationFor("user_local", "", s.Timezone)
	now := time.Now().In(loc)

	var specStr string
	var consumed int
	switch {
	case strings.EqualFold(fields[0], "daily") && len(fields) >= 3 && isHHMM(fields[1]):
		specStr, consumed = "daily@"+fields[1], 2
	case len(fields) >= 3 && isDate(fields[0]) && isHHMM(fields[1]):
		t, err := time.ParseInLocation("2006-01-02 15:04", fields[0]+" "+fields[1], loc)
		if err != nil {
			return "Couldn't parse that date/time."
		}
		specStr, consumed = "once@"+t.Format("2006-01-02T15:04"), 2
	case isHHMM(fields[0]):
		hm, _ := time.ParseInLocation("15:04", fields[0], loc)
		t := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, loc)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1) // already past today → tomorrow
		}
		specStr, consumed = "once@"+t.Format("2006-01-02T15:04"), 1
	default:
		return "Couldn't parse the time. Use HH:MM, 'daily HH:MM', or 'YYYY-MM-DD HH:MM'."
	}
	msg := strings.Join(fields[consumed:], " ")
	if strings.TrimSpace(msg) == "" {
		return "What should I remind you about? /remind <time> <message>."
	}
	spec, perr := schedule.Parse(specStr)
	if perr != nil {
		return "Bad time spec."
	}
	next, ok := schedule.NextFireUTC(spec, loc, time.Now())
	if !ok {
		return "That time is in the past."
	}
	payload, _ := json.Marshal(map[string]string{"message": msg})
	id, err := o.db.CreateJob(ctx, store.Job{
		UserID: userID, Name: "reminder", Type: "direct_message",
		ScheduleSpec: specStr, TZMode: "user_local", Payload: string(payload),
		Enabled: true, CatchUp: true, NextFireUTC: next.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return "Couldn't set the reminder."
	}
	return fmt.Sprintf("⏰ Reminder #%d set for %s: %s", id, next.In(loc).Format("Mon 2006-01-02 15:04"), msg)
}

// cmdPassword changes the signed-in user's password from chat. The command
// message is never stored in the transcript (slash commands are intercepted
// before dispatch) and the values are never logged — but it still travels over
// the channel, so the reply warns the user to delete it (especially on Telegram).
func (o *Orchestrator) cmdPassword(ctx context.Context, userID int64, arg string) string {
	fields := strings.Fields(arg)
	if len(fields) != 2 {
		return "Usage: /password <current> <new> (new ≥ 8 chars).\n" +
			"⚠️ Your message contains your password and travels over this channel — on Telegram it passes through Telegram's servers. Prefer the web portal (Settings → Account); if you use this, delete the message right after."
	}
	current, next := fields[0], fields[1]
	user, err := o.db.GetUserByID(ctx, userID)
	if err != nil {
		return "Couldn't load your account."
	}
	if err := auth.VerifyPassword(current, user.PasswordHash); err != nil {
		_ = o.db.AddAuditEvent(ctx, userID, 0, "auth", "password_change", "via chat command", "denied")
		return "Current password is incorrect."
	}
	if len(next) < 8 {
		return "New password must be at least 8 characters."
	}
	if next == current {
		return "New password must differ from the current one."
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		return "Couldn't update the password."
	}
	if err := o.db.UpdatePassword(ctx, userID, hash); err != nil {
		return "Couldn't update the password."
	}
	_ = o.db.AddAuditEvent(ctx, userID, 0, "auth", "password_change", "via chat command", "ok")
	return "✅ Password changed.\n⚠️ Delete your message above — it contains your old and new passwords."
}

const adminHelp = "Admin commands (admins only):\n" +
	"• /admin users — list all users\n" +
	"• /admin add <username> <password> — create a user\n" +
	"• /admin disable <username> — disable a user\n" +
	"• /admin enable <username> — re-enable a user\n" +
	"• /admin resetpw <username> <new-password> — reset a user's password\n" +
	"⚠️ Subcommands with a password expose it to this channel — prefer the web Admin page and delete the message after."

// cmdAdmin handles admin-only user management. The calling user must be an admin;
// it mirrors the web Admin page guards (non-admin targets only for disable/reset,
// length/dup checks on create).
func (o *Orchestrator) cmdAdmin(ctx context.Context, userID int64, arg string) string {
	caller, err := o.db.GetUserByID(ctx, userID)
	if err != nil {
		return "Couldn't verify your account."
	}
	if !caller.IsAdmin {
		return "That command is for admins only."
	}
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return adminHelp
	}
	sub := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(arg), fields[0]))
	switch sub {
	case "help":
		return adminHelp
	case "users", "list", "ls":
		return o.adminListUsers(ctx)
	case "add", "adduser", "create":
		return o.adminAddUser(ctx, rest)
	case "disable":
		return o.adminSetDisabled(ctx, rest, true)
	case "enable":
		return o.adminSetDisabled(ctx, rest, false)
	case "resetpw", "resetpassword":
		return o.adminResetPw(ctx, rest)
	default:
		return "Unknown admin subcommand. Try /admin help."
	}
}

func (o *Orchestrator) adminListUsers(ctx context.Context) string {
	users, err := o.db.ListUsers(ctx)
	if err != nil {
		return "Couldn't list users."
	}
	var b strings.Builder
	b.WriteString("Users:\n")
	for _, u := range users {
		tags := ""
		if u.IsAdmin {
			tags += " [admin]"
		}
		if u.Disabled {
			tags += " [disabled]"
		}
		fmt.Fprintf(&b, "• #%d %s%s\n", u.ID, u.Username, tags)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (o *Orchestrator) adminAddUser(ctx context.Context, rest string) string {
	f := strings.Fields(rest)
	if len(f) != 2 {
		return "Usage: /admin add <username> <password> (username ≥ 3, password ≥ 8). ⚠️ The password is visible to this channel."
	}
	username, password := f[0], f[1]
	if len(username) < 3 {
		return "Username must be at least 3 characters."
	}
	if len(password) < 8 {
		return "Password must be at least 8 characters."
	}
	if existing, _ := o.db.GetUserByUsername(ctx, username); existing != nil {
		return fmt.Sprintf("Username %q is already taken.", username)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "Couldn't create the user."
	}
	id, err := o.db.CreateUser(ctx, username, hash, false)
	if err != nil {
		return "Couldn't create the user."
	}
	_ = o.db.AddAuditEvent(ctx, id, 0, "auth", "user_create", "by admin (command)", "ok")
	return fmt.Sprintf("✅ Created user #%d %q.\n⚠️ Delete the message above — it contains the password.", id, username)
}

func (o *Orchestrator) adminSetDisabled(ctx context.Context, rest string, disabled bool) string {
	username := strings.TrimSpace(rest)
	if username == "" {
		return "Usage: /admin disable <username>  (or /admin enable <username>)."
	}
	target, err := o.db.GetUserByUsername(ctx, username)
	if err != nil || target == nil {
		return fmt.Sprintf("No user named %q.", username)
	}
	if target.IsAdmin {
		return "Admin accounts can't be disabled."
	}
	if err := o.db.SetUserDisabled(ctx, target.ID, disabled); err != nil {
		return "Couldn't update the user."
	}
	action := "enabled"
	if disabled {
		action = "disabled"
	}
	_ = o.db.AddAuditEvent(ctx, target.ID, 0, "auth", "user_"+action, "by admin (command)", "ok")
	return fmt.Sprintf("✅ User %q %s.", username, action)
}

func (o *Orchestrator) adminResetPw(ctx context.Context, rest string) string {
	f := strings.Fields(rest)
	if len(f) != 2 {
		return "Usage: /admin resetpw <username> <new-password> (≥ 8). ⚠️ The password is visible to this channel."
	}
	username, password := f[0], f[1]
	if len(password) < 8 {
		return "New password must be at least 8 characters."
	}
	target, err := o.db.GetUserByUsername(ctx, username)
	if err != nil || target == nil {
		return fmt.Sprintf("No user named %q.", username)
	}
	if target.IsAdmin {
		return "Admins change their own password with /password or in Settings."
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "Couldn't reset the password."
	}
	if err := o.db.UpdatePassword(ctx, target.ID, hash); err != nil {
		return "Couldn't reset the password."
	}
	_ = o.db.AddAuditEvent(ctx, target.ID, 0, "auth", "password_reset", "by admin (command)", "ok")
	return fmt.Sprintf("✅ Reset password for %q — give them the new one.\n⚠️ Delete the message above.", username)
}

// ── command helpers ─────────────────────────────────────────────────────────

func localFire(rfc3339 string, loc *time.Location) string {
	if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
		return t.In(loc).Format("Mon 15:04")
	}
	return rfc3339
}

func isHHMM(s string) bool { _, err := time.Parse("15:04", s); return err == nil }
func isDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }

func errSuffix(n int) string {
	if n > 0 {
		return fmt.Sprintf(" (%d errors)", n)
	}
	return ""
}

// cmdBots lists the user's Telegram bots with their bound agent and status, so
// they can reference a bot id with /bind.
func (o *Orchestrator) cmdBots(ctx context.Context, userID int64) string {
	bots, err := o.db.ListTelegramBots(ctx, userID)
	if err != nil {
		return "Couldn't read your bots."
	}
	if len(bots) == 0 {
		return "No Telegram bots yet. Add one under Extensions → Telegram bots in the portal."
	}
	names := map[int64]string{}
	if agents, aerr := o.db.ListAgents(ctx, userID); aerr == nil {
		for _, a := range agents {
			names[a.ID] = a.Name
		}
	}
	paired := map[int64]bool{}
	if idents, ierr := o.db.ListIdentitiesByUser(ctx, userID, "telegram"); ierr == nil {
		for _, id := range idents {
			paired[id.BotID] = true
		}
	}
	var b strings.Builder
	b.WriteString("Your Telegram bots:\n")
	for _, bot := range bots {
		agent := "active agent"
		if bot.AgentID != 0 && names[bot.AgentID] != "" {
			agent = names[bot.AgentID]
		}
		uname := ""
		if bot.Username != "" {
			uname = " @" + bot.Username
		}
		status := "enabled"
		if !bot.Enabled {
			status = "disabled"
		}
		if !paired[bot.ID] {
			status += ", unpaired"
		}
		fmt.Fprintf(&b, "• #%d %q%s → %s [%s]\n", bot.ID, bot.Label, uname, agent, status)
	}
	b.WriteString("Rebind with /bind <bot-id> <agent name>  (or /bind <bot-id> none to unbind).")
	return strings.TrimRight(b.String(), "\n")
}

// cmdBind changes a bot's bound agent (or unbinds it). Usage:
// /bind <bot-id> <agent name>  |  /bind <bot-id> none
func (o *Orchestrator) cmdBind(ctx context.Context, userID int64, arg string) string {
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		return "Usage: /bind <bot-id> <agent name>  (or /bind <bot-id> none). See /bots for ids."
	}
	id, perr := strconv.ParseInt(fields[0], 10, 64)
	if perr != nil {
		return "The first argument must be a bot id (a number). See /bots."
	}
	bot, err := o.db.GetTelegramBot(ctx, userID, id)
	if err != nil {
		return fmt.Sprintf("No bot #%d. See /bots for your bots.", id)
	}
	target := strings.TrimSpace(strings.TrimPrefix(arg, fields[0]))

	var agentID int64
	var bound string
	switch strings.ToLower(target) {
	case "none", "active", "unbind", "0", "off":
		agentID, bound = 0, "the active agent (unbound)"
	default:
		a, aerr := o.db.GetAgentByName(ctx, userID, target)
		if aerr != nil || a == nil {
			return fmt.Sprintf("No agent named %q. Use /agent to list your agents.", target)
		}
		agentID, bound = a.ID, a.Name
	}
	if err := o.db.UpdateTelegramBot(ctx, userID, id, bot.Label, agentID, bot.Enabled); err != nil {
		return "Couldn't update that bot."
	}
	_ = o.db.AddAuditEvent(ctx, userID, 0, "channel", "telegram_bot_bind",
		fmt.Sprintf("bot #%d → %s", id, bound), "ok")
	return fmt.Sprintf("Bot %q is now bound to %s. It takes effect within ~30s.", bot.Label, bound)
}

func helpText() string {
	return strings.Join([]string{
		"Commands:",
		"• /agent [name] — show your agents or switch the active one",
		"• /model [name] — show or set the chat model (default, opus, sonnet, haiku)",
		"• /runtime [name] — show or set the access method (channels, sdk, codex)",
		"• /status — show your current agent, access method, model, timezone, delivery",
		"• /timezone [IANA] — show or set your timezone (e.g. Asia/Singapore)",
		"• /delivery [web|telegram] — where scheduled jobs are delivered",
		"• /format [markdown|html|plain] — how Telegram messages are formatted",
		"• /groupreply [mention|all] — in groups, reply only when addressed (default) or to every message",
		"• /bots — list your Telegram bots and their bound agents",
		"• /bind <bot-id> <agent|none> — bind a Telegram bot to an agent",
		"• /jobs — list your scheduled jobs",
		"• /reminders — list your active reminders",
		"• /remind <time> <message> — set a reminder (HH:MM, 'daily HH:MM', or 'YYYY-MM-DD HH:MM')",
		"• /recall <query> — search your memory",
		"• /new — start a fresh conversation (keeps memory & history)",
		"• /usage — recent runs and token usage by type",
		"• /password <current> <new> — change your password (prefer the web portal; the message is exposed on the channel)",
		"• /admin — (admins) manage users — e.g. /admin add <username> <password>; type /admin for the full list (users/add/disable/enable/resetpw)",
		"• /refresh-claude — refresh & verify the Claude login (recover if its token lapsed)",
		"• /help — this list",
		"Anything not starting with one of these is sent to your assistant as usual.",
	}, "\n")
}

func (o *Orchestrator) statusText(ctx context.Context, userID int64) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	avail := ""
	if !o.IsRuntimeAvailable(s.ActiveRuntime) {
		avail = " (not available yet — runs fall back to Claude SDK)"
	}
	agentName := "Assistant"
	if a, err := o.db.GetActiveAgent(ctx, userID); err == nil {
		agentName = a.Name
	}
	return strings.Join([]string{
		"Current configuration:",
		"• Agent: " + agentName,
		"• Access method: " + RuntimeLabel(s.ActiveRuntime) + avail,
		"• Model: " + ModelLabel(modelHint(s.ModelHints, "chat")),
		"• Timezone: " + s.Timezone,
		"• Delivery: " + s.DeliveryChannel,
	}, "\n")
}

func (o *Orchestrator) cmdAgent(ctx context.Context, userID int64, arg string) string {
	if arg == "" {
		agents, err := o.db.ListAgents(ctx, userID)
		if err != nil {
			return "Couldn't list your agents."
		}
		active, _ := o.db.GetActiveAgent(ctx, userID)
		var lines []string
		for _, a := range agents {
			tag := ""
			if a.IsDefault {
				tag += " (default)"
			}
			if active != nil && a.ID == active.ID {
				tag += " ← active"
			}
			lines = append(lines, fmt.Sprintf("• %s — %s%s", a.Name, a.Description, tag))
		}
		return "Agents:\n" + strings.Join(lines, "\n") + "\nSwitch with /agent <name>."
	}
	a, err := o.db.GetAgentByName(ctx, userID, arg)
	if err != nil || a == nil {
		return fmt.Sprintf("No agent named %q. Use /agent to list them.", arg)
	}
	if err := o.db.SetActiveAgent(ctx, userID, a.ID); err != nil {
		return "Failed to switch agent."
	}
	return "Switched to agent: " + a.Name + ". New messages use it."
}

func (o *Orchestrator) cmdModel(ctx context.Context, userID int64, arg string) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	if arg == "" {
		var opts []string
		for _, m := range Models {
			opts = append(opts, m.Alias)
		}
		return fmt.Sprintf("Current model: %s.\nSet with /model <name>. Options: %s.\nYou can also pass a full model id.",
			ModelLabel(modelHint(s.ModelHints, "chat")), strings.Join(opts, ", "))
	}
	id, ok := ResolveModel(arg)
	if !ok {
		// Accept an arbitrary model id too, for forward compatibility.
		id = strings.TrimSpace(arg)
	}
	s.ModelHints = SetChatModel(s.ModelHints, id)
	if err := o.db.UpdateSettings(ctx, s); err != nil {
		return "Failed to save the model change."
	}
	return "Model set to " + ModelLabel(id) + "."
}

func (o *Orchestrator) cmdRuntime(ctx context.Context, userID int64, arg string) string {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return "Couldn't read your settings."
	}
	if arg == "" {
		var lines []string
		for _, rc := range o.RuntimeChoices() {
			tag := ""
			if !rc.Available {
				tag = " (coming soon)"
			}
			if rc.ID == s.ActiveRuntime {
				tag += " ← current"
			}
			lines = append(lines, fmt.Sprintf("• %s — /runtime %s%s", rc.Label, rc.Short, tag))
		}
		return "Access methods:\n" + strings.Join(lines, "\n")
	}
	id, ok := ResolveRuntime(arg)
	if !ok {
		return "Unknown access method. Try: channels, sdk, codex. (/runtime to list.)"
	}
	if !o.IsRuntimeAvailable(id) {
		return RuntimeLabel(id) + " isn't available yet — its adapter isn't built. Available now: " + RuntimeLabel("claude-headless") + "."
	}
	s.ActiveRuntime = id
	if err := o.db.UpdateSettings(ctx, s); err != nil {
		return "Failed to save the access-method change."
	}
	return "Access method set to " + RuntimeLabel(id) + ". Takes effect on your next message."
}

// SetChatModel updates the "chat" key of a model-hints JSON string. Exported so
// the settings UI and the slash-command handler stay in lock-step.
func SetChatModel(existing, modelID string) string {
	m := map[string]string{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &m)
	}
	if strings.TrimSpace(modelID) == "" {
		delete(m, "chat")
	} else {
		m["chat"] = modelID
	}
	b, err := json.Marshal(m)
	if err != nil {
		return existing
	}
	return string(b)
}
