package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"samwise/internal/runtime"
	"samwise/internal/store"
)

// ChannelSender delivers a message to a user over an external channel
// (Telegram). The web channel is delivered in-process by appending to the
// conversation, so it needs no sender.
//
// With multiple Telegram bots, delivery must pick the right bot: Send uses the
// user's primary bot, SendAgent prefers the bot bound to a given agent (falling
// back to primary), and SendBot targets one specific bot (e.g. a pairing
// confirmation on the bot the user just messaged).
type ChannelSender interface {
	Send(ctx context.Context, userID int64, text string) error
	SendAgent(ctx context.Context, userID, agentID int64, text string) error
	SendBot(ctx context.Context, userID, botID int64, text string) error
	// SendToChat delivers to an explicit bot+chat (a specific paired chat chosen
	// as a job's delivery destination).
	SendToChat(ctx context.Context, userID, botID, chatID int64, text string) error
}

// SetTelegramSender wires the Telegram delivery sink (MVP step 6). Until set,
// telegram-targeted delivery falls back to web.
func (o *Orchestrator) SetTelegramSender(s ChannelSender) { o.telegram = s }

// TelegramConfigured reports whether Telegram delivery is available.
func (o *Orchestrator) TelegramConfigured() bool { return o.telegram != nil }

// NotifyTelegram sends a one-off Telegram message to a user (e.g. an alert) via
// their primary bot, independent of their preferred delivery channel. No-op if
// Telegram is not configured.
func (o *Orchestrator) NotifyTelegram(ctx context.Context, userID int64, text string) error {
	if o.telegram == nil {
		return nil
	}
	return o.telegram.Send(ctx, userID, text)
}

// NotifyTelegramBot sends a one-off message via a specific bot (e.g. the pairing
// confirmation on the bot the user just linked). botID 0 = the legacy bot.
func (o *Orchestrator) NotifyTelegramBot(ctx context.Context, userID, botID int64, text string) error {
	if o.telegram == nil {
		return nil
	}
	return o.telegram.SendBot(ctx, userID, botID, text)
}

// DeliverToUser delivers text to the user's preferred delivery channel. All
// formatting/chunking/rate-limiting for a channel lives behind its
// sender; the agent has no send tool.
func (o *Orchestrator) DeliverToUser(ctx context.Context, userID int64, text string) error {
	s, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return err
	}
	if s.DeliveryChannel == "telegram" && o.telegram != nil {
		err := o.telegram.Send(ctx, userID, text)
		if err == nil {
			return nil
		}
		// No paired/running bot (or a transient send failure): fall through to web
		// so a reminder is never silently dropped.
		o.log.Warn("telegram delivery failed; delivering to web", "user_id", userID, "err", err)
	}
	// Web delivery: append to the active agent's web conversation so it shows in chat.
	agent, err := o.db.GetActiveAgent(ctx, userID)
	if err != nil {
		return err
	}
	conv, err := o.db.GetOrCreateConversation(ctx, userID, "web", agent.ID)
	if err != nil {
		return err
	}
	_, err = o.db.AddMessage(ctx, conv.ID, userID, "web", "assistant", text)
	return err
}

// DeliverRunResult delivers an agent_run result to the user's channel. The
// reply is ALREADY persisted to the conversation by Dispatch, so for the web
// channel this is a no-op (it's visible in chat) — avoiding a double-store. For
// Telegram it routes to the bot bound to the run's agent (agentName ""/unknown
// => the user's primary bot).
func (o *Orchestrator) DeliverRunResult(ctx context.Context, userID int64, agentName, delivery, text string) error {
	switch {
	case strings.HasPrefix(delivery, "tg:"):
		// A specific paired chat chosen for this job.
		if botID, chatID, ok := parseTGDelivery(delivery); ok && o.telegram != nil {
			return o.telegram.SendToChat(ctx, userID, botID, chatID, text)
		}
		return o.postToWebChat(ctx, userID, agentName, text) // fallback if unavailable
	case delivery == "web":
		return o.postToWebChat(ctx, userID, agentName, text)
	default:
		// "" → the user's default delivery channel.
		s, err := o.db.GetSettings(ctx, userID)
		if err != nil {
			return err
		}
		if s.DeliveryChannel == "telegram" && o.telegram != nil {
			if agentID := o.agentIDForName(ctx, userID, agentName); agentID != 0 {
				return o.telegram.SendAgent(ctx, userID, agentID, text)
			}
			return o.telegram.Send(ctx, userID, text)
		}
		return o.postToWebChat(ctx, userID, agentName, text)
	}
}

