# Spec 08 — Skills as Oracle Data

**Tier B4 · Scope S · Requires: Spec 01 (embedding helpers)**

## Problem

Skills are filesystem-bound. `SkillsLoader` (`pkg/skills/loader.go`) scans
workspace/global/builtin dirs for `SKILL.md`; `SkillInstaller`
(`pkg/skills/installer.go`) fetches from GitHub. The agent loads *all*
available skill descriptions into context — which is fine at 5 skills and
wasteful at 50. There is no record of **which skills get used** and no way to
search "which skill handles X" semantically.

## Goal

1. **Usage tracking** — each skill invocation is recorded (Oracle state, or a
   file when Oracle is off) so `skills list` shows last-used + hit counts and
   the agent can prune/reorder.
2. **Semantic skill search** — `skills search "make a tmux session"` uses
   embeddings over skill descriptions instead of substring matching.
3. **Skill awareness injection** — only the top-K skill descriptions (by
   vector similarity to the user's message, injected at context build) go into
   the prompt, bounded context stays bounded.

## Design

### 1. Usage tracking

`pkg/skills` gains a `UsageRecorder` with two backends:

- **Oracle**: table `PICO_SKILL_USAGE` (v1.3.0 schema):

```sql
CREATE TABLE PICO_SKILL_USAGE (
    skill_name VARCHAR2(128) NOT NULL,
    agent_id   VARCHAR2(64) NOT NULL,
    use_count  NUMBER DEFAULT 0,
    last_used  TIMESTAMP,
    embedding  VECTOR,
    PRIMARY KEY (skill_name, agent_id)
);
```

- **File**: `~/.picooraclaw/skills/usage.json` (JSON map) — the offline path.

Hook points: `SkillInstaller` (on install/uninstall) and — for usage — the
agent's context builder (`pkg/agent/context.go`) each time a skill's
description is actually referenced, or simpler: `skills` CLI subcommands record
their own invocations, plus the agent loop records when the skill directory was
accessed during a turn (heuristic: if `read_file`/`list_dir` touched the skill
path). v1 keeps it explicit: `skills list` records; agent-loop hook is a
best-effort event-based counter.

### 2. Semantic search

Extend `skills search`:

```
picooraclaw skills search "set up a tmux session"
```

- Embed the query (ONNX inline `VECTOR_EMBEDDING(model USING :1 AS DATA)` or
  `EmbedText` — reuse `pkg/oracle/embedding.go`).
- For each skill, embed `name + description` (first 512 chars — the
  `maxEmbeddingInputLen` cap already exists) at install/scan time, store in
  `PICO_SKILL_USAGE.embedding` (Oracle mode) or a sidecar
  `skills/.embeddings.json` (file mode).
- Rank by cosine; fall back to existing substring matching when embeddings are
  unavailable (e.g., ONNX not loaded).

### 3. Context injection (top-K)

`AgentLoop` builds tool/skill context at `pkg/agent/context.go` and
`pkg/tools/registry.go:188` (`summaries`). With Oracle available and >N
(8) skills installed:

- Embed the latest user message (cheap: one call).
- Select top-K skill descriptions by similarity (K=4).
- Replace the "all skills" block with the selected ones + a note
  "…and N more (use skills search)".

Config: `agent.skills.top_k` (0 = off, keep current behavior).

## Files touched

- `pkg/oracle/schema.go` — v1.3.0 `PICO_SKILL_USAGE`
- `pkg/oracle/skill_store.go` (new) — record/embed/search/rank
- `pkg/skills/` — `UsageRecorder`, `SemanticSearch` (oracle + file backends)
- `pkg/agent/context.go` — top-K injection
- `cmd/picooraclaw/main.go` — `skills search` upgrade, usage in `skills list`
- `pkg/config/config.go` — `Agent.Skills.TopK`

## Testing

- Unit: file-mode usage JSON round-trip; top-K selection given fake
  similarities; fallback to substring search when embedding nil.
- sqlmock: upsert + search SQL shapes.
- Container integration: install 2 skills, `skills search` ranks the matching
  one first; `skills list` shows hit counts after simulated usage.

## Risks

- **Embedding stale after SKILL.md edit** — re-embed on `mtime` change at scan
  (same guard as Spec 03 incremental index).
- **Top-K hiding skills** — bounded by config default 4 + explicit
  "…and N more" note; `top_k=0` preserves today's behavior exactly.
- **File-mode parity** — sidecar JSON keeps feature working without Oracle.

## Acceptance criteria

- `skills search "tmux"` returns the tmux skill first when ONNX is loaded.
- `skills list` shows last-used and counts; counts persist across restarts.
- With 12 skills installed and `top_k=4`, prompt size shrinks while the
  relevant skill description is still present for a matching request.
- `top_k=0` → byte-identical prompt to today.
