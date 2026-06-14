package web

// briefingSkillContent is the seeded morning-briefing skill (spec §7.4). It must
// degrade gracefully when the user has no external tools registered.
const briefingSkillContent = `# Morning briefing

How to assemble a concise, useful morning briefing for the user.

Gather what's relevant, then write a short briefing:

1. **Reminders** — call ` + "`reminder_list`" + ` for anything pending or due today.
2. **Calendar / tasks** — if the user has calendar or task tools available (e.g. an
   ` + "`mcp__*`" + ` server they registered), check today's events and due tasks. If no such
   tools exist, skip this silently — do not mention missing tools.
3. **Recent context** — call ` + "`memory_search`" + ` for yesterday's summary and any
   relevant facts/preferences.

Then write the briefing:
- Lead with anything time-sensitive or conflicting (flag overlaps explicitly).
- Be concise — a few short sections or a tight list, not an essay.
- Use the user's local time for "today"/"this morning".
- If there is genuinely nothing scheduled and no reminders, say so briefly and
  offer one helpful nudge.

Output only the briefing text — it is delivered directly to the user.`

// briefingPrompt is the scheduled agent_run prompt that invokes the skill.
const briefingPrompt = `It's the morning. Produce my morning briefing now, following the "morning-briefing" skill: ` +
	`check pending reminders, any calendar/task tools I have registered, and recent memory; ` +
	`flag conflicts; be concise. If I have no external tools, brief me on reminders and memory only. ` +
	`Output only the briefing.`
