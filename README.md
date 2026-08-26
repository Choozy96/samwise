# 🌿 Samwise

> *"I can't carry it for you, but I can carry you."* — your loyal personal assistant.

**Samwise** is a self-hosted, multi-channel personal AI assistant — a **channel
router + memory system + scheduler + runtime-adapter layer** that serves messages
from different platforms to an AI agent harness, maintains long-term per-user
memory, and runs scheduled proactive jobs. Domain features (calendar, tasks,
briefings) are delivered through a user **extension system**, not core code.

> The Go module, binary, and Docker image are named `samwise`.

**Docs:** [Deploy & operate](docs/DEPLOY.md) · [User guide](docs/user-guide.md) (also in-app under **Guide**) · [Security model](SECURITY.md)

> **Status:** v0.3.8 released; v0.4.0 (Slack + Discord channels) in development.
> Deployed and running on a GCP VPS. Still ahead: the channels runtime,
> codex-exec, and per-user containers.

## What works today

- **Four channels** — web portal chat, **Telegram** (multiple bots per user,
  optional agent binding, groups), **Slack** (Socket Mode, threads), and
  **Discord** (Gateway, guild channels + DMs). One conversation per agent,
  shared across channels; delivery routes to a configurable default
  destination, including a specific paired chat.
- **Multi-agent** — several agents per user, each with a soul (system prompt)
  and its own **model** picked from an admin-configurable catalog; `/agent`
  and `/model` in chat, Agents page in the portal.
- **Memory** — SQLite semantic + episodic layers with FTS5; two-tier retrieval
  (recent episodic always loaded + top-K semantic); end-of-day + incremental
  intraday **distillation**; a browsable/searchable memory page. Core MCP tools
  (`memory_*`, `reminder_*`, `job_*`, `set_timezone`, `update_settings`, …)
  are served **in-process** by the orchestrator, token-scoped per run.
- **Multi-user isolation** — each agent run drops to a per-user OS uid
  (workspaces 0700); the MCP host resolves the user server-side from a per-run
  bearer token, so the model can never act as another user. Group senders who
  aren't DM-paired get read-only runs; per-skill/per-tool **audience** controls
  and a sandboxed `skill_run` cover the rest.
- **Extensions** — DB-backed **skills** (portal editor, ZIP import, the agent
  can manage its own), per-user **MCP registry** (stdio/http, encrypted creds),
  per-user **secrets** injected as env vars, OAuth secrets auto-refreshed.
- **Scheduler** — cron jobs with per-job IANA timezone, timezone-change
  recompute, catch-up + double-fire guards, reminders, failure notices; the
  agent manages its own jobs via MCP tools.
- **Files** — workspace browser with in-browser edits and uploads; the agent
  can create and send files back to you (`send_file`).
- **Admin** — user management, password reset, **usage & cost dashboard**
  (by user/model/period), model catalog, audit log of every message/tool
  call/job fire.
- **Ops** — Docker image (linux/amd64-pinned builds via `make`), nightly SQLite
  backups (`VACUUM INTO`, rotated), `/healthz`, structured JSON logs, version
  stamped from `git describe`.

## Language: Go (rationale)

Go was chosen because the core
is a **long-running concurrent service** doing several things at once — child-process
supervision (spawning/managing `claude`/`codex` runs), a scheduler tick loop,
channel pollers, and a web server. Go gives that profile a single static binary,
first-class concurrency, and trivial Docker packaging, with a small auditable
dependency surface — matching the spec's "boring, auditable, no framework-heavy
stack" mandate. The web portal is stdlib `net/http` + `html/template`; the
database driver is pure-Go `modernc.org/sqlite` (no cgo, FTS5 support).

## Database hosting

SQLite is **embedded** — a single file opened in-process, not a separate server.
There is no managed/RDS dependency and no second container: the DB lives as a file
on a Docker named volume (`/data/app.db` in-container, `./data/app.db` for native
dev), configurable via `DB_PATH`. A client/server engine (Postgres-in-a-container)
was explicitly considered and rejected — it would forfeit the pure-Go build, FTS5
retrieval, the `sqlite-vec` upgrade path, and single-file portability, for
infrastructure this single-owner system doesn't need.

## Secrets

Two tiers:

- **Bootstrap secrets** — `MASTER_KEY`, `SESSION_KEY`, Telegram bot token — live
  in the env file (`.env`, `chmod 600`), never in the repo or DB.
