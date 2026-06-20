package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"samwise/internal/orchestrator"
	"samwise/internal/runtime"
	"samwise/internal/scheduler"
	"samwise/internal/telegram"
	"samwise/internal/web"
)

// runServe starts the orchestrator: the web portal now, with the scheduler and
// runtime manager wired in by later MVP steps. It blocks until SIGINT/SIGTERM,
// then shuts down gracefully.
func runServe(_ []string) error {
	d, err := bootstrap()
	if err != nil {
		return err
	}
	defer d.db.Close()

	web.SetVersion(version)
	web.SetUserGuide(userGuideMarkdown)

	// Runtime adapters + orchestrator. MVP registers claude-headless; the
	// channels and codex adapters slot in behind the same interface later.
	headless := runtime.NewClaudeHeadless(d.cfg.ClaudeBin, d.log)
	orch := orchestrator.New(d.cfg, d.db, d.log, d.box, headless)
	// Bring up the in-process, token-scoped core MCP host before serving. Fail
	// loudly if its loopback listener can't bind — a run with no core host gets
	// no memory/job tools, and we never want to silently fall back to spawning
	// the core server under the agent's uid.
	if err := orch.Start(); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              d.cfg.HTTPAddr,
		Handler:           web.New(d.cfg, d.db, d.log, d.box, orch).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Telegram channel. The Manager runs one inbound poller per bot — the optional
	// legacy .env token (unbound, bot id 0) plus every per-user bot in the
	// telegram_bots table (each bound to an agent) — and is the orchestrator's
	// outbound sender, routing each delivery to the right bot. It's always started
	// so portal-added bots come online without a restart; with no env token and no
	// DB bots it simply runs no pollers.
	mgr := telegram.NewManager(d.db, orch, d.box, d.log, d.cfg.TelegramBotToken)
	orch.SetTelegramSender(mgr)
	go mgr.Run(ctx)
	if d.cfg.TelegramBotToken == "" {
		d.log.Info("telegram: no legacy TELEGRAM_BOT_TOKEN; only per-user bots will run")
	}

	// Scheduler: 1-minute tick loop firing due jobs (reminders, agent jobs,
	// maintenance). Stops when ctx is cancelled.
	sched := scheduler.New(d.db, orch, d.log)
	go sched.Run(ctx)
	d.log.Info("scheduler started")

	serverErr := make(chan error, 1)
	go func() {
		d.log.Info("portal listening", "addr", d.cfg.HTTPAddr, "env", d.cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		d.log.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
