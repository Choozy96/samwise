# User guide

Everything you need to get the most out of **Samwise**, your personal assistant.
This same guide is rendered in the app under **Guide**.

## What this is

A personal AI assistant you talk to from the **web portal** or **Telegram**. It
remembers things about you, can run scheduled jobs (like a morning briefing), and
is extended with **skills** and **tool servers** you add yourself — no code
changes required.

## Getting started

1. **First login** drops you into a short **setup wizard**. It seeds the assistant
   with a few things about you (what to call you, a bit about you, your preferred
   tone), lets you name and shape your assistant, and sets your timezone. You can
   **Skip** it and do any of this later.
2. After that you land on **Chat**. Type a message and the assistant replies,
   streaming as it goes.
3. You can re-run the wizard anytime from **Settings → Re-run setup**.

## Agents

An **agent** is a named persona with its own identity and behavior. You can have
several — e.g. a work assistant, a writing coach, a research agent.

- Manage them under **Agents**. Each has a **soul** (its system prompt — who it is
  and how it behaves), and optionally its own **model** and **runtime**.
- Leave the soul blank to use the standard assistant. If you write one, your
  operational rules (memory, reminders, timezone) are always kept.
- **Switch agents** with the dropdown on the Chat page, or type `/agent <name>` in
  chat. `/agent` on its own lists them.
- Each agent keeps its own conversation thread, but **memory is shared** across
  your agents.

## Models & access methods

- **Model** — which model answers you (e.g. Opus, Sonnet, Haiku). Set it in
  **Settings**, per-agent on the **Agents** page, or in chat with `/model opus`.
- **Access method** (runtime) — *how* the assistant runs. **Claude — SDK** is
  available today; **Claude — channels** and **ChatGPT — Codex** are coming.
  Set it in Settings or with `/runtime <name>`.

## Slash commands (chat)

Type these in the chat box (web or Telegram); they act instantly and aren't sent
to the assistant:

| Command | What it does |
|---|---|
| `/agent [name]` | List your agents, or switch the active one |
| `/model [name]` | Show or set the chat model (default, opus, sonnet, haiku) |
| `/runtime [name]` | Show or set the access method (channels, sdk, codex) |
| `/timezone [IANA]` | Show or set your timezone (e.g. `Asia/Singapore`); recomputes local schedules |
| `/delivery [web\|telegram]` | Show or set where scheduled jobs are delivered |
| `/status` | Show your current agent, model, timezone, delivery |
| `/format [markdown\|html\|plain]` | Show or set how Telegram messages are formatted |
| `/groupreply [mention\|all]` | In group chats, reply only when addressed (default) or to every message |
| `/bots` | List your Telegram bots and their bound agents |
| `/bind <bot-id> <agent\|none>` | Bind a Telegram bot to an agent (or unbind it) |
| `/jobs` | List your scheduled (recurring) jobs |
| `/reminders` | List your active reminders |
| `/remind <time> <message>` | Set a reminder — `HH:MM`, `daily HH:MM`, or `YYYY-MM-DD HH:MM` |
| `/recall <query>` | Search your memory and show matches directly |
| `/new` | Start a fresh conversation thread (memory & history are kept) |
| `/usage` | Recent runs and token usage by type — input/output/cache (24h / 7d) |
| `/password <current> <new>` | Change your password (prefer the web portal — the message is visible to the channel) |
| `/admin …` | **Admins only** — manage users from chat: `users`, `add <user> <pass>`, `disable <user>`, `enable <user>`, `resetpw <user> <pass>` |
| `/refresh-claude` | Refresh & verify the Claude login — recover if its token lapsed |
| `/help` | List the commands |

## Telegram

Chat with your assistant from Telegram, with the same memory and agents.

1. In Telegram, message **your bot**.
2. The bot replies with a **6-character pairing code** (valid 15 minutes). It
   won't say anything else to strangers.
3. In the portal, open the **Agents** page, scroll to **Pair a Telegram chat**,
   enter the code, and submit.
4. Now message the bot normally — it shows a "typing…" indicator while it works.
   Slash commands work there too.

Set **Settings → Delivery channel** to Telegram to have scheduled jobs (like the
briefing) delivered there.

### Group chats

You can add a bot to a **group chat**. The group is paired as a whole (by its
group id): the moment you add the bot it **posts a pairing code to the group**
(or just message it if you missed it), and you redeem the code under **Agents →
Pair a Telegram chat** like a normal pairing. After
that, **anyone in the group** talks to *your* assistant — sharing your memory,
skills, and context. So only add it to groups you trust. For separate memory,
pair the group to a **different user account** instead.

**Who can change things (write permission).** Everyone in the group can chat with
the assistant, but only people whose **own Telegram account is paired** with the
assistant (a registered user — pair by DMing the bot) may perform **write**
actions: saving/forgetting memory, creating or editing cron jobs and reminders,
and running write-capable tools. For everyone else the run is **read-only** — they
can ask and read, but can't mutate your account. The same gate covers slash
commands (below). Tip: if you own the group, DM-pair your own account too, so you
can run commands and writes there.

