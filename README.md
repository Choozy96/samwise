# 🌿 Samwise

> *"I can't carry it for you, but I can carry you."* — your loyal personal assistant.

**Samwise** is a self-hosted, multi-channel personal AI assistant — a **channel
router + memory system + scheduler + runtime-adapter layer** that serves messages
from different platforms to an AI agent harness, maintains long-term per-user
memory, and runs scheduled proactive jobs. Domain features (calendar, tasks,
briefings) are delivered through a user **extension system**, not core code.

> The Go module, binary, and Docker image are named `samwise`.

**Docs:** [Deploy & operate](docs/DEPLOY.md) · [User guide](docs/user-guide.md) (also in-app under **Guide**) · [Security model](SECURITY.md)

> **Status:** MVP complete — a useful single-user assistant.
> The channels runtime, codex-exec, per-user containers, and the reference
> briefing extension come after.

## What works today (MVP)

- **Web portal** — first-run admin setup, argon2id login, sessions, admin user
  management, settings (timezone, runtime, model hints, schedule times, context K/N).
- **Chat** — streaming chat on the `claude-headless` runtime, with full context
  assembly (identity, profile, retrieved memory, rolling summary, transcript) and
  stateless rehydration so any turn is reproducible.
- **Memory** — SQLite semantic + episodic layers with FTS5 retrieval; a core MCP
  server exposing `memory_save/search/forget/list_topics`, `set_timezone`,
  `get_settings`, and `reminder_set/list/cancel`, each bound to the run context.
  Portal memory editor + audit log.
- **Scheduler** — 1-minute tick, materialized next-fire with timezone-change
  recompute, double-fire guard + within-period catch-up, `direct_message` /
  `agent_run` / `maintenance` jobs, retry-once + failure notices. Reminders.
- **Telegram** — long-polling bot, pairing flow, and delivery with chunking +
  retry (set `TELEGRAM_BOT_TOKEN` to enable; web chat works without it).
- **Ops** — nightly SQLite backup (`VACUUM INTO`, rotated), `/healthz`, structured
  JSON logs, Docker image + compose.

## Language: Go (rationale)

The language was deliberately left open. Go was chosen because the core
is a **long-running concurrent service** doing several things at once — child-process
supervision (spawning/managing `claude`/`codex` runs), a scheduler tick loop,
channel pollers, and a web server. Go gives that profile a single static binary,
first-class concurrency, and trivial Docker packaging, with a small auditable
dependency surface — a deliberately boring, auditable, no-framework-heavy
stack. The web portal is stdlib `net/http` + `html/template`; the
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

Deferred: helper **scripts** need per-user container isolation (not yet
built) to run safely; native `.claude/skills` files for the channels runtime are
written when that runtime lands (headless uses context injection today).

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

The image bundles the `claude` CLI. To let the in-container runtime use your
claude.ai auth, mount your host's claude config into the app user's home — see the
commented `volumes:` entry in `docker-compose.yml`. The SQLite DB persists in the
`app-data` named volume.

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
samwise      mcp           core MCP server over stdio (internal; spawned by runtimes)
samwise      version       print version
```

## Layout

```
main.go, cmd_*.go, app.go        command dispatch + shared bootstrap
internal/config                  config + .env / bootstrap-secret loading
internal/applog                  structured (JSON) slog setup
internal/secretbox               AES-GCM encryption chokepoint for DB secrets
internal/store                   SQLite open (WAL) + embedded migrations + DAL
internal/web                     HTTP portal (auth, chat, settings, …)
Dockerfile, docker-compose.yml   main container (named volume for the DB)
```

## License

[MIT](LICENSE) — use it, fork it, ship it.

## Disclaimer

Provided **as is, without warranty** (see the LICENSE). Samwise can run on a
Claude subscription login or an API key — if you point it at a subscription
login, make sure that use complies with **Anthropic's Terms of Service**; you are
responsible for how you operate it. This is an independent project and is not
affiliated with or endorsed by Anthropic.
