# Security model

## Data isolation between users

Every data-access method is scoped to a `user_id` taken from the authenticated
session or the paired channel identity — never from model/agent input. A user
cannot read or modify another user's memory, conversations, jobs, agents,
skills, secrets, or settings. The core MCP server is spawned per run bound to a
single `--user-id`, so the agent's tools operate only on that user's data.

This is covered by tests, e.g. `TestCrossUserIsolation`,
`TestSearchMemoryUserScopedFTS`, and `TestMemorySearchUserScoped`.

## The agent cannot run raw SQL (through the app)

The agent interacts with stored data **only through the core MCP tools**
(`memory_*`, `job_*`, `reminder_*`, …), which call typed, parameterized queries.
No tool accepts SQL. The one free-text input — `memory_search` — is reduced to
quoted alphanumeric tokens (`buildMatch`) and passed as a **bound parameter**, so
it cannot inject SQL or FTS operators (`TestSearchMemoryNoInjection`).

## Secrets

- Per-user secrets and MCP credentials are **encrypted at rest** (AES-256-GCM
  under `MASTER_KEY`, which lives outside the database).
- A run injects only **that user's** secrets into the agent's environment.
- App secrets (`MASTER_KEY`, `SESSION_KEY`, `TELEGRAM_BOT_TOKEN`) are **stripped**
  from the agent's environment (`agentEnv`), so a host tool can't read them — and
  in particular can't use `MASTER_KEY` to decrypt the secrets stored in the DB
  (`TestAgentEnvStripsSecrets`).
- The agent is also instructed never to echo a secret's value.

## Known boundary: host-tool sandboxing (multi-user)

When `ALLOW_AGENT_TOOLS=true` (default in the Docker image), the agent gets a
scoped set of Claude Code built-ins — `Read`, `Glob`, `Grep`, `Bash`, `Write`,
`Edit` — so skills can run their scripts. These tools are **not filesystem-
sandboxed per user**: a determined or prompt-injected agent could read files on
disk, including the SQLite database file. Encrypted secrets stay encrypted (the
key is stripped from its environment), but **other users' plaintext rows**
(memory, messages, usernames, password *hashes*) would be reachable.

By default the agent gets only the scoped file/shell set. A user may opt into
**individual** extra built-in tools (Settings → Agent tools), each validated
against a fixed catalog so only known tools can ever be enabled. Notably
`WebFetch`/`WebSearch` add network **egress** — combined with shell access that's
an exfiltration path, and `WebFetch` can reach internal/metadata endpoints (SSRF)
unless egress is restricted; `Task` (sub-agents) is flagged extra-dangerous. Keep
these off (and the master switch off) on untrusted deployments.

The intended isolation boundary for untrusted multi-user is **per-user
containers** (planned). Until then:

- **Single-user or trusted multi-user (family/team): fine as-is** — the agent
  only ever reaches its own user's data through the tools, and host-tool file
  access exposes data the operator already trusts.
- **Untrusted multi-user:** run with `ALLOW_AGENT_TOOLS=false` (skills' scripts
  won't execute, but the agent has no host tools), or wait for per-user
  containers.

## Reporting

Found a vulnerability? Please open a private report / security advisory on the
repository rather than a public issue.