**Commands in groups need an explicit mention.** In a group, a slash command runs
only when it's a *clean* command that **names this bot**. Any of these three forms
work (use whichever feels natural):

- `@thisbot /command args` — mention first
- `/command args @thisbot` — mention last
- `/command@thisbot args` — Telegram's native form (what the `/` command menu inserts)

A bare `/command` with no mention is ignored, and a slash buried in other text
("hey /status") is treated as normal chatter, not a command. In a **DM** no
mention is needed — just send `/command args`.

**Reply mode** (Settings → group reply mode, or `/groupreply`): by default the bot
in a group replies **only when it's addressed** — an @mention, a reply to one of
its messages, or a command. Switch to **reply to every message** if you want it to
respond to all group chatter. "Every message" also requires turning the bot's
Telegram **privacy mode off** in @BotFather (otherwise Telegram only delivers it
mentions/commands/replies in the first place).

### Multiple bots, one per agent

You can run **several Telegram bots**, each **bound to an agent** — so your
"work" bot always talks to your work agent and your "personal" bot to your
personal agent, each with its own thread. Manage them all on the **Agents** page
under **Telegram bots**:

1. Create a bot with Telegram's [@BotFather](https://t.me/BotFather) and copy its
   token.
2. Under **Agents → Telegram bots**, add the bot: give it a **label**, paste the
   **token** (validated and stored encrypted — never shown again), and **bind it
   to an agent** (or leave it unbound to follow your active agent).
3. Message the new bot to get a pairing code and redeem it under **Agents → Pair a
   Telegram chat**. Each bot is paired separately. You can rebind a bot's agent any
   time from the same page or with `/bind` in chat, and **unpair** a chat there too.
   A bot configured by the admin via the environment also appears here as the
   "Default bot," which you can pair/unpair (it routes to your active agent).

A **bound** bot ignores `/agent` (it always speaks as its agent — change the
binding in the portal instead). Scheduled **agent runs** are delivered through
the bot bound to that agent, if you have one. If your deployment was set up with
a single bot via its environment, that one keeps working and routes to your
active agent.

**Message format** (Settings → Telegram message format, or `/format` in chat):
- **Markdown** (default) — the assistant's markdown is converted to Telegram's
  **MarkdownV2** and sent with the right `parse_mode`, so **bold**, *italic*,
  `code`, and links render properly.
- **HTML** — the same, using `parse_mode=HTML`.
- **Plain** — sends text as-is, no formatting.

If Telegram ever rejects the formatted markup, the message is automatically
resent as plain text so it always arrives. Switchable any time; the web chat is
unaffected.

## Memory

The assistant keeps long-term memory about you and surfaces what's relevant to
each message. It's **shared across all your agents**, and you only ever see your
own.

### Two layers

- **Facts** (semantic) — discrete things about you, tagged by **topic**: your
  name, preferences, ongoing projects, people you mention. These don't expire.
- **Daily notes** (episodic) — a dated **summary of each day**: decisions,
  commitments, progress, anything worth remembering from that day's conversation.

### How memory gets saved

1. **Direct saving.** When you tell it something worth keeping ("I'm allergic to
   peanuts", "remember that my manager is Dana"), it saves a **fact** right then.
   You can also explicitly ask it to remember or forget something, or add/delete
   entries yourself on the Memory page.
2. **Distillation (automatic).** You don't have to curate daily notes — the
   assistant writes them for you by reading the conversation:
   - **Through the day**, every few hours, it refreshes *today's* note with
     anything new (incremental — it extends the existing note, doesn't start over).
   - **At end of day** (your **distillation time** in Settings → Memory), it
     re-reads the whole day and writes the authoritative summary for that date.
   - Distillation is **silent by default**. If you'd like to see what it
     remembered, turn on the **end-of-day note** under **Settings → Memory &
     context** and it'll message you that summary when the daily pass runs.

### How memory gets retrieved

Each message, the assistant assembles context from:
- the **last two days of daily notes** — always loaded, so recent context is
  never missed;
- a **relevance search** (full-text) across all your facts and older daily notes,
  pulling the **top matches** for what you're talking about (the **top K** in
  Settings → Memory & context tunes how many);
- plus the recent messages of the current thread (the **transcript window**).

So recent days are always present, and older memory surfaces when it's relevant
to the conversation — you don't have to remind it of things it already knows.

### Browsing & editing

The **Memory** page lets you browse by **topic** (facts) or by **date** (daily
notes), **search** across everything, drill into a topic or date, and add or
delete entries. Click any table's column header to **sort** by it (click again to
reverse).

## Skills

**Skills** are markdown instructions that shape how the assistant does a task
(e.g. "how to write my weekly review").

- **Extensions → Skills**: create one with the editor (name, description, content),
  or **import a `.zip`** of a skill folder (a `SKILL.md` plus optional scripts &
  assets).
- Mark a skill **"always on"** to have it apply to every chat; otherwise it's
  followed when relevant or when a job names it.
- **Skills with scripts** can actually run their scripts when the assistant has
  its scoped tools enabled (on by default in the Docker deployment).
- For an imported skill, expand it to see its **bundle files** (scripts & assets)
  and click any file to view its contents read-only — so you can inspect exactly
  what a complex skill contains, not just its `SKILL.md`.

