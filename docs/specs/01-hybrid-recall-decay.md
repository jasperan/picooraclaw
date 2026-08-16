# Spec 01 — Hybrid Recall + Memory Decay + Transcript Embeddings

**Tier A3 · Scope S · Enables: 02, 03, 04, 08**

## Problem

Today `MemoryStore.Recall` (`pkg/oracle/memory_store.go:290`) is pure cosine
vector search with a hard `recallMinScore = 0.3` cutoff:

```sql
SELECT memory_id, content, importance, category,
       VECTOR_DISTANCE(embedding, <query_vec>, COSINE) AS distance
FROM PICO_MEMORIES
WHERE agent_id = :agent AND embedding IS NOT NULL
ORDER BY distance ASC
FETCH FIRST :n ROWS ONLY
```

Known weaknesses:

1. **No lexical channel.** Exact terms ("Go 1.25", "ORA-30081", "PGQ") are weak
   in 384-dim MiniLM space; a user asking for a verbatim error string gets
   misses the vector channel would never see.
2. **Importance/recency/accessibility unused for ranking.** The schema already
   stores `importance`, `access_count`, `accessed_at` (`pkg/oracle/schema.go`)
   but only `importance` is even selected — never scored.
3. **No filters.** `recall` cannot scope by `category`, date range, or agent_id
   beyond the implicit one.
4. **Transcripts are not semantically searchable.** `PICO_TRANSCRIPTS` (the
   richest data source: every message, every channel) has no `embedding`
   column. "How did we solve X last month?" is impossible.

## Goal

`recall` returns the *best* memories, not just the *closest* ones:
vector + lexical evidence fused with RRF, re-ranked by an
importance × recency × accessibility score, with optional filters. Transcripts
become semantically queryable (foundation for Spec 02/04).

## Design

### 1. Schema migration (v1.0.0 → v1.1.0)

Additive, idempotent, run from the new migration runner (Spec 09):

```sql
-- 1) Lexical index over memory content (Oracle Text CONTEXT index)
CREATE INDEX IDX_PICO_MEMORIES_CTX ON PICO_MEMORIES(content)
  INDEXTYPE IS CTXSYS.CONTEXT;

-- 2) Embedding column on transcripts (backfilled lazily, see §4)
ALTER TABLE PICO_TRANSCRIPTS ADD (embedding VECTOR, embedding_ts TIMESTAMP);

-- 3) Vector index for transcript search (only after backfill, or empty-safe)
CREATE VECTOR INDEX IDX_PICO_TRANSCRIPTS_VEC ON PICO_TRANSCRIPTS(embedding)
  ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95;

-- 4) Decay helper columns already exist; add a computed recency helper view
--    (no new columns needed on PICO_MEMORIES)
```

> ORA-00955/ORA-01408/ORA-01430 tolerance mirrors the existing `InitSchema`
> pattern. Oracle Text needs `CTXSYS` access — the setup script must grant
> `EXECUTE ON CTXSYS.CTX_DDL` (or we degrade to `INSTR`-based lexical fallback,
> see §2.2).

### 2. Hybrid retrieval — `Recall(query, maxResults, opts)`

New signature (backward-compatible wrapper keeps old 2-arg form):

```go
type RecallOptions struct {
    Category   string    // exact category filter
    Before, After time.Time // created_at range
    MinScore   float64   // fused score floor (default 0.15)
    Lexical    bool      // enable lexical channel (default true)
}

func (ms *MemoryStore) Recall(query string, maxResults int) ([]MemoryRecallResult, error)
func (ms *MemoryStore) RecallOpts(query string, maxResults int, opts RecallOptions) ([]MemoryRecallResult, error)
```

**Channel A — vector.** Same `VECTOR_DISTANCE` query as today.

**Channel B — lexical.** Two modes, chosen at schema init:

- *Oracle Text (preferred):* `CONTAINS(content, :q) > 0` scoring with
  `SCORE(1)` normalized.
- *Fallback (no CTXSYS):* `INSTR(LOWER(content), LOWER(:q)) > 0` + a simple
  term-frequency approximation — good enough for verbatim strings.

**Fusion — Reciprocal Rank Fusion in SQL.** Two ranked subqueries tagged by
channel, unioned, ranked, fused, then joined back for the decay score:

```sql
WITH vec AS (
  SELECT memory_id, ROW_NUMBER() OVER (ORDER BY VECTOR_DISTANCE(embedding, TO_VECTOR(:v), COSINE)) rn
  FROM PICO_MEMORIES WHERE agent_id = :a AND embedding IS NOT NULL
  ORDER BY rn FETCH FIRST 100 ROWS ONLY
), lex AS (
  SELECT memory_id, ROW_NUMBER() OVER (ORDER BY SCORE(1) DESC) rn
  FROM PICO_MEMORIES WHERE agent_id = :a AND CONTAINS(content, :q, 1) > 0
  ORDER BY rn FETCH FIRST 100 ROWS ONLY
), fused AS (
  SELECT memory_id, SUM(1.0/(60 + rn)) AS rrf FROM (
    SELECT memory_id, rn FROM vec UNION ALL SELECT memory_id, rn FROM lex
  ) GROUP BY memory_id
)
SELECT m.memory_id, m.content, m.importance, m.category, f.rrf
FROM fused f JOIN PICO_MEMORIES m ON m.memory_id = f.memory_id
WHERE m.agent_id = :a
ORDER BY f.rrf DESC FETCH FIRST :n ROWS ONLY;
```

### 3. Decay scoring (final re-rank)

Compute in Go over the fused candidates (cheap, ≤ 100 rows):

```go
score = rrf * 0.5 +
        (0.3 * importance) +
        (0.2 * retrievability(access_count, accessed_at))

retrievability = 0.5 * (1 / (1 + log2(1 + access_count)))   // use-it-or-lose-it
               + 0.5 * exp(-λ * days_since(accessed_at))     // Ebbinghaus, λ ≈ 0.03
```

Default `MinScore` 0.15 keeps this forgiving while removing noise. The 30%
vector-only cutoff is retired; lexical hits that are semantically unrelated get
killed by the low fused floor instead.

### 4. Transcript embedding pipeline

- `TranscriptStore.Append` (in `memory_store.go`/session path) gains an
  optional embed step: content → `VECTOR_EMBEDDING(model USING :1 AS DATA)`
  (ONNX) or `EmbedText` (API mode), same dual path as `Remember`.
- Backfill: `picooraclaw reindex --transcripts` (Spec 09) iterates rows where
  `embedding IS NULL`, embeds in batches of 50, updates `embedding_ts`.
- New query `RecallTranscripts(query, opts)` mirrors `RecallOpts` over
  `PICO_TRANSCRIPTS` (vector only, v1).

### 5. Tool surface

`pkg/tools/recall.go` (`RecallTool`) parameters gain optional:
`category`, `days` (recency filter), `lexical` (bool), `scope` (`memories` |
`transcripts` | `both`). The tool description is updated so the model can say
"recall what we did about X last week" and have it work.

## Files touched

- `pkg/oracle/schema.go` — v1.1.0 DDL additions + version bump
- `pkg/oracle/memory_store.go` — `RecallOpts`, RRF query, decay scoring,
  `RecallTranscripts`, transcript embed-on-append
- `pkg/oracle/vector_store.go` — shared RRF helper
- `pkg/tools/recall.go` — new parameters + `scope`
- `cmd/picooraclaw/main.go` — wire transcript embedding on append; `reindex`
  subcommand hooks (Spec 09)
- `scripts/setup-oracle.sh` — CTXSYS grant + reindex step

## Testing

- sqlmock: RRF SQL shape, decay math (importance/recency/accessibility edge
  cases: never-accessed, accessed-today, high-importance vs high-recall),
  filters, transcript embed-on-append, ONNX vs API embedding paths.
- Container integration (existing pattern): seed 3 memories incl. one with a
  rare token ("ORA-30081") → lexical-only query must surface it; transcript
  append + recall round-trip.

## Risks

- Oracle Text privileges on Free/ADB — mitigated by `INSTR` fallback (feature
  flag `recall.lexical_mode: text|instr|off`).
- RRF SQL complexity with `go-ora` — keep fusion query in one place
  (`vector_store.go`) and test shape with sqlmock.
- Re-ranking cost — bounded by `FETCH FIRST 100` per channel.

## Acceptance criteria

- `recall -s "ORA-30081"` returns the memory containing that exact string even
  when cosine similarity is low.
- Two memories with same vector similarity but different importance rank by
  importance.
- A memory never accessed in 90 days scores below a freshly-written one at
  equal similarity.
- Transcripts from any channel are semantically searchable.
- All unit + container tests green; Oracle-disabled mode unaffected.
