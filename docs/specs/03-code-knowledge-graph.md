# Spec 03 — Code Knowledge Graph via SQL Property Graphs (PGQ)

**Tier A1 (flagship) · Scope L · Requires: Spec 01 (RRF/hybrid helpers)**

## Problem

PicoOraClaw has filesystem tools (`read_file`, `list_dir`, `edit_file` in
`pkg/tools/`) but **no code understanding**. A model asked to "find where
`vector_store.go` is used" or "what calls `EmbedText`" must either guess file
paths or brute-force `grep` via `exec`. RAG-over-chunks (what most "code
agents" do) hallucinates paths and can't answer *structural* questions —
callers, imports, blast radius.

Oracle AI Database 26ai is the only converged engine that does **vectors +
SQL property graphs + full-text in one database**. Oracle's own GraphRAG
guidance (blogs.oracle.com, "GraphRAG with Oracle AI Database 26ai") is exactly
this architecture. Nobody in the personal-agent space ships this.

## Goal

`picooraclaw index <path>` parses a repository into a property graph
(files → symbols → imports/calls/co-edits) with embeddings on nodes, then the
agent gets a `code_search` tool that answers:

- *Natural language:* "find the code that handles SSE events" → vector match
  on node summaries.
- *Structural:* "what calls `float32SliceToString`?" → `GRAPH_TABLE MATCH`
  1–2 hop traversal.
- *Blast radius:* "what touches `PICO_MEMORIES`?" → transitive imports.

Dogfood: index this repo itself; demo = "understand this repo in 30 seconds".

## Design

### 1. Schema (v1.3.0) — vertex/edge tables + property graph

```sql
CREATE TABLE PICO_CODE_NODES (
    node_id     VARCHAR2(64) PRIMARY KEY,   -- kind:path:name (stable key)
    agent_id    VARCHAR2(64) NOT NULL,
    repo        VARCHAR2(512) NOT NULL,     -- repo root path (normalized)
    kind        VARCHAR2(32),               -- file | package | func | type | method | const | var
    name        VARCHAR2(512),              -- symbol name
    path        VARCHAR2(1024),             -- relative file path
    signature   VARCHAR2(1024),             -- Go-style signature, if any
    doc         CLOB,                       -- doc comment / excerpt
    summary     CLOB,                       -- embedding source text (see §3)
    embedding   VECTOR,
    start_line  NUMBER, end_line NUMBER,
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IDX_PICO_CODE_NODES_REPO ON PICO_CODE_NODES(agent_id, repo);
CREATE VECTOR INDEX IDX_PICO_CODE_NODES_VEC ON PICO_CODE_NODES(embedding)
  ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95;

CREATE TABLE PICO_CODE_EDGES (
    edge_id     VARCHAR2(64) PRIMARY KEY,
    agent_id    VARCHAR2(64) NOT NULL,
    repo        VARCHAR2(512) NOT NULL,
    src_node_id VARCHAR2(64) NOT NULL,
    dst_node_id VARCHAR2(64) NOT NULL,
    kind        VARCHAR2(32),               -- imports | calls | co_edit | defines
    weight      NUMBER DEFAULT 1
);
CREATE INDEX IDX_PICO_CODE_EDGES_SRC ON PICO_CODE_EDGES(agent_id, src_node_id);
CREATE INDEX IDX_PICO_CODE_EDGES_DST ON PICO_CODE_EDGES(agent_id, dst_node_id);

-- The graph (SQL/PGQ, SQL:2023 part 16, available 23ai+)
CREATE PROPERTY GRAPH PICO_CODE_GRAPH
  VERTEX TABLES (PICO_CODE_NODES AS NODE KEY (node_id)
                 PROPERTIES (name, kind, path, summary))
  EDGE TABLES   (PICO_CODE_EDGES AS EDGE KEY (edge_id)
                 SOURCE KEY (src_node_id) REFERENCES PICO_CODE_NODES(node_id)
                 DESTINATION KEY (dst_node_id) REFERENCES PICO_CODE_NODES(node_id)
                 PROPERTIES (kind, weight));
```

`CREATE PROPERTY GRAPH` requires graph DDL privilege; setup script must grant it
(or we degrade: v1 ships a SQL `CONNECT BY` / recursive-CTE fallback — see Risks).

### 2. Parser (pure Go, zero new deps for v1)

- **Go:** `go/parser` + `go/ast` (stdlib) → files, packages, funcs, methods,
  types, consts/vars, `import` edges, **call edges** via AST call-expression
  resolution (same-package + exported symbol heuristic), doc comments.
- **Python/JS (v1 heuristic):** line-based symbol scan — `def | class | func
  | const | function` + import/require edges via regex. Explicitly *not* a full
  parser; good enough for structure, never wrong about paths because paths come
  from the actual tree walk.
- `co_edit` edges: files that changed in the same commit (from `git log
  --name-only` when repo is a git checkout) — the CKG pattern.
- Incremental: `index --repo <path>` upserts by stable `node_id`; a `mtime`
  guard skips unchanged files (`stat` + stored hash in `PICO_META`).

### 3. Embedding source text per node

```
file:    <package/imports>/<first doc comment>/<first 40 lines>  (no code bodies)
func:    signature + doc comment + local symbol names
type:    name + doc + field/method names (no bodies)
```

This keeps MiniLM embeddings about *structure and intent*, not code text —
matching how "find the code that handles SSE" maps to symbols.

### 4. Tool — `code_search`

New tool in `pkg/tools/code_search.go`, registered in `pkg/agent/loop.go`
behind `cfg.Agent.CodeSearch.Enabled` (default on when Oracle enabled and a
repo is indexed):

| arg | meaning |
|---|---|
| `query` | natural-language intent (vector channel) |
| `symbol` | exact symbol to trace (structural channel, e.g. `EmbedText`) |
| `callers_of` | who calls `symbol` (graph, 1 hop) |
| `what_calls` | transitively, up to `depth` (default 2) |
| `path` | restrict to a file/subdir |
| `max_results` | default 8 |

**Hybrid retrieval (reuse Spec 01 RRF):**
- Channel A: `VECTOR_DISTANCE` over `PICO_CODE_NODES` (top 50).
- Channel B: `CONTAINS(summary/doc/name, query)` lexical (top 50).
- Fuse via RRF; node kind preference (`func` > `type` > `file`) as tiebreak.

**Structural:** `GRAPH_TABLE MATCH`:

```sql
SELECT n.name, n.path, n.kind, e.kind AS how, m.name AS via
FROM GRAPH_TABLE (PICO_CODE_GRAPH
  MATCH (src)-[e]->(n)-[e2]->(m)
  WHERE src.name = :symbol AND n.agent_id = :a AND n.repo = :r
  COLUMNS (n.name, n.path, n.kind, e.kind, m.name)) t
```

Tool result renders a tree; the model gets real paths + line numbers so its
follow-up `read_file` calls are grounded.

### 5. CLI

```
picooraclaw index <path> [--repo name] [--force]   # parse + upsert + build graph
picooraclaw index status                            # per-repo node/edge counts, last indexed
picooraclaw code-search "query" [--symbol X] [--path dir] [--depth 2]
```

`oracle-inspect` gains `code` table view.

## Files touched

- `pkg/oracle/code_graph.go` (new) — schema DDL, upsert, graph DDL, traversal
- `pkg/oracle/code_parse_go.go` (new) — go/parser → nodes/edges
- `pkg/oracle/code_parse_generic.go` (new) — py/js heuristic
- `pkg/tools/code_search.go` (new)
- `pkg/agent/loop.go` — register tool (config-gated)
- `cmd/picooraclaw/main.go` — `index` + `code-search` subcommands
- `scripts/setup-oracle.sh` — grants (`CREATE PROPERTY GRAPH`), optional
  auto-index of the repo dir
- `docs/` — usage page

## Testing

- Parser unit tests: fixture repos (small Go + Python trees) → expected
  node/edge counts, call edges resolved, doc extraction.
- sqlmock: upsert SQL shape, RRF fusion over nodes, graph traversal SQL shape.
- Container integration: `index` a fixture repo in a temp dir, run
  `code_search` for NL + `callers_of`, assert results.
- Property graph creation failure → recursive-CTE fallback path.

## Risks

- **PGQ privilege / availability** — 23ai+ only; Free/ADB 26ai has it, but the
  fallback (recursive CTE over `PICO_CODE_EDGES`) is implemented anyway; it is
  also the faster path for 1–2 hops, so PGQ can be a progressive enhancement.
- **Parser accuracy** — Go is exact (stdlib AST); py/js heuristic can miss
  dynamic calls; tool result should say "heuristic" so the model trusts paths
  but not exhaustiveness.
- **Embedding cost on large repos** — batch 50 nodes per `VECTOR_EMBEDDING`
  call, incremental re-index, cap at 20k nodes with a warning.

## Acceptance criteria

- `picooraclaw index .` on this repo indexes ≥ 1 node/file and call edges for
  `float32SliceToString`-style intra-package calls.
- `code_search "handles SSE events"` returns `pkg/channels/web/server.go`
  `handleEvents` in top 3 without the model ever being told the path.
- `code_search --callers_of float32SliceToString` returns `memory_store.go`.
- Re-index is idempotent (no duplicate nodes/edges).
- Oracle-disabled mode: `code_search` returns a "not indexed / Oracle off"
  result, never errors the loop.
