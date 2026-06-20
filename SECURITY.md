# Security model

## Data isolation between users

Every data-access method is scoped to a `user_id` taken from the authenticated
session or the paired channel identity — never from model/agent input. A user
cannot read or modify another user's memory, conversations, jobs, agents,
skills, secrets, or settings. The core MCP server is spawned per run bound to a
single `--user-id`, so the agent's tools operate only on that user's data.

This is covered by tests, e.g. `TestCrossUserIsolation`,
`TestSearchMemoryUserScopedFTS`, and `TestMemorySearchUserScoped`.

## Group chats: write operations are gated to registered users

A Telegram group is paired to one **owner**, and the agent acts as that owner
(their memory, jobs, tools). Anyone in the group can chat/read, but a message
only gets **write** access — the core MCP write tools (`memory_save`,
`job_*`, `reminder_set/cancel`, `set_timezone`) and write-capable built-in
tools (`Bash`/`Write`/`Edit`) — when the **sender's own Telegram account is
registered** (DM-paired) with the assistant. An unregistered member's run is
read-only, so a stranger can't mutate the owner's data or run commands as them
(`TestTelegramUserIsPaired`). The owner must DM-pair their own Telegram id to
write in their own group.

The core MCP server runs **inside the trusted orchestrator**, not as a child of
the agent. The agent reaches it over a loopback HTTP endpoint, and which user a
request acts as is fixed by a random **per-run bearer token** resolved
server-side — never from agent input. So a run can only ever touch its own
user's data; there is no `--user-id` to change (`TestMCPHostTokenScoping`,
`TestMCPHostRejectsBadToken`). The token + any decrypted tool credentials travel
in a `0600` per-run file, never on the command line (`/proc/<pid>/cmdline` is
world-readable).

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

## Host-tool isolation between users (`AGENT_ISOLATION`)

When `ALLOW_AGENT_TOOLS=true` (default in the Docker image), the agent gets a
scoped set of Claude Code built-ins — `Read`, `Glob`, `Grep`, `Bash`, `Write`,
`Edit` — so skills can run their scripts. With a shell, tool-level path checks
aren't enough; isolation is enforced at the **OS level**.

With `AGENT_ISOLATION=true` (default in prod), the orchestrator runs as root and
drops **each agent run to a distinct, unprivileged per-user uid** (`AGENT_UID_BASE
+ userID`). The kernel then bounds everything the agent's tools can touch:

- **The database is unreachable.** `app.db` and its `-wal`/`-shm` sidecars are
  `0600` owned by root; a restrictive umask keeps new files owner-only. The agent
  uid can neither read the DB nor re-run the `mcp` binary against it.
- **Other users' workspaces are invisible.** `/data` and `/data/workspaces` are
  `0711` (traverse, not list — no enumerating siblings) and each workspace is
  `0700` owned by its own run uid. User 2 can reach only `/data/workspaces/2`.
- **The claude runtime's config is per-user too.** Each run gets its own
  `CLAUDE_CONFIG_DIR`/`HOME` inside its `0700` workspace, so claude's transcripts
  and session state are private to that uid — not shared and cross-readable.
- The only **shared** piece is the **claude.ai credential** (one subscription for
  all users, by design): it's symlinked into each per-user config dir from a
  canonical group-readable/writable file, so a run can authenticate and refresh
  the token, but that group has no path to the root-owned DB.

A user may still opt into **individual** extra built-in tools (Settings → Agent
tools), each validated against a fixed catalog. `WebFetch`/`WebSearch` add network
**egress** — with shell access that's an exfiltration path, and `WebFetch` can
reach internal/metadata endpoints (SSRF) unless egress is restricted; `Task`
(sub-agents) is flagged extra-dangerous.

**Requirements / residuals (honest):**

- Isolation needs **root on Linux** (the container provides it). Off Linux or
  when not root — e.g. native dev — it's **disabled with a loud warning** and the
  app falls back to the old single-uid behavior. Set `AGENT_ISOLATION=false` to
  opt out explicitly (the app then gosu-drops to one unprivileged uid; the agent
  shares it, so there's no per-user isolation).
- It isolates the **filesystem**, not the network or process table, and it does
  **not** hide unrelated world-readable system files (`/etc/passwd`, `/usr`, …) —
  those hold no user data. Hiding the whole filesystem would need a mount
  namespace (bubblewrap) or per-user containers.
- The shared claude.ai OAuth token is reachable by the agent's own shell — this
  is **by design** (every user drives the owner's one subscription). Don't expect
  it to be hidden from users.
- This is single-host isolation, not a container-escape boundary. For
  **untrusted** multi-user, also run with restricted egress, and per-user
  containers remain the stronger long-term option.

## Reporting

Found a vulnerability? Please open a private report / security advisory on the
repository rather than a public issue.