// parseTGDelivery parses "tg:<botID>:<chatID>".
func parseTGDelivery(s string) (botID, chatID int64, ok bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 || parts[0] != "tg" {
		return 0, 0, false
	}
	b, e1 := strconv.ParseInt(parts[1], 10, 64)
	c, e2 := strconv.ParseInt(parts[2], 10, 64)
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return b, c, true
}

// postToWebChat delivers a scheduled-job result into the user's interactive web
// conversation so it's visible there (isolated task runs write to a hidden task
// thread, so the result must be posted here explicitly).
func (o *Orchestrator) postToWebChat(ctx context.Context, userID int64, agentName, text string) error {
	agentID := o.agentIDForName(ctx, userID, agentName)
	if agentID == 0 {
		if a, err := o.db.GetActiveAgent(ctx, userID); err == nil {
			agentID = a.ID
		}
	}
	conv, err := o.db.GetOrCreateConversation(ctx, userID, "web", agentID)
	if err != nil {
		return err
	}
	_, err = o.db.AddMessage(ctx, conv.ID, userID, "web", "assistant", text)
	return err
}

// agentIDForName resolves a job's agent name to an id, falling back to the user's
// active agent. Returns 0 only if neither can be resolved.
func (o *Orchestrator) agentIDForName(ctx context.Context, userID int64, name string) int64 {
	if name != "" {
		if a, err := o.db.GetAgentByName(ctx, userID, name); err == nil && a != nil {
			return a.ID
		}
	}
	if a, err := o.db.GetActiveAgent(ctx, userID); err == nil && a != nil {
		return a.ID
	}
	return 0
}

// RunAgentJob executes a scheduled agent_run as a specific agent (agentName ""
// = the user's active agent): it dispatches the job prompt with full
// memory/context but does not store the prompt as a user message. The caller
// delivers the returned text.
func (o *Orchestrator) RunAgentJob(ctx context.Context, userID int64, prompt, agentName string) (*runtime.Result, error) {
	user, err := o.db.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var agent *store.Agent
	if agentName != "" {
		agent, _ = o.db.GetAgentByName(ctx, userID, agentName) // nil -> Dispatch uses active agent
	}
	return o.Dispatch(ctx, DispatchRequest{
		User:             user,
		Channel:          "web",
		Agent:            agent,
		UserMessage:      prompt,
		StoreUserMessage: false,
		Isolated:         true, // its own thread, no chat transcript, no merge with live messages
	}, nil)
}

// RunAgentJobWithSkill is RunAgentJob with an optional skill: when skillName is
// set and resolves, that skill's current content is loaded fresh and prepended
// to the prompt, so editing the skill changes the job's behavior without
// touching the job.
func (o *Orchestrator) RunAgentJobWithSkill(ctx context.Context, userID int64, prompt, skillName, agentName string) (*runtime.Result, error) {
	if skillName != "" {
		if sk, err := o.db.GetSkillByName(ctx, userID, skillName); err == nil && sk != nil && strings.TrimSpace(sk.Content) != "" {
			skillBlock := sk.Content
			if sk.HasBundle {
				skillBlock += fmt.Sprintf("\n\n(This skill's files — scripts/assets — are in: %s.)",
					o.SkillBundleDir(userID, sk.Name))
			}
			prompt = "Follow this skill:\n\n" + skillBlock + "\n\n---\n\n" + prompt
			_ = o.db.AddAuditEvent(ctx, userID, 0, "skill", sk.Name, "loaded into agent run", "ok")
		}
	}
	return o.RunAgentJob(ctx, userID, prompt, agentName)
}

