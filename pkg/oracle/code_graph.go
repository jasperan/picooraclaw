package oracle

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jasperan/picooraclaw/pkg/logger"
)

// CodeNode is one vertex in the code knowledge graph (Spec 03).
type CodeNode struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Kind      string `json:"kind"` // file | func | type | method | const | var | class
	Name      string `json:"name"`
	Path      string `json:"path"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
	Summary   string `json:"summary"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// CodeEdge is one graph edge between nodes.
type CodeEdge struct {
	ID     string `json:"id"`
	Repo   string `json:"repo"`
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Kind   string `json:"kind"` // imports | calls | co_edit | defines
	Weight int    `json:"weight"`
}

// CodeSearchHit is a ranked search result.
type CodeSearchHit struct {
	NodeID    string   `json:"node_id"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	Signature string   `json:"signature"`
	Doc       string   `json:"doc"`
	Score     float64  `json:"score"`
	Callers   []string `json:"callers,omitempty"`
	Callees   []string `json:"callees,omitempty"`
}

// CodeGraphStore persists and queries the code knowledge graph.
type CodeGraphStore struct {
	db        *sql.DB
	agentID   string
	modelName string
	embedding *EmbeddingService
}

// NewCodeGraphStore creates a code graph store.
func NewCodeGraphStore(db *sql.DB, agentID string, embedding *EmbeddingService) *CodeGraphStore {
	modelName := ""
	if embedding != nil {
		modelName = embedding.ModelName()
	}
	if modelName != "" {
		if err := validateSQLIdentifier(modelName); err != nil {
			modelName = ""
		}
	}
	return &CodeGraphStore{db: db, agentID: agentID, modelName: modelName, embedding: embedding}
}

// CodeNodeID derives a stable node id from kind + relative path + name.
func CodeNodeID(kind, path, name string) string {
	h := sha1.Sum([]byte(kind + ":" + path + ":" + name))
	return hex.EncodeToString(h[:])[:20]
}

// CodeEdgeID derives a stable edge id.
func CodeEdgeID(kind, src, dst string) string {
	h := sha1.Sum([]byte(kind + ":" + src + ":" + dst))
	return hex.EncodeToString(h[:])[:20]
}

// UpsertNodes replaces the repo's nodes with the given set (delete-then-insert
// per repo keeps re-indexing idempotent). Returns the number inserted.
func (gs *CodeGraphStore) UpsertNodes(repo string, nodes []CodeNode) (int, error) {
	if gs == nil || gs.db == nil {
		return 0, fmt.Errorf("code graph store not available")
	}
	if _, err := gs.db.Exec("DELETE FROM PICO_CODE_NODES WHERE agent_id = :1 AND repo = :2", gs.agentID, repo); err != nil {
		return 0, err
	}
	if _, err := gs.db.Exec("DELETE FROM PICO_CODE_EDGES WHERE agent_id = :1 AND repo = :2", gs.agentID, repo); err != nil {
		return 0, err
	}

	inserted := 0
	for i := range nodes {
		n := &nodes[i]
		if n.ID == "" {
			n.ID = CodeNodeID(n.Kind, n.Path, n.Name)
		}
		if len(n.Summary) > 2000 {
			n.Summary = n.Summary[:2000]
		}
		var err error
		if gs.modelName != "" && gs.embedding != nil && gs.embedding.Mode() == "onnx" {
			_, err = gs.db.Exec(fmt.Sprintf(`
				INSERT INTO PICO_CODE_NODES (node_id, agent_id, repo, kind, name, path, signature, doc, summary, embedding, start_line, end_line)
				VALUES (:1, :2, :3, :4, :5, :6, :7, :8, :9, VECTOR_EMBEDDING(%s USING :10 AS DATA), :11, :12)`,
				gs.modelName),
				n.ID, gs.agentID, repo, n.Kind, n.Name, n.Path, n.Signature, n.Doc, n.Summary, n.Summary, n.StartLine, n.EndLine)
		} else {
			_, err = gs.db.Exec(`
				INSERT INTO PICO_CODE_NODES (node_id, agent_id, repo, kind, name, path, signature, doc, summary, start_line, end_line)
				VALUES (:1, :2, :3, :4, :5, :6, :7, :8, :9, :10, :11)`,
				n.ID, gs.agentID, repo, n.Kind, n.Name, n.Path, n.Signature, n.Doc, n.Summary, n.StartLine, n.EndLine)
		}
		if err != nil {
			logger.WarnCF("oracle", "code node insert failed", map[string]interface{}{"error": err.Error(), "name": n.Name})
			continue
		}
		inserted++
	}
	return inserted, nil
}

// UpsertEdges inserts the repo's edges (called after UpsertNodes).
func (gs *CodeGraphStore) UpsertEdges(repo string, edges []CodeEdge) (int, error) {
	if gs == nil || gs.db == nil {
		return 0, fmt.Errorf("code graph store not available")
	}
	inserted := 0
	for i := range edges {
		e := &edges[i]
		if e.ID == "" {
			e.ID = CodeEdgeID(e.Kind, e.Src, e.Dst)
		}
		if _, err := gs.db.Exec(`
			INSERT INTO PICO_CODE_EDGES (edge_id, agent_id, repo, src_node_id, dst_node_id, kind, weight)
			VALUES (:1, :2, :3, :4, :5, :6, :7)`,
			e.ID, gs.agentID, repo, e.Src, e.Dst, e.Kind, e.Weight); err != nil {
			logger.WarnCF("oracle", "code edge insert failed", map[string]interface{}{"error": err.Error()})
			continue
		}
		inserted++
	}
	return inserted, nil
}

// Stats returns node/edge counts for a repo.
func (gs *CodeGraphStore) Stats(repo string) (nodes, edges int, err error) {
	if gs == nil || gs.db == nil {
		return 0, 0, fmt.Errorf("code graph store not available")
	}
	if err = gs.db.QueryRow("SELECT COUNT(*) FROM PICO_CODE_NODES WHERE agent_id = :1 AND repo = :2", gs.agentID, repo).Scan(&nodes); err != nil {
		return 0, 0, err
	}
	if err = gs.db.QueryRow("SELECT COUNT(*) FROM PICO_CODE_EDGES WHERE agent_id = :1 AND repo = :2", gs.agentID, repo).Scan(&edges); err != nil {
		return 0, 0, err
	}
	return nodes, edges, nil
}

// SearchNL finds nodes matching a natural-language query using vector +
// lexical evidence (RRF-lite fused in Go for simplicity).
func (gs *CodeGraphStore) SearchNL(repo, query string, limit int) ([]CodeSearchHit, error) {
	if gs == nil || gs.db == nil {
		return nil, fmt.Errorf("code graph store not available")
	}
	if gs.embedding == nil {
		return nil, fmt.Errorf("embedding service not available")
	}
	if limit <= 0 {
		limit = 8
	}

	// Vector channel (ONNX inline or API pre-embed).
	var rows *sql.Rows
	var err error
	if gs.modelName != "" && gs.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT node_id, kind, name, path, signature, doc, start_line,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS dist
			FROM PICO_CODE_NODES
			WHERE agent_id = :2 AND repo = :3 AND embedding IS NOT NULL
			ORDER BY dist ASC FETCH FIRST 60 ROWS ONLY`, gs.modelName)
		rows, err = gs.db.Query(sqlQuery, query, gs.agentID, repo)
	} else {
		qvec, embErr := gs.embedding.EmbedText(query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to embed query: %w", embErr)
		}
		rows, err = gs.db.Query(`
			SELECT node_id, kind, name, path, signature, doc, start_line,
			       VECTOR_DISTANCE(embedding, TO_VECTOR(:1), COSINE) AS dist
			FROM PICO_CODE_NODES
			WHERE agent_id = :2 AND repo = :3 AND embedding IS NOT NULL
			ORDER BY dist ASC FETCH FIRST 60 ROWS ONLY`,
			float32SliceToString(qvec), gs.agentID, repo)
	}
	if err != nil {
		return nil, fmt.Errorf("code search failed: %w", err)
	}
	defer rows.Close()

	vec := map[string]CodeSearchHit{}
	order := []string{}
	for rows.Next() {
		var h CodeSearchHit
		var doc, sig sql.NullString
		var dist float64
		if err := rows.Scan(&h.NodeID, &h.Kind, &h.Name, &h.Path, &sig, &doc, &h.Line, &dist); err != nil {
			continue
		}
		h.Signature = sig.String
		h.Doc = doc.String
		h.Score = 1.0 - dist
		if h.Score >= 0.25 {
			vec[h.NodeID] = h
			order = append(order, h.NodeID)
		}
	}

	// Lexical channel: tokenized INSTR over name/summary/path.
	var lexRows *sql.Rows
	tokens := lexicalTokens(query)
	if len(tokens) > 0 {
		preds := make([]string, 0, len(tokens))
		args := []interface{}{gs.agentID, repo}
		for _, tok := range tokens {
			b := len(args) + 1
			preds = append(preds, fmt.Sprintf("(INSTR(LOWER(name), LOWER(:%d)) > 0 OR INSTR(LOWER(NVL(summary,'')), LOWER(:%d)) > 0)", b, b))
			args = append(args, tok)
		}
		q := fmt.Sprintf(`
			SELECT node_id, kind, name, path, signature, NVL(doc,''), start_line
			FROM PICO_CODE_NODES
			WHERE agent_id = :1 AND repo = :2 AND (%s)
			FETCH FIRST 60 ROWS ONLY`, strings.Join(preds, " OR "))
		lexRows, err = gs.db.Query(q, args...)
		if err == nil {
			defer lexRows.Close()
			for lexRows.Next() {
				var h CodeSearchHit
				var doc, sig sql.NullString
				if err := lexRows.Scan(&h.NodeID, &h.Kind, &h.Name, &h.Path, &sig, &doc, &h.Line); err != nil {
					continue
				}
				h.Signature = sig.String
				h.Doc = doc.String
				if existing, ok := vec[h.NodeID]; ok {
					h.Score = existing.Score + 0.15 // lexical boost
				} else {
					h.Score = 0.4
					vec[h.NodeID] = h
					order = append(order, h.NodeID)
				}
			}
		}
	}

	out := make([]CodeSearchHit, 0, len(order))
	for _, id := range order {
		h := vec[id]
		// kind preference: funcs over files over others
		switch h.Kind {
		case "func", "method":
			h.Score += 0.05
		case "file":
			h.Score -= 0.02
		}
		out = append(out, h)
	}
	// stable sort by score desc
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CallersOf returns nodes that (transitively, to depth) call the symbol.
func (gs *CodeGraphStore) CallersOf(repo, symbol string, depth, limit int) ([]CodeSearchHit, error) {
	return gs.traverse(repo, symbol, depth, limit, true)
}

