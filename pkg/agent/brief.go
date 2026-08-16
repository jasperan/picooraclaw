package agent

// Built-in proactive prompts (Spec 06). These are scheduled agent turns:
// the agent uses its own tools (recall, cron list, write_daily_note) to
// gather live data, so no new data plumbing is required.

// MorningBriefPrompt instructs the agent to compile a short brief from its
// memory, recent conversations, and reminders.
const MorningBriefPrompt = `You are preparing the user's morning brief. Be concise and practical.

Gather live data using your tools, then produce at most 10 lines:
1. Call ` + "`recall`" + ` with scope "transcripts" and days 1 to see what happened yesterday.
2. Call ` + "`cron`" + ` with action "list" to find reminders scheduled for today.
3. If anything important was remembered recently, include it.

Output format:
- One line: "Good morning. Here is your brief."
- Bullets: yesterday's highlights, today's scheduled items, any open threads.
- End with one line on what needs attention first.
Do not invent data. If there is nothing yet, say so briefly.`

// EODSummaryPrompt instructs the agent to summarize the day and persist it to
// the daily note.
const EODSummaryPrompt = `You are preparing an end-of-day summary. Use your tools:

1. Call ` + "`recall`" + ` with scope "transcripts" and days 1 to gather today's conversations.
2. Summarize what was accomplished, what was learned, and what is still open.
3. Persist the summary by calling ` + "`write_daily_note`" + ` with a markdown section titled "## End-of-Day Summary".

Then reply to the user with a 3-6 line wrap-up. Do not invent data.`
