# Spec 06 — Proactive Agent: Morning Brief + EOD Summary

**Tier B2 · Scope S**

## Problem

PicoOraClaw is purely reactive: it answers when spoken to. But the primitives
for proactivity already exist and are underused:

- `pkg/cron` `CronService` + `CronTool` — full scheduling (cron exprs,
  one-time, recurring) with delivery via bus.
- `AgentLoop.ProcessHeartbeat` (`pkg/agent/loop.go:315`) — stateless
  agent turns designed exactly for scheduled work ("Each heartbeat is
  independent").
- `MessageTool` (`pkg/tools/message.go`) — send to any channel.
- Daily notes + memories — the content a brief is built from.

The user wakes up, opens a channel, and the agent should already know what's
waiting. Proactive beats reactive for both utility and demos.

## Goal

Two config-gated built-in schedules, registered by the gateway at startup:

1. **Morning brief** (default `0 8 * * *`, configurable): pull today's
   reminders (cron jobs due today), yesterday's daily note, new memories
   (last 24 h), open threads (sessions with no reply in 48 h) → one
   heartbeat LLM turn → deliver a tight brief to the user's last channel
   (`PICO_STATE.last_channel`/`last_chat_id` — already tracked!).
2. **EOD summary** (default `0 18 * * *`): summarize the day's transcripts
   into today's daily note (auto `write_daily_note`), close open items.

Both jobs are **just cron jobs** — created through the existing
`CronService.AddJob` API at gateway startup, visible via `cron list`, and
user-disablable. No new scheduling machinery.

## Design

### 1. Configuration (`pkg/config/config.go`)

```go
type ProactiveConfig struct {
    Enabled      bool   `json:"enabled" env:"PICOCLAW_AGENT_PROACTIVE_ENABLED"` // default true
    BriefCron    string `json:"brief_cron"    env:"PICOCLAW_AGENT_BRIEF_CRON"`    // "0 8 * * *"
    EODCron      string `json:"eod_cron"      env:"PICOCLAW_AGENT_EOD_CRON"`      // "0 18 * * *"
    BriefChannel string `json:"brief_channel" env:"PICOCLAW_AGENT_BRIEF_CHANNEL"` // "" = last used
}
```

### 2. Brief builder (`pkg/agent/brief.go`, new)

`BuildBriefContext(store *MemoryStore, window time.Duration) string`:
- reminders: `cronService.ListJobs` filtered to due-today or recurring;
- yesterday note: `GetRecentDailyNotes(1)` (skips today);
- new memories: SQL `WHERE created_at >= SYSDATE - 1` ordered by importance;
- open threads: transcripts grouped by session with `MAX(created_at)` older
  than 48 h and role split showing no assistant reply.

Wired through `ProcessHeartbeat` (already stateless) with a prompt like:

```
You are the morning brief. From the following raw data, produce ≤10 lines:
<data>
```

Delivery: `MessageTool`-equivalent via bus to `brief_channel` or the stored
`last_channel`/`last_chat_id` (`pkg/oracle/state_store.go:79-97`). Fallback:
print to stdout + log.

### 3. EOD summary

Heartbeat turn whose tool plan is: gather today's transcripts (Oracle query,
spec 04 analytics), summarize, `write_daily_note` (tool exists:
`pkg/tools/daily_note.go`). No new write path — reuse the tool.

### 4. Gateway wiring (`cmd/picooraclaw/main.go` `gatewayCmd`)

After `cronService := setupCronTool(...)`:

```go
if cfg.Agent.Proactive.Enabled {
    cronService.AddJob("morning_brief", cron.CronSchedule{CronExpr: cfg.Agent.Proactive.BriefCron},
        briefPrompt, true /*deliver*/, channel, chatID)
    cronService.AddJob("eod_summary", ..., eodPrompt, true, ...)
}
```

The existing `JobExecutor` (`ProcessDirectWithChannel`) already handles
`deliver=true` by running an agent turn and messaging back — so the built-ins
are declarative: name + schedule + prompt.

## Files touched

- `pkg/config/config.go` — `ProactiveConfig`
- `pkg/agent/brief.go` (new) — context builders
- `cmd/picooraclaw/main.go` — job registration in `gatewayCmd`
- `pkg/oracle/memory_store.go` — `RecentMemories(days)`, `OpenThreads(hours)`
  queries (also reused by Spec 04)

## Testing

- Unit: `BuildBriefContext` given seeded rows produces reminders + yesterday +
  new-memory + open-thread sections; empty-data case returns a graceful
  "nothing yet".
- sqlmock: `RecentMemories`, `OpenThreads` SQL shapes.
- Integration (container): register jobs, tick `computeNextRun` manually,
  assert jobs fire through `JobExecutor`.
- Config off → zero jobs registered; `cron list` empty.

## Risks

- **Heartbeat cost** — one LLM call per brief/EOD; fine by design
  (config-gated, user-disablable).
- **Wrong target channel** — fallback chain: explicit config → stored
  `last_channel` → stdout. Never fails the gateway.
- **Job persistence** — cron store is file-based today (`CronService(storePath,…)`);
  registering built-ins each start is idempotent via stable job names
  (`morning_brief`, `eod_summary`) with add-if-missing semantics.

## Acceptance criteria

- With a seeded "remind me about release EOD" job + yesterday's note, starting
  the gateway delivers a morning brief containing both within the day.
- `cron list` shows the two built-ins; `cron remove morning_brief` disables.
- EOD summary writes to today's daily note and is visible in `oracle-inspect
  notes`.
- Oracle off: brief degrades to file-based daily notes + MEMORY.md.
