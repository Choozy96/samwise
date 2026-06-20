// Package scheduler runs the 1-minute tick loop that fires due jobs.
// It implements the timezone edge-case handling: the double-fire guard and the
// within-current-period catch-up window.
package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/schedule"
	"samwise/internal/store"
)

// lateGrace is how long after a fire moment a job is still considered "on time".
// Beyond it, a non-catch-up job that missed its slot is rolled to the next
// period instead of firing late.
const lateGrace = 2 * time.Minute

const (
	// oauthRefreshEvery is how often we proactively refresh OAuth credentials.
	oauthRefreshEvery = 15 * time.Minute
	// reauthAlertCooldown rate-limits the "credential needs re-auth" alert so a
	// persistently dead refresh token doesn't message the user every cycle.
	reauthAlertCooldown = 6 * time.Hour
	// distillCheckEvery is how often we evaluate whether to update a day's memory;
	// intradayEvery is the minimum gap between intraday (incremental) updates.
	distillCheckEvery = time.Hour
	intradayEvery     = 4 * time.Hour
)

// Scheduler evaluates and fires jobs.
type Scheduler struct {
	db   *store.DB
	orch *orchestrator.Orchestrator
	log  *slog.Logger
	// lastReauthAlert rate-limits per-credential re-auth alerts. Keyed
	// "userID:secretName"; in-memory (re-alerts after a restart, which is fine).
	lastReauthAlert map[string]time.Time
	// Distillation state (in-memory): last message id distilled, last intraday
	// update time, and the last local date fully consolidated — all per user.
	lastMsg      map[int64]int64
	lastIntraday map[int64]time.Time
	lastFullDate map[int64]string
}

// New constructs a Scheduler.
func New(db *store.DB, orch *orchestrator.Orchestrator, log *slog.Logger) *Scheduler {
	return &Scheduler{
		db: db, orch: orch, log: log,
		lastReauthAlert: map[string]time.Time{},
		lastMsg:         map[int64]int64{},
		lastIntraday:    map[int64]time.Time{},
		lastFullDate:    map[int64]string{},
	}
}

// Run ticks every minute (firing due jobs) plus a slower tick that refreshes
// OAuth credentials, until ctx is cancelled. It evaluates once on start so a
// just-booted orchestrator catches jobs due within the current period.
func (s *Scheduler) Run(ctx context.Context) {
	s.tick(ctx, time.Now())
	s.refreshOAuth(ctx, time.Now())
	t := time.NewTicker(time.Minute)
	rt := time.NewTicker(oauthRefreshEvery)
	dt := time.NewTicker(distillCheckEvery)
	defer t.Stop()
	defer rt.Stop()
	defer dt.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.tick(ctx, now)
		case now := <-rt.C:
			s.refreshOAuth(ctx, now)
		case now := <-dt.C:
			s.runDistillation(ctx, now)
		}
	}
}

// runDistillation keeps each user's "today" episodic note fresh: an incremental
// update every ~4h while there's new activity, and one authoritative full-day
// consolidation at/after the user's distillation time.
func (s *Scheduler) runDistillation(ctx context.Context, now time.Time) {
	users, err := s.db.ListUsers(ctx)
	if err != nil {
		s.log.Error("scheduler: list users for distillation", "err", err)
		return
	}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		settings, err := s.db.GetSettings(ctx, u.ID)
		if err != nil {
			continue
		}
		loc := schedule.LocationFor("user_local", "", settings.Timezone)
		localNow := now.In(loc)
		localDate := localNow.Format("2006-01-02")

		latest, _ := s.db.LatestMessageID(ctx, u.ID)
		if latest <= s.lastMsg[u.ID] {
			continue // no new activity since the last distillation
		}

		// Overnight: one full-day consolidation per local day, at/after the
		// distillation time.
		if s.lastFullDate[u.ID] != localDate && pastDistillTime(localNow, settings.DistillationTime) {
			if err := s.orch.SaveDailyDistillation(ctx, u.ID, localDate); err != nil {
				s.log.Error("scheduler: daily distillation", "user_id", u.ID, "err", err)
				continue
			}
			s.lastFullDate[u.ID] = localDate
			s.lastIntraday[u.ID] = now
			s.lastMsg[u.ID] = latest
			continue
		}
		// Intraday: incremental refresh every ~4h.
		if now.Sub(s.lastIntraday[u.ID]) >= intradayEvery {
			if err := s.orch.SaveIntradayDistillation(ctx, u.ID, localDate); err != nil {
				s.log.Error("scheduler: intraday distillation", "user_id", u.ID, "err", err)
				continue
			}
			s.lastIntraday[u.ID] = now
			s.lastMsg[u.ID] = latest
		}
	}
}

