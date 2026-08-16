# Spec 04 — Self-Querying Brain: Analytics + Digest

**Tier A4 · Scope S · Requires: Spec 01 (transcript embeddings)**

## Problem

Every conversation is already persisted (`PICO_TRANSCRIPTS`, `PICO_STATE` with
counters like `total_conversations`, `tools_used_count`), but **nothing queries
them analytically**. The agent cannot answer "what did we work on last week?",
"which tools do I overuse?", or produce a weekly digest — despite all the raw
material living in one converged database where a single `LISTAGG` window
query answers any of these.

## Goal

1. `picooraclaw digest [--week|--day]` — auto-generated retrospective from
   transcripts + memories + daily notes.
2. `oracle-inspect analytics <topic>` — SQL-powered breakdowns: activity,
   topics, tool usage, channels, session summaries.
3. Expose the same queries to the agent via a `brain` tool so the model can
   answer "what did we do last week" mid-conversation.

## Design

### 1. `oracle-inspect analytics` subcommand

Topics (all single-query, window functions over `PICO_TRANSCRIPTS`):

| topic | query shape |
|---|---|
| `activity` | messages/day (last 30 d), per channel, role split |
| `topics` | top frequent tokens (excluding stopwords) from user content + top memories by category |
| `tools` | `tools_used_count` + per-tool success rate from `PICO_STATE`/events |
| `channels` | per-channel message counts + last activity |
| `sessions` | sessions with message counts + summary preview |
| `week` | digest: all of the above scoped to last 7 days, markdown-rendered |

Example core query:

```sql
SELECT TRUNC(created_at) d, COUNT(*) msgs,
       COUNT(DISTINCT session_key) sessions
FROM PICO_TRANSCRIPTS
WHERE agent_id = :a AND created_at >= SYSDATE - 30
GROUP BY TRUNC(created_at) ORDER BY d;
```

Topic extraction (no external NLP): `REGEXP_SUBSTR` over `LOWER(content)`,
stopword filter, `GROUP BY` on tokens, `FETCH FIRST 20`. This is the v1
approximation; Spec 01 transcript embeddings unlock semantic topic clustering
as v2.

### 2. `digest` subcommand (markdown, cron-able)

```
picooraclaw digest [--day|--week|--since <date>] [--to <channel>]
```

Pipeline (all Oracle reads, one LLM call at the end):

1. Pull transcripts (user role), daily notes, new memories for the window.
2. Assemble a compact markdown digest: activity stats + top topics + new
   memories + open threads (sessions with no assistant reply in 48 h).
3. One LLM pass (existing provider) to tighten into "This week: worked on X,
   learned Y, remembered Z. Open: W."
4. `--to` delivers via `MessageTool`/bus to any channel (default stdout).

Cron registration in gateway mirrors Spec 02 (config
`agent.digest.cron`, default `0 17 * * 5` = Friday 5pm, output to `cli`).

### 3. `brain` tool (in-loop analytics)

| arg | meaning |
|---|---|
| `question` | one of `activity|topics|tools|channels|sessions|week` |
| `days` | window (default 7) |

Result is a compact text table. The tool description teaches: "Use when the
user asks about past activity, what we worked on, tool usage, or a summary of
recent sessions." Registered in `pkg/agent/loop.go` (config-gated, Oracle only).

## Files touched

- `pkg/oracle/analytics.go` (new) — all queries + digest assembly
- `cmd/picooraclaw/main.go` — `digest` subcommand; `oracle-inspect analytics`
- `pkg/tools/brain.go` (new) — thin wrapper over `analytics.go`
- `pkg/agent/loop.go` — register `brain`
- `pkg/channels/` — reuse `MessageTool` for delivery; no channel changes

## Testing

- sqlmock: each analytics query shape; stopword tokenizer unit tests.
- Container integration: seed transcripts via real loop, run `digest --day`,
  assert markdown contains activity + topics.
- File-mode: `digest` degrades to reading `memory/` dirs (daily notes +
  MEMORY.md) — same output shape, no Oracle.

## Risks

- Token-based topics are noisy — acceptable v1; v2 uses embeddings (Spec 01).
- LLM digest cost — one call/day/week, configurable model; `--no-llm` flag
  emits the raw markdown without the LLM pass.

## Acceptance criteria

- After a week of real use, `picooraclaw digest --week` produces a correct
  retrospective mentioning the actual topics worked on.
- `oracle-inspect analytics tools` shows per-tool success rates.
- `brain` answers "what did we do last week" mid-session from real data.
- All queries parameterize `agent_id`; no SQL injection surface (validated
  identifiers per `pkg/oracle/validate.go`).
