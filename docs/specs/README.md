# PicoOraClaw Feature Specs

Detailed, implementation-ready specs for the next wave of PicoOraClaw features.
Each spec is grounded in the current codebase (file/line references) and covers:
schema changes, API/tool surface, SQL sketches, config, implementation plan,
testing, and risks.

## The thesis

PicoOraClaw's moat is that **the entire agent brain already lives in Oracle AI
Database** — memories, sessions, transcripts, state, prompts, config, daily notes,
all with in-database ONNX embeddings. The highest-leverage features all share one
shape: *put more of the agent's experience into Oracle, and use more of Oracle's
unique engines — vector, full-text, PGQ graphs, SQL analytics, JSON duality —
behind one SQL interface.*

## Index

| # | Spec | Tier | Scope | Depends on |
|---|------|------|-------|-----------|
| 01 | [Hybrid recall + memory decay + transcript embeddings](01-hybrid-recall-decay.md) | A3 | S | — |
| 02 | [Episodic memory + nightly consolidation](02-episodic-memory.md) | A2 | M | 01 (schema migration infra) |
| 03 | [Code knowledge graph via PGQ](03-code-knowledge-graph.md) | A1 | L | 01 (hybrid retrieval helpers) |
| 04 | [Self-querying analytics + digest](04-self-querying-analytics.md) | A4 | S | 01 (transcript embeddings) |
| 05 | [Single-file web UI](05-web-ui.md) | B1 | S | — |
| 06 | [Proactive agent (briefs + EOD summary)](06-proactive-agent.md) | B2 | S | — |
| 07 | [Voice loop](07-voice-loop.md) | B3 | S | — |
| 08 | [Skills as Oracle data](08-skills-as-data.md) | B4 | S | 01 |
| 09 | [Ops: migrations, re-embed, JSON duality](09-ops-tier-c.md) | C | S | 01 |

Scope: **S** = days, **M** = ~1–2 weeks, **L** = 2–3+ weeks.

## Sequencing (suggested)

```
Week 1   → 01 (hybrid+decay+transcripts) → 05 (web UI) → 09 migration runner
Week 2-3 → 02 (episodic) → 06 (proactive) → 04 (analytics)
Week 3-4 → 08 (skills) → 07 (voice) → 03 (code graph — flagship, take the time)
```

## Cross-cutting invariants

- **Oracle stays isolated in `pkg/oracle/`** — no Oracle SQL outside it.
- **Schema migrations are additive and idempotent** — bump `schema_version` in
  `PICO_META`, never destructive (`CREATE TABLE IF NOT EXISTS`-style via
  ORA-00955 tolerance as `InitSchema` already does).
- **Graceful fallback** — every new Oracle feature must no-op or degrade when
  Oracle is disabled (file-based mode), matching existing patterns.
- **Tests** — unit tests with `go-sqlmock` + `testify` for pure logic; the
  existing `oracle_container_integration_test.go` pattern for real-DB checks.
- **No external embedding API required** — in-database ONNX `VECTOR_EMBEDDING()`
  is the default path for any new embedding consumer; the API-mode
  `EmbeddingService` remains as fallback.