// runAgentJobSilent runs an internal prompt with full context but writes nothing
// back to the conversation (no messages, no session update) — for distillation.
func (o *Orchestrator) runAgentJobSilent(ctx context.Context, userID int64, prompt string) (*runtime.Result, error) {
	user, err := o.db.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return o.Dispatch(ctx, DispatchRequest{
		User:             user,
		Channel:          "web",
		UserMessage:      prompt,
		StoreUserMessage: false,
		Silent:           true,
	}, nil)
}

// SaveIntradayDistillation refreshes the running note for today incrementally,
// folding recent activity into the existing note (which the agent sees via the
// always-loaded recent memory). Cheap — runs every few hours.
func (o *Orchestrator) SaveIntradayDistillation(ctx context.Context, userID int64, localDate string) error {
	prompt := fmt.Sprintf("Update my running memory note for today (%s). If a note for today is already "+
		"in your memory, refine and extend it with anything new from our recent conversation; otherwise "+
		"write a fresh short note. Capture decisions, commitments, progress, and anything worth remembering. "+
		"Output only the updated note.", localDate)
	res, err := o.runAgentJobSilent(ctx, userID, prompt)
	if err != nil {
		return err
	}
	if res == nil || strings.TrimSpace(res.FinalText) == "" {
		return nil
	}
	return o.db.UpsertEpisodic(ctx, userID, "day", localDate, res.FinalText)
}

// SaveDailyDistillation is the authoritative end-of-day pass: it re-reads the
// whole local day's messages and writes a consolidated summary, replacing the
// day's running note.
func (o *Orchestrator) SaveDailyDistillation(ctx context.Context, userID int64, localDate string) error {
	settings, err := o.db.GetSettings(ctx, userID)
	if err != nil {
		return err
	}
	startUTC, endUTC := localDayRangeUTC(localDate, settings.Timezone)
	msgs, err := o.db.MessagesForUserInRange(ctx, userID, startUTC, endUTC)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil // nothing happened today
	}
	prompt := fmt.Sprintf("Here is everything from my day (%s), oldest first:\n\n%s\n\n"+
		"Write a consolidated summary of the day in 4-8 sentences — decisions, commitments, progress, "+
		"and anything worth remembering long-term. Output only the summary.", localDate, transcriptDigest(msgs, 12000))
	res, err := o.runAgentJobSilent(ctx, userID, prompt)
	if err != nil {
		return err
	}
	if res == nil || strings.TrimSpace(res.FinalText) == "" {
		return nil
	}
	if err := o.db.UpsertEpisodic(ctx, userID, "day", localDate, res.FinalText); err != nil {
		return err
	}
	// Tell the user what was distilled (unless they've turned notifications off).
	// The distillation run itself is silent; this is a separate user-facing note.
	if settings.DistillNotify {
		note := fmt.Sprintf("📝 Saved today's memory note (%s):\n\n%s", localDate, strings.TrimSpace(res.FinalText))
		if derr := o.DeliverToUser(ctx, userID, note); derr != nil {
			o.log.Warn("distillation notify failed", "user_id", userID, "err", derr)
		}
	}
	return nil
}

// localDayRangeUTC returns the [start, end) UTC timestamps ('YYYY-MM-DD HH:MM:SS')
// bounding a local calendar date in tz.
func localDayRangeUTC(localDate, tz string) (string, string) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	start, perr := time.ParseInLocation("2006-01-02", localDate, loc)
	if perr != nil {
		start = time.Now().In(loc)
	}
	const f = "2006-01-02 15:04:05"
	return start.UTC().Format(f), start.AddDate(0, 0, 1).UTC().Format(f)
}

// transcriptDigest renders messages as "role: content" lines, truncated to about
// maxChars so the distillation prompt stays bounded.
func transcriptDigest(msgs []store.Message, maxChars int) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		if b.Len() > maxChars {
			b.WriteString("…(truncated)\n")
			break
		}
	}
	return b.String()
}

// DB exposes the store for scheduler-side reads/writes that don't warrant a
// dedicated orchestrator method (e.g. listing due jobs is on the scheduler).
func (o *Orchestrator) DB() *store.DB { return o.db }