## MCP servers (external tools)

Give the assistant new powers — calendars, task managers, note apps — by
registering **MCP servers** under **Extensions → MCP servers**.

- Choose **stdio** (a command, e.g. `npx -y <package>`) or **http** (a URL).
- Enter any credentials as `KEY=value` lines; they're **encrypted at rest**.
- Enabled servers are available to the assistant on every run.
- Tip: `npx`-based servers download on first use — pre-install them so they
  connect reliably.

## Secrets (API tokens for skills)

When a skill's script needs an API token (Todoist, Notion, etc.), add it under
**Extensions → Secrets** — a **name** (the environment variable the skill reads,
e.g. `TODOIST_TOKEN`) and its **value**.

- Stored **encrypted at rest** and injected into every run as an environment
  variable, so the skill's script can read it.
- The value is **never** shown in chat, saved to memory, or displayed back — it's
  write-only (you can replace it, not read it). This is the right place for
  secrets; **don't** paste tokens into chat (they'd land in the transcript) or ask
  the assistant to "remember" them (memory isn't for secrets). The assistant is
  also instructed never to print a secret's value back to you.
- For **OAuth** credentials (e.g. Google), choose type *OAuth* and paste the token
  JSON. The page shows that credential's **expiry** read-only, and the app
  **auto-refreshes** it in the background before it lapses (keeping scheduled tasks
  working 24/7). If the underlying login is ever revoked and can't be refreshed,
  you get an alert to re-authenticate and update it here.
- The **Credential status** list also shows your **Claude subscription** token's
  expiry, so you can see at a glance when the assistant's own login will need a
  refresh.
- Requires `MASTER_KEY` to be set (the encryption key).

## Cron jobs & reminders

Under **Cron jobs** you can create things that run automatically.

- **Reminders** are simple "ping me" messages — just ask the assistant ("remind me
  to call the dentist at 3pm").
- **Agent runs** are scheduled prompts (like a **morning briefing**) that can load
  a skill. Create them two ways: the
  **Cron jobs** form, or **just ask the assistant in chat** — e.g. "set up a
  daily briefing at 8am" or "move my 9pm report to 10pm." It can list, change,
  pause, and delete your scheduled jobs for you.
- **Where the result lands**: each agent run has its own delivery destination —
  your default channel, the web portal, or a specific Telegram chat you're paired
  to (pick it in the **Cron jobs** form's *Deliver result to* dropdown, or tell
  the assistant "deliver this one here" / "send it to the web"). You can only
  choose chats you actually belong to.
- **Timezone drift**: jobs set to *your local time* follow you when you travel; tell
  the assistant "I've landed in London" and your schedule shifts with you.
- **Pausing**: untick **Enabled** when editing a job to pause it — it stays in your
  list but won't fire until you re-enable it. (One-shot jobs auto-pause after they
  run; a job that keeps failing to authenticate is paused too.)
- **Sorting**: click any column header in the jobs table to sort by it (click again
  to reverse).

## Audit log

The **Audit** page records activity on your account — logins, messages, skill and
tool invocations, and scheduled-job fires. It's your debugging lifeline when
something looks off. You only see your own activity.

## Settings

Settings are organized into tabs:

- **General** — timezone, delivery channel, Telegram message format, your active
  access method / model, and **agent tools (advanced)**: the assistant always has
  a default file/shell toolset; here you can switch on **individual** extra
  built-in tools (e.g. `WebFetch`/`WebSearch` to read pages and search the web).
  Each lists what it does, with a warning on the risky ones and a note on the ones
  that do nothing here. All off by default, and only apply when the deployment has
  agent tools enabled.
- **Memory & context** — the **end-of-day distillation time**, whether to be
  **notified** of the daily memory note, and context tuning (transcript window,
  memory retrieval depth). See [Memory](#memory) for what these do.
- **Account** — **change password** (enter your current password and a new one,
  min 8 chars) and **re-run setup** (restarts the welcome wizard).

## Roadmap (coming soon)

A few things are scaffolded but not live yet. They're marked here so you know what
to expect:

- **More access methods** — **Claude — channels** and **ChatGPT — Codex** runtimes
  (only **Claude — SDK** runs today).
- **Helper scripts & per-user sandboxes** — isolated per-user containers so skills
  can run heavier scripts safely.
- **Multi-agent v2** — per-agent skills/MCP tools and optional per-agent memory
  (today agents share your skills, tools, and memory).
- **Bundled extension content** — ready-made Calendar / Todoist / Notion setups
  (the underlying skill + MCP system already works; this is pre-built content).

Selecting a not-yet-live option (e.g. a future runtime) just shows a "coming soon"
note — nothing breaks.

## Troubleshooting

- **A scheduled job failed** — you'll get a one-line notice; check the **Audit**
  page and the job under **Cron jobs**.
- **The assistant did something odd** — check the **Audit** page to see exactly
  what tools it called, and your **Memory** for anything it's holding onto.
- **A registered tool isn't working** — confirm it's enabled under Extensions, and
  that any `npx` package is pre-installed.
