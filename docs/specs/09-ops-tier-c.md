# Spec 09 — Ops Tier C: Migration Runner, Re-Embed, JSON Duality Views

**Tier C · Scope S**

## Problem

`InitSchema` (`pkg/oracle/schema.go:109`) creates tables/indexes idempotently
and stamps `schema_version = 1.0.0` into `PICO_META`, but there is **no
migration machinery**: adding a column or table to `tableDDL` only works for
fresh installs. Existing installs never get new columns (Specs 01–03 all need
`ALTER TABLE`/new tables). Also missing: a way to (re)compute embeddings when
the model changes or rows are backfilled.

## Goal

1. **Migration runner** — ordered, idempotent, version-gated DDL steps;
   `InitSchema` applies all steps up to current version.
2. **`reindex` command** — recompute embeddings for a table/scope
   (memories, transcripts, episodes, code nodes, skills) in batches.
3. **JSON-Relational Duality views** (optional, 23ai+; showcase Oracle) — expose
   memories/sessions as updatable JSON documents so the web channel can serve
   REST JSON with zero glue code.

## Design

### 1. Migration runner (`pkg/oracle/migrations.go`, new)

```go
type Migration struct {
    Version string
    Apply   func(db *sql.DB) error   // must be idempotent
}

var migrations = []Migration{
    {Version: "1.1.0", Apply: func(db *sql.DB) error {
        // Spec 01: CONTEXT index, transcripts embedding column, vector index
    }},
    {Version: "1.2.0", Apply: /* Spec 02: PICO_EPISODES + consolidation */},
    {Version: "1.3.0", Apply: /* Spec 03: code graph tables + PGQ + Spec 08 usage */},
}

// in InitSchema, after tables exist:
for _, m := range migrations {
    if applied(meta, m.Version) { continue }
    if err := m.Apply(db); err != nil { return wrapped }
    setSchemaVersion(db, m.Version)
}
```

Rules:
- Each `Apply` tolerates already-applied DDL (ORA-00955/01408/01430/01432
  swallow, mirroring existing helpers `isORA00955`/`isORA01408`).
- Version read: `SELECT meta_value FROM PICO_META WHERE meta_key='schema_version'`.
- Partial failure: log + continue (additive-only policy means a failed step is
  retried next boot); a `PICO_META` key `last_migration_error` records it.
- `InitSchema` keeps creating the *base* 8 tables as today (fresh installs),
  then applies migrations in order. `setup-oracle` output shows
  `Applied migration 1.1.0 ✓`.

### 2. `reindex` command (`cmd/picooraclaw/main.go`)

```
picooraclaw reindex [--scope memories|transcripts|episodes|code|skills|all]
                    [--batch 50] [--force]
```

- Iterates rows with `embedding IS NULL` (or all when `--force`), embeds via
  `EmbeddingService` (ONNX inline preferred; API fallback), updates in batches
  of 50 (`execBatch`), prints progress `[  420/1200 ]`.
- Reused by `setup-oracle.sh` post-install to backfill transcripts.
- No-op gracefully when scope table doesn't exist (migration not yet applied).

### 3. JSON-Relational Duality views (optional, `setup-oracle --duality`)

Oracle 23ai+ lets one DDL create a REST-servable, auto-synced JSON document
view over relational tables:

```sql
CREATE JSON RELATIONAL DUALITY VIEW PICO_MEMORIES_DOC AS
  PICO_MEMORIES @INSERT @UPDATE @DELETE
  { _id : memory_id,
    agent : agent_id,
    content : content,
    importance : importance,
    category : category };
```

- Enabled via a migration step **skipped when the feature isn't present**
  (probe `SELECT COUNT(*) FROM ALL_JSON_RELATIONAL_DUALITY_VIEWS` or catch
  ORA-00955/unsupported → mark `duality=disabled` in `PICO_META`).
- Used by: web channel `/v1/memory` could later serve `SELECT * FROM
  PICO_MEMORIES_DOC` — zero serialization code. v1: expose an
  `oracle-inspect duality` view of the DDL + a `/v1/memories` JSON endpoint
  behind the flag.
- Purpose is *showcase + zero-glue REST*, not critical path.

## Files touched

- `pkg/oracle/migrations.go` (new) — runner + all step functions
- `pkg/oracle/schema.go` — `InitSchema` calls runner; version bump constant
- `pkg/oracle/embedding.go` — expose `BatchEmbed(texts)` (loop, capped) for
  reindex
- `cmd/picooraclaw/main.go` — `reindex`, `setup-oracle --duality`
- `scripts/setup-oracle.sh` — run `reindex` after ONNX load; duality flag

## Testing

- Unit: migration ordering (apply fresh = all; re-run = none), version
  comparison, error tolerance (swallow known ORA codes).
- sqlmock: migration step SQL shapes; reindex batch update count.
- Container: fresh DB → all migrations applied; second `setup-oracle` run →
  no re-apply; `reindex --force` updates embeddings; duality view query
  returns JSON.

## Risks

- **Oracle version differences** — every step probes capability first
  (`V$VERSION` / object existence) and records graceful "skipped" outcomes.
- **Reindex cost on big data** — batched, resumable (only NULL rows unless
  `--force`), capped at 10k rows/run with a log pointer (`PICO_META
  reindex_cursor`).
- **Duality DDL quirks** — feature-flagged and never fatal.

## Acceptance criteria

- Fresh `setup-oracle` applies 1.0.0 → 1.3.0 in order; existing DBs upgrade
  in place with zero data loss.
- `reindex --scope transcripts` backfills embeddings and subsequent
  `recall --scope transcripts` works.
- `setup-oracle --duality` on a supporting DB creates `PICO_MEMORIES_DOC` and
  `SELECT` returns JSON documents; on others it reports disabled and proceeds.
- `schema_version` in `oracle-inspect meta` reflects the latest applied version.
