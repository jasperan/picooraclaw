# Spec 02 — Episodic Memory + Nightly Consolidation

**Tier A2 · Scope M · Requires: Spec 01 (schema migration infra)**

## Problem

The agent already emits a structured event stream — `Event` in
`pkg/agent/events.go`: `message_start/end`, `tool_call_start/end` with
`Args`, `Result`, `OK`, session id, timestamp — and every turn is persisted to
`PICO_TRANSCRIPTS`. But nothing *mines* that experience. The agent cannot answer
"how did I solve a problem like this before?" It starts each session cold except
for the small `GetMemoryContext` window (`pkg/agent/memory.go`).

This is the difference between *storage* and *learning*. Letta/MemGPT and the
memory-consolidation literature (Zylos 2026) agree: long-running agents must
transform raw episodic traces into compressed, semantically queryable
long-term knowledge. PicoOraClaw can do this **inside Oracle with SQL** — no
external memory server, no new infra.

## Goal

1. Capture each agent run as an **episode**: goal → tool trajectory (with
   outcomes) → final result, stored with an embedding.
2. `recall --episodes "how did I fix the ORA-30081 issue"` replays past
   trajectories (experience replay).
3. A nightly **consolidation** cron transforms episodes into new long-term
   memories and prunes noise — the agent literally gets smarter with use.

## Design

### 1. Schema (v1.2.0)

```sql
CREATE TABLE PICO_EPISODES (
    episode_id   VARCHAR2(64) PRIMARY KEY,
    agent_id     VARCHAR2(64) NOT NULL,
    session_key  VARCHAR2(255),
    goal         CLOB,                 -- the user request / task
    trajectory   CLOB,                 -- JSON: [{tool,args,result,ok,ts}...]
    outcome      CLOB,                 -- final assistant text or summary
    status       VARCHAR2(16),         -- success | failed | interrupted
    embedding    VECTOR,               -- goal embedding
    importance   NUMBER(3,2) DEFAULT 0.5,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    duration_ms  NUMBER
);
CREATE INDEX IDX_PICO_EPISODES_AGENT ON PICO_EPISODES(agent_id, created_at);
CREATE VECTOR INDEX IDX_PICO_EPISODES_VEC ON PICO_EPISODES(embedding)
  ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95;

CREATE TABLE PICO_CONSOLIDATION (
    run_id      VARCHAR2(64) PRIMARY KEY,
    agent_id    VARCHAR2(64) NOT NULL,
    started_at  TIMESTAMP,
    finished_at TIMESTAMP,
    episodes_in NUMBER DEFAULT 0,
    memories_out NUMBER DEFAULT 0,
    pruned      NUMBER DEFAULT 0,
    log         CLOB
);
```

### 2. Capture path

`AgentLoop` already has `SetEventEmitter` (`pkg/agent/loop.go:97ad397`).
Add an `EpisodeRecorder` that implements `EventEmitter` and:

- On `message_end`, finalize the in-flight episode for `session_id`:
  - `goal` = first `message_start` text,
  - `trajectory` = accumulated `tool_call_start/end` pairs (args JSON + result
    prefix capped at 2 KB each + `ok`),
  - `outcome` = final text,
  - `status` = `failed` if any tool call `ok=false` or `EventError` fired,
  - embed `goal` (ONNX inline or API fallback), `INSERT` into
    `PICO_EPISODES`.
- Debounced: episodes shorter than 2 events are skipped (noise).
- Wired in `initOracleAgent` (`cmd/picooraclaw/main.go:1607`) next to the
  memory store; file-based mode records nothing (no-op).

### 3. Experience replay — `recall --episodes`

Extend `RecallTool` with `scope=episodes`:

```sql
SELECT episode_id, goal, trajectory, outcome, status, importance,
       VECTOR_DISTANCE(embedding, <qvec>, COSINE) AS distance
FROM PICO_EPISODES
WHERE agent_id = :a AND embedding IS NOT NULL AND status <> 'interrupted'
ORDER BY distance ASC
FETCH FIRST :n ROWS ONLY;
```

The tool result renders a compact replay card per episode:

```
◈ How I fixed the ORA-30081 issue (0.91, 2026-02-16, success, 42s)
  goal:      agent errors with ORA-30081 on recall
  steps:     recall(query="status") → err; oracle-inspect memories; exec
             "ALTER USER ... " → ok; remember("root cause: ...")
  outcome:   fixed; stored root cause in memory
```

The `recall` tool description teaches the model to use episodes when the user
asks "how did you / how do we / what did we do about".

### 4. Nightly consolidation (`picooraclaw consolidate` + cron)

`consolidate` is a CLI subcommand *and* a gateway-registered cron job:

1. Select episodes from the last 24 h where `status='success'`, grouped by
   vector-cluster (goal embeddings, k≈5 via DBMS `VECTOR_DISTANCE` matrix or a
   simple greedy threshold).
2. For each cluster, LLM-summarize (via existing provider) into 1–3 candidate
   long-term memories: "Pattern: <what worked>; Context: <cluster goals>".
3. Write through `MemoryStore.Remember` with `importance` derived from episode
   count in cluster (`0.4 + 0.1 * min(5, count)`), category `pattern`.
4. Prune: delete episodes older than 90 days with `importance < 0.5`; mark
   `interrupted` episodes older than 30 days as deletable.
5. Record a `PICO_CONSOLIDATION` row; expose last run in `oracle-inspect`.

Gateway: register with `CronService.AddJob("consolidate", {cron_expr: "0 3 * * *"}, ...)`
if `agent.consolidate.enabled` (default true when Oracle enabled).

## Files touched

- `pkg/oracle/schema.go` — v1.2.0 DDL
- `pkg/oracle/episode_store.go` (new) — `Append`, `RecallEpisodes`,
  `ConsolidateCandidates`, `Prune`
- `pkg/agent/loop.go` — wire recorder via emitter (no behavior change when nil)
- `pkg/tools/recall.go` — `scope=episodes`
- `cmd/picooraclaw/main.go` — `consolidate` subcommand, gateway cron registration
- `pkg/agent/events.go` — no change (recorder consumes existing events)

## Testing

- sqlmock: episode INSERT shape (ONNX vs API embed), recall ordering,
  consolidate candidate selection + prune SQL, recorder event accumulation.
- Unit: recorder skips <2-event episodes; status=interrupted excluded from
  replay; cluster→memory importance mapping.
- Container integration: run a real loop, verify episode row; run consolidate
  and verify memories created.

## Risks

- **Cost of nightly LLM summarization** — mitigated: only `success` episodes,
  capped at 50 clusters/night, configurable model (reuse current provider).
- **Trajectory size** — cap per-tool args/results at 2 KB; drop old rows via
  prune so the table stays bounded.
- **Recorder in file mode** — strictly no-op; the emitter default remains
  `NoopEmitter` (`pkg/agent/events.go`).

## Acceptance criteria

- After 5 real conversations, `recall -s "how did I fix the oracle error" --scope episodes`
  returns at least the successful episode with its steps.
- Consolidation creates pattern memories and they surface via normal `recall`.
- Episodes older than 90 days / interrupted > 30 days are pruned.
- Oracle-disabled runs are byte-for-byte unchanged in behavior.