// pastDistillTime reports whether the user's local time is at or past their
// configured end-of-day distillation time (default ~23:00 if unset/bad).
func pastDistillTime(localNow time.Time, hhmm string) bool {
	t, err := time.Parse("15:04", strings.TrimSpace(hhmm))
	if err != nil {
		return localNow.Hour() >= 23
	}
	return localNow.Hour() > t.Hour() || (localNow.Hour() == t.Hour() && localNow.Minute() >= t.Minute())
}

// refreshOAuth refreshes every user's oauth-kind secrets and alerts (rate-limited)
// when a refresh token is dead and the user must re-authenticate.
func (s *Scheduler) refreshOAuth(ctx context.Context, now time.Time) {
	users, err := s.db.ListUsers(ctx)
	if err != nil {
		s.log.Error("scheduler: list users for oauth refresh", "err", err)
		return
	}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		for _, name := range s.orch.RefreshOAuthSecrets(ctx, u.ID) {
			key := strconv.FormatInt(u.ID, 10) + ":" + name
			if last, ok := s.lastReauthAlert[key]; ok && now.Sub(last) < reauthAlertCooldown {
				continue
			}
			s.lastReauthAlert[key] = now
			_ = s.orch.DeliverToUser(ctx, u.ID,
				"⚠️ The credential \""+name+"\" couldn't be refreshed — its OAuth login has expired or was revoked. "+
					"Re-authenticate and update it under Extensions → Secrets so scheduled tasks keep working.")
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	jobs, err := s.db.DueJobs(ctx, now.UTC().Format(time.RFC3339))
	if err != nil {
		s.log.Error("scheduler: due jobs query", "err", err)
		return
	}
	for _, j := range jobs {
		s.process(ctx, j, now)
	}
}

// process applies the period guard / catch-up rules to one due job, fires it if
// warranted, and materializes its next fire time.
func (s *Scheduler) process(ctx context.Context, j store.Job, now time.Time) {
	spec, err := schedule.Parse(j.ScheduleSpec)
	if err != nil {
		s.log.Error("scheduler: bad schedule_spec, disabling job", "job_id", j.ID, "spec", j.ScheduleSpec, "err", err)
		_ = s.db.SetJobEnabled(ctx, j.UserID, j.ID, false)
		return
	}
	settings, err := s.db.GetSettings(ctx, j.UserID)
	if err != nil {
		s.log.Error("scheduler: settings", "job_id", j.ID, "err", err)
		return
	}
	loc := schedule.LocationFor(j.TZMode, j.TZRef, settings.Timezone)

	fireTime, err := time.Parse(time.RFC3339, j.NextFireUTC)
	if err != nil {
		s.log.Error("scheduler: bad next_fire_utc", "job_id", j.ID, "err", err)
		s.advance(ctx, j, spec, loc, now, j.LastFiredPeriod)
		return
	}
	firePeriod := schedule.PeriodKey(spec, fireTime, loc)
	nowPeriod := schedule.PeriodKey(spec, now, loc)
	late := now.Sub(fireTime) > lateGrace

	switch {
	case firePeriod != "once" && firePeriod == j.LastFiredPeriod:
		// Double-fire guard: already fired this period (e.g. a tz
		// change pulled the moment back). Advance without firing.
		s.advance(ctx, j, spec, loc, now, j.LastFiredPeriod)
		return
	case firePeriod != "once" && firePeriod != nowPeriod:
		// The scheduled moment belongs to a past period (downtime). Catch-up
		// only applies within the current period — do not replay backlog.
		s.advance(ctx, j, spec, loc, now, j.LastFiredPeriod)
		return
	case late && !j.CatchUp:
		// Missed the slot and not a catch-up job: roll to the next occurrence.
		if firePeriod == "once" {
			_ = s.db.SetJobEnabled(ctx, j.UserID, j.ID, false)
			return
		}
		s.advance(ctx, j, spec, loc, now, j.LastFiredPeriod)
		return
	}

	s.fire(ctx, j, settings, now, loc)
	s.advance(ctx, j, spec, loc, now, firePeriod)
}

// advance materializes the next fire time, disabling one-shot jobs that have no
// future occurrence.
func (s *Scheduler) advance(ctx context.Context, j store.Job, spec schedule.Spec, loc *time.Location, now time.Time, lastPeriod string) {
	next, ok := schedule.NextFireUTC(spec, loc, now)
	if !ok {
		_ = s.db.SetJobEnabled(ctx, j.UserID, j.ID, false)
		_ = s.db.UpdateJobSchedule(ctx, j.ID, "", lastPeriod)
		return
	}
	if err := s.db.UpdateJobSchedule(ctx, j.ID, next.UTC().Format(time.RFC3339), lastPeriod); err != nil {
		s.log.Error("scheduler: update schedule", "job_id", j.ID, "err", err)
	}
}

// fire dispatches a job by type.
func (s *Scheduler) fire(ctx context.Context, j store.Job, settings *store.Settings, now time.Time, loc *time.Location) {
	s.log.Info("scheduler: firing job", "job_id", j.ID, "type", j.Type, "name", j.Name)
	name := j.Name
	if name == "" {
		name = j.Type
	}
	_ = s.db.AddAuditEvent(ctx, j.UserID, 0, "job", name, j.Type+" @ "+j.ScheduleSpec, "fired")
	switch j.Type {
	case "direct_message":
		s.fireDirectMessage(ctx, j)
	case "agent_run":
		s.fireAgentRun(ctx, j)
	case "maintenance":
		s.fireMaintenance(ctx, j, now, loc)
	default:
		s.log.Error("scheduler: unknown job type", "job_id", j.ID, "type", j.Type)
	}
}

func (s *Scheduler) fireDirectMessage(ctx context.Context, j store.Job) {
	var p struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	if p.Message == "" {
		return
	}
	if err := s.orch.DeliverToUser(ctx, j.UserID, p.Message); err != nil {
		// A dropped reminder is this app's worst failure — surface it.
		s.log.Error("scheduler: direct_message delivery failed", "job_id", j.ID, "err", err)
	}
}

func (s *Scheduler) fireAgentRun(ctx context.Context, j store.Job) {
	var p struct {
		Prompt   string `json:"prompt"`
		Skill    string `json:"skill"`    // optional: load this skill's content into the run
		Agent    string `json:"agent"`    // optional: run as this named agent
		Delivery string `json:"delivery"` // "" default | "web" | "tg:<botID>:<chatID>"
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	if p.Prompt == "" {
		s.log.Error("scheduler: agent_run with empty prompt", "job_id", j.ID)
		return
	}
	// Retry once, then a one-line failure notice.
	res, err := s.orch.RunAgentJobWithSkill(ctx, j.UserID, p.Prompt, p.Skill, p.Agent)
	if err != nil {
		s.log.Warn("scheduler: agent_run failed, retrying once", "job_id", j.ID, "err", err)
		res, err = s.orch.RunAgentJobWithSkill(ctx, j.UserID, p.Prompt, p.Skill, p.Agent)
	}
	if err != nil {
		s.log.Error("scheduler: agent_run failed after retry", "job_id", j.ID, "err", err)
		name := j.Name
		if name == "" {
			name = "Scheduled job"
		}
		// An auth failure (e.g. the Claude subscription token can no longer
		// refresh) won't fix itself on the next run — tell the user to re-auth
		// rather than implying a transient retry.
		if isAuthFailure(err) {
			_ = s.orch.DeliverToUser(ctx, j.UserID, "⚠️ "+name+" failed: the assistant's login (Claude subscription) "+
				"isn't valid anymore. Re-authenticate `claude` (refresh its credentials) so scheduled tasks resume.")
		} else {
			_ = s.orch.DeliverToUser(ctx, j.UserID, "⚠️ "+name+" failed (runtime error). Will try again next time.")
		}
		return
	}
	if res.FinalText != "" {
		// The reply is already persisted to the conversation by Dispatch; this
		// pushes it to an external channel without re-storing it (web is a no-op).
		// Route to the bot bound to this job's agent, if any.
		if err := s.orch.DeliverRunResult(ctx, j.UserID, p.Agent, p.Delivery, res.FinalText); err != nil {
			s.log.Error("scheduler: agent_run delivery failed", "job_id", j.ID, "err", err)
		}
	}
}

func (s *Scheduler) fireMaintenance(ctx context.Context, j store.Job, now time.Time, loc *time.Location) {
	var p struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	switch p.Kind {
	case "distillation":
		localDate := now.In(loc).Format("2006-01-02")
		if err := s.orch.SaveDailyDistillation(ctx, j.UserID, localDate); err != nil {
			s.log.Error("scheduler: distillation failed", "job_id", j.ID, "err", err)
		}
	case "backup":
		if err := s.orch.Backup(ctx, now); err != nil {
			s.log.Error("scheduler: backup failed", "job_id", j.ID, "err", err)
		}
	default:
		s.log.Warn("scheduler: unknown maintenance kind", "job_id", j.ID, "kind", p.Kind)
	}
}

// isAuthFailure heuristically detects a credential/authentication failure (as
// opposed to a transient runtime error) from a run error message.
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, sig := range []string{"401", "invalid authentication", "authentication_error",
		"invalid_grant", "unauthorized", "oauth", "expired token", "could not refresh"} {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