- **Everything else** — MCP credentials/API tokens entered via the portal — is
  stored in the SQLite DB **encrypted with AES-256-GCM under `MASTER_KEY`**. So a
  stolen DB file or backup is useless without the env file. There is no Vault/KMS;
  all encryption flows through one chokepoint (`internal/secretbox`) so a future
  swap is localized.

## Extensions

Domain powers are added through extensions, not core code. Manage them
on the **Extensions** page in the portal.

- **MCP servers** — register external tool servers (Google Calendar, Todoist,
  Notion, …) as `stdio` (`command` + args) or `http` (`url`). Credentials are
  entered as `KEY=value` lines and stored **AES-GCM-encrypted under `MASTER_KEY`**
  (decrypted only in memory when composing a run). Enabled servers merge into
  every run alongside the core server, and their tools are pre-allowed so
  unattended runs never stall on a prompt.
  - **Note on `npx` servers:** a stdio server launched via `npx -y <pkg>`
    downloads on first use, which can exceed the harness's MCP startup timeout
    and silently not connect. Pre-install it (`npm i -g <pkg>`, or run it once to
    populate the npx cache) so it connects reliably.
- **Skills** — markdown instruction files that shape behavior. "Always on" skills
  are injected into every chat; others are followed when relevant or when a job
  names one. Edit a skill to change behavior — no code change.
- **Morning briefing** (reference extension) — one click on the Extensions page
  creates a `morning-briefing` skill and a daily `agent_run` job that assembles
  your reminders + memory (+ any registered tools) and delivers at your configured
  time. Degrades gracefully when you have no external tools.

Skills can ship **scripts**: paired users run them through the harness's scoped
tools under per-user uid isolation, and unregistered group members can invoke a
skill's vetted entrypoint via the sandboxed `skill_run` (no shell, minimal env,
timeout + output caps). Native `.claude/skills` files for the channels runtime
are written when that runtime lands (headless uses context injection today).

## Development (native, Windows/macOS/Linux)

Requires **Go 1.26+** and (for actually talking to the assistant) the `claude`
CLI on `PATH`.

```sh
cp .env.example .env        # fill in secrets; dev auto-generates a session key
go run . migrate            # create/upgrade the SQLite schema
go run . serve              # start the portal on :8080
curl localhost:8080/healthz # {"status":"ok"}
```

The dev loop is native `go run` — fast rebuilds, your host `claude` auth already
works. The Docker artifacts below are a tested deliverable for deployment, not the
inner loop.

## Running in Docker (deployment)

```sh
cp .env.example .env        # set MASTER_KEY and SESSION_KEY (openssl rand -base64 32)
docker compose up --build
```

The image bundles the `claude` CLI. The in-container runtime needs its own
claude.ai login (a mounted credential dir — see `docs/DEPLOY.md`; don't share a
credential with a machine you actively use, the OAuth refresh tokens rotate each
other out). The SQLite DB persists in the `app-data` named volume.

## Subscription ToS note

This is a single-owner personal tool serving ≤5 trusted household members on the
owner's Claude (and later ChatGPT) subscriptions, driving the official CLIs
programmatically. Verify the current Anthropic (and, when `codex-exec` lands,
OpenAI) terms for subscription-backed programmatic use before relying on it; record
the outcome of that check here. **[Owner: confirm and date this before shipping.]**

## Commands

```
samwise      serve         run the orchestrator (default)
samwise      migrate       apply database migrations and exit
samwise      create-user   create a portal user (first user is admin)
                           e.g. create-user --username alice --password 's3cret!!'
samwise      mcp           legacy stdio core-MCP mode (superseded by the orchestrator's
                           in-process MCP host in v0.3.0; kept for debugging)
samwise      version       print version
```

## Layout

```
main.go, cmd_*.go, app.go        command dispatch + shared bootstrap
internal/config                  config + .env / bootstrap-secret loading
internal/applog                  structured (JSON) slog setup
internal/auth                    argon2id password hashing
internal/secretbox               AES-GCM encryption chokepoint for DB secrets
internal/store                   SQLite open (WAL) + embedded migrations + DAL
internal/orchestrator            core: dispatch, context assembly, delivery,
                                 MCP host, commands, channel seam (Address)
internal/runtime                 agent runtime adapters (claude-headless) +
                                 per-user uid isolation
internal/mcpserver               core MCP tools (memory, jobs, skills, files, …)
internal/scheduler, internal/schedule   job tick loop + cron/timezone math
internal/telegram, internal/slack, internal/discord   channel adapters
internal/web                     HTTP portal (auth, chat, agents, memory, …)
Dockerfile, docker-compose.yml   main container (named volume for the DB)
Makefile                         version-stamped, linux/amd64-pinned image builds
```