// CalleesOf returns nodes the symbol calls (transitively, to depth).
func (gs *CodeGraphStore) CalleesOf(repo, symbol string, depth, limit int) ([]CodeSearchHit, error) {
	return gs.traverse(repo, symbol, depth, limit, false)
}

// traverse walks call edges with a recursive CTE. dir=true → callers
// (reverse edges), false → callees (forward edges).
func (gs *CodeGraphStore) traverse(repo, symbol string, depth, limit int, callers bool) ([]CodeSearchHit, error) {
	if gs == nil || gs.db == nil {
		return nil, fmt.Errorf("code graph store not available")
	}
	if depth <= 0 {
		depth = 2
	}
	if limit <= 0 {
		limit = 20
	}

	// Resolve the symbol to a node id.
	var srcID string
	err := gs.db.QueryRow(`
		SELECT node_id FROM PICO_CODE_NODES
		WHERE agent_id = :1 AND repo = :2 AND name = :3 AND kind IN ('func','method')
		FETCH FIRST 1 ROW ONLY`, gs.agentID, repo, symbol).Scan(&srcID)
	if err != nil {
		// Try any kind.
		err = gs.db.QueryRow(`
			SELECT node_id FROM PICO_CODE_NODES
			WHERE agent_id = :1 AND repo = :2 AND name = :3
			FETCH FIRST 1 ROW ONLY`, gs.agentID, repo, symbol).Scan(&srcID)
		if err != nil {
			return nil, fmt.Errorf("symbol %q not found in indexed repo", symbol)
		}
	}

	direction := "dst_node_id"
	startCol := "src_node_id"
	if callers {
		direction, startCol = "src_node_id", "dst_node_id"
	}

	// Every bind placeholder appears exactly once (go-ora quirk): agent and
	// repo are duplicated per occurrence.
	q := fmt.Sprintf(`
		WITH reach(node_id, depth) AS (
			SELECT %s, 1 FROM PICO_CODE_EDGES
			WHERE %s = :1 AND agent_id = :2 AND repo = :3 AND kind = 'calls'
			UNION ALL
			SELECT e.%s, r.depth + 1
			FROM PICO_CODE_EDGES e JOIN reach r ON e.%s = r.node_id
			WHERE e.agent_id = :4 AND e.repo = :5 AND e.kind = 'calls' AND r.depth < :6
		)
		SELECT n.node_id, n.kind, n.name, n.path, NVL(n.signature,' '), n.start_line, MIN(r.depth)
		FROM reach r JOIN PICO_CODE_NODES n ON n.node_id = r.node_id
		WHERE n.agent_id = :7 AND n.repo = :8
		GROUP BY n.node_id, n.kind, n.name, n.path, NVL(n.signature,' '), n.start_line
		ORDER BY MIN(r.depth), n.name
		FETCH FIRST %d ROWS ONLY`,
		direction, startCol, direction, startCol, limit)

	rows, err := gs.db.Query(q, srcID, gs.agentID, repo, gs.agentID, repo, depth, gs.agentID, repo)
	if err != nil {
		return nil, fmt.Errorf("code traversal failed: %w", err)
	}
	defer rows.Close()

	var out []CodeSearchHit
	for rows.Next() {
		var h CodeSearchHit
		var sig sql.NullString
		var d int
		if err := rows.Scan(&h.NodeID, &h.Kind, &h.Name, &h.Path, &sig, &h.Line, &d); err != nil {
			continue
		}
		h.Signature = sig.String
		h.Score = 1.0 - float64(d)*0.1
		out = append(out, h)
	}
	return out, nil
}

// RepoList returns indexed repos (for `index status`).
func (gs *CodeGraphStore) RepoList() ([]string, error) {
	if gs == nil || gs.db == nil {
		return nil, nil
	}
	rows, err := gs.db.Query(
		"SELECT DISTINCT repo FROM PICO_CODE_NODES WHERE agent_id = :1 ORDER BY repo", gs.agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}
