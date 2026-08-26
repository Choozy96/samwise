# Slack channel support — pre-build review & design (v0.4.0)

> **Status: implemented** (v0.4.0-dev on `main` — seam refactor shipped as
> v0.3.8, Slack adapter in the Slack P1–P4 commits; Discord followed the same
> seam). Kept as the design record.

Decisions locked: use `github.com/slack-go/slack` (Socket Mode); **channels
(group chats) are in the first cut**. This doc is (1) a review of every place
the codebase currently assumes Telegram, (2) the seam refactor to do BEFORE the
Slack adapter lands, (3) the Slack design itself.

## 1. Review — where Telegram leaks outside `internal/telegram`

The adapter itself is cleanly contained; the leaks are the *routing layer*:

| Surface | Coupling | Severity |
|---|---|---|
| `orchestrator/delivery.go` | `o.telegram` (single `ChannelSender` field) used 27×; `"tg:"` prefix + `parseTGDelivery` in DeliverToUser / DeliverRunResult / deliverToTarget / SendWorkspaceFile / anchor | **High — the core seam** |
| Delivery-target scheme | `"tg:<bot>:<chat>"` stored in: job payloads, `user_settings.delivery_channel`, `distill_notify_target`, `system_settings.anchor_chat` | High (stored data!) |
| `web/jobs_handlers.go` | `resolveTGChat`, `chatHints`, `deliveryTargets`, `splitDelivery` all query `channel='telegram'` and mint `tg:` values | High |
| `DispatchRequest.Channel` | `"web" \| "telegram"` string; conversations keyed by it (a Slack thread must become `channel='slack'` cleanly — schema already fine) | Medium |
| `OriginBotID/OriginChatID` | Comments say "Telegram bot+chat" but values are just int64s; Slack channel ids are strings (`C0123…`) — **type mismatch** | Medium |
| `settings.TgFormat` | Telegram-only formatting pref; Slack needs mrkdwn (no user choice needed — mrkdwn is the only mode) | Low |
| `GroupReplyMode` | mention/all — concept ports as-is | Low |
| commands `/delivery`, `/format` | accept `telegram` literals | Low |
| `selfconfig.go` update_settings | `web\|telegram\|here` enum | Low |
| onboarding/settings/agents/jobs templates | "Telegram" wording + bots UI | Low (additive) |

**Verdict:** the DB schema is already channel-generic (`channel_identities.channel`,
`conversations.channel`, `messages.channel`) — good. The work is concentrated in
one place: the orchestrator's delivery routing + the web helpers that mint/parse
`tg:` strings. Everything else is additive.

## 2. Seam refactor (do first, on `main`, before any Slack code)

Goal: after this refactor, adding Slack = implementing one interface + one web
UI page, with zero edits to delivery logic.

1. **Delivery address type.** Replace scattered `"tg:"` parsing with one type:
   `orchestrator.Address{Channel string; BotID int64; ChatID string}` +
   `ParseAddress("tg:1:-100")/("slack:2:C0123")` + `String()`. `ChatID` becomes a
   **string** (Slack ids aren't numeric); Telegram converts internally. Legacy
   stored `tg:` values parse unchanged — **no data migration needed**.
2. **Sender registry.** Replace the single `o.telegram ChannelSender` with
   `o.senders map[string]ChannelSender` (`"telegram"`, later `"slack"`) +
   `RegisterSender(channel, s)`. `ChannelSender` methods take `chatID string`.
   All `if o.telegram != nil` checks become `if s := o.sender(addr.Channel); s != nil`.
3. **Web helpers generalized.** `deliveryTargets`/`chatHints`/`resolveTGChat`
   iterate over ALL channels' identities and mint `Address` strings; labels get a
   channel prefix ("Telegram group (-100…)", "Slack #general").
4. **Origin as Address.** `DispatchRequest.Origin Address` replaces
   OriginBotID/OriginChatID (Binding likewise); "here" resolution uses it.
5. **`TgFormat` stays** (Telegram-only knob); Slack always sends mrkdwn.

Estimated size: one focused session; fully covered by existing delivery tests
(update to the new types) + new Address parse tests. Tag as v0.3.8 so the
refactor soaks separately from the Slack adapter.

## 3. Slack design (mirrors telegram/)

- **Registration:** `slack_bots` table (label, bot_token_enc `xoxb-`, app_token_enc
  `xapp-`, agent_id, enabled, team/self ids cached). UI on the Agents page next
  to Telegram bots. Both tokens encrypted with the secretbox like TG tokens.
- **Transport:** Socket Mode via slack-go (`socketmode.Client`); a `Manager`
  clone: one goroutine per enabled app, reconcile loop, ChannelSender impl.
- **Events (first cut):** `message` (DMs: `im`), `app_mention` (channels),
  plain channel `message` when GroupReplyMode=all. Files in events → download
  via `files.info` + auth'd URL into the workspace (mirror of downloadAttachments,
  incl. the replied-to/thread-parent media rule via `thread_ts`).
- **Pairing:** identical 6-char code flow. DM → pairs the sender
  (`external_id=U…`, `chat_id=D…`); channel → pairs the channel (`external_id`/
  `chat_id=C…`), shared-context like TG groups. Read-only gating for unpaired
  channel members carries over untouched (it's orchestrator-side).
- **Addressing in channels:** respond on @mention (`app_mention`), replies in
  **thread** (`thread_ts`) to keep channels tidy — nicer than TG's flat replies.
- **Formatting:** markdown → **mrkdwn** converter in the slack package (bold
  `*x*`, links `<url|text>`, code fences pass through); chunk at ~39k chars.
  No parse-mode fallback dance needed (mrkdwn is lenient) — keep the
  at-most-once send rule from the TG fix (classify Slack API errors; never
  blind-resend on ambiguous transport errors).
- **Files out:** `ChannelSender.SendFileToChat` → `files.upload v2`.
- **Scopes** (app manifest shipped in docs): `chat:write`, `im:history`,
  `app_mentions:read`, `channels:history`, `files:read`, `files:write`,
  `users:read`. Socket Mode toggle + app token. DEPLOY gets a "create the Slack
  app from this manifest" walkthrough.
- **Docs:** guide "Slack" section mirroring the Telegram one; capabilities block
  mentions Slack; delivery dropdowns show Slack chats automatically via the
  generalized helpers.

## 4. Build order (each step green + committed)

1. Seam refactor → tag v0.3.8.
2. `slack_bots` store + migration + Agents-page UI (register/validate via
   `auth.test`).
3. Socket Mode manager + DM receive/reply + pairing.
4. Channels: app_mention gating, thread replies, read-only enforcement, media
   intake (incl. thread-parent).
5. Delivery integration: `slack:` addresses selectable everywhere (jobs,
   defaults, distill, anchor) + SendFile.
6. Docs (guide + DEPLOY manifest), capabilities block → tag **v0.4.0**.

## 5. Maintainability rules for the Slack package

- Mirror `internal/telegram`'s file layout (client/manager/bot/format) so the
  two adapters stay parallel and reviewable side-by-side.
- slack-go is used for transport/types only; all Samwise policy (gating,
  pairing, formatting rules, delivery) stays in our code — swap-friendly.
- No Slack imports outside `internal/slack` + cmd_serve wiring (enforce the
  same containment Telegram has).
- Every stored value goes through `Address` — never hand-formatted strings.
