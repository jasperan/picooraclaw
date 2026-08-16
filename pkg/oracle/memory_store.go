package oracle

import (
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasperan/picooraclaw/pkg/logger"
)

// Recall and deduplication tuning constants.
const (
	// recallMinScore is the minimum fused score for a memory to be returned by
	// Recall (retained for backward compatibility; RecallOpts uses MinScore).
	recallMinScore = 0.15
	// dedupMaxDistance is the maximum cosine distance below which two memories
	// are treated as near-duplicates (i.e. ~95%+ similarity).
	dedupMaxDistance = 0.05
	// defaultLongTermImportance is the importance assigned to long-term memories
	// written via WriteLongTerm.
	defaultLongTermImportance = 0.7
)

// RecallOptions controls hybrid retrieval. Zero value behaves like the legacy
// Recall call except for the lower (fused) score floor.
type RecallOptions struct {
	Category string  // exact category filter ("" = all)
	MinScore float64 // fused score floor; 0 = default 0.15
	Lexical  bool    // enable the lexical channel (default true)
	Days     int     // only memories created within N days (0 = all)
}

func (o RecallOptions) normalized() RecallOptions {
	if o.MinScore <= 0 {
		o.MinScore = recallMinScore
	}
	return o
}

// MemoryRecallResult represents a single recalled memory with similarity score.
type MemoryRecallResult struct {
	MemoryID    string    `json:"memory_id"`
	Text        string    `json:"text"`
	Importance  float64   `json:"importance"`
	Category    string    `json:"category"`
	Score       float64   `json:"score"`
	AccessCount int       `json:"access_count,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	AccessedAt  time.Time `json:"accessed_at,omitempty"`
}

// MemoryStore implements MemoryStoreInterface and OracleMemoryStore backed by Oracle.
type MemoryStore struct {
	db          *sql.DB
	agentID     string
	embedding   *EmbeddingService
	modelName   string // ONNX model name for VECTOR_EMBEDDING() SQL
	lexicalMode string // "instr" (default), "text", or "off"
}

// NewMemoryStore creates a new Oracle-backed memory store.
func NewMemoryStore(db *sql.DB, agentID string, embedding *EmbeddingService) *MemoryStore {
	modelName := ""
	if embedding != nil {
		modelName = embedding.ModelName()
	}
	if modelName != "" {
		if err := validateSQLIdentifier(modelName); err != nil {
			logger.WarnCF("oracle", "Invalid model name for MemoryStore, disabling embedding SQL", map[string]interface{}{"error": err.Error(), "modelName": modelName})
			modelName = ""
		}
	}
	lexMode := getMetaValue(db, "oracle.lexical_mode")
	if lexMode == "" {
		lexMode = "instr"
	}
	return &MemoryStore{
		db:          db,
		agentID:     agentID,
		embedding:   embedding,
		modelName:   modelName,
		lexicalMode: lexMode,
	}
}

// SetLexicalMode overrides the lexical channel resolution.
func (ms *MemoryStore) SetLexicalMode(mode string) {
	switch mode {
	case "text", "instr", "off":
		ms.lexicalMode = mode
	}
}

// ReadLongTerm reads all long-term memories, joined with "---" separator.
func (ms *MemoryStore) ReadLongTerm() string {
	// Order by importance with time-decay: recently accessed memories rank higher
	rows, err := ms.db.Query(
		"SELECT content FROM PICO_MEMORIES WHERE agent_id = :1 ORDER BY (importance * (1.0 / (1.0 + (SYSDATE - CAST(NVL(accessed_at, created_at) AS DATE)) * 0.1))) DESC, CAST(created_at AS DATE) DESC FETCH FIRST 50 ROWS ONLY",
		ms.agentID,
	)
	if err != nil {
		logger.WarnCF("oracle", "Failed to read long-term memories", map[string]interface{}{"error": err.Error()})
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var content sql.NullString
		if err := rows.Scan(&content); err == nil && content.Valid {
			parts = append(parts, content.String)
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// WriteLongTerm stores a new long-term memory with embedding.
func (ms *MemoryStore) WriteLongTerm(content string) error {
	_, err := ms.Remember(content, defaultLongTermImportance, "long_term")
	return err
}

// ReadToday reads today's daily note.
func (ms *MemoryStore) ReadToday() string {
	var content sql.NullString
	err := ms.db.QueryRow(
		"SELECT content FROM PICO_DAILY_NOTES WHERE agent_id = :1 AND note_date = TRUNC(SYSDATE) ORDER BY updated_at DESC FETCH FIRST 1 ROW ONLY",
		ms.agentID,
	).Scan(&content)
	if err != nil || !content.Valid {
		return ""
	}
	return content.String
}

// AppendToday appends content to today's daily note.
func (ms *MemoryStore) AppendToday(content string) error {
	// Try to get existing today note
	existing := ms.ReadToday()

	if existing == "" {
		// Insert new daily note
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		fullContent := header + content

		noteID := uuid.New().String()[:8]

		if ms.modelName != "" && ms.embedding != nil && ms.embedding.Mode() == "onnx" {
			query := fmt.Sprintf(`
				INSERT INTO PICO_DAILY_NOTES (note_id, agent_id, note_date, content, embedding)
				VALUES (:1, :2, TRUNC(SYSDATE), :3, VECTOR_EMBEDDING(%s USING :4 AS DATA))`,
				ms.modelName,
			)
			_, err := ms.db.Exec(query, noteID, ms.agentID, fullContent, fullContent)
			return err
		}

		_, err := ms.db.Exec(`
			INSERT INTO PICO_DAILY_NOTES (note_id, agent_id, note_date, content)
			VALUES (:1, :2, TRUNC(SYSDATE), :3)`,
			noteID, ms.agentID, fullContent,
		)
		return err
	}

	// Append to existing
	newContent := existing + "\n" + content

	if ms.modelName != "" && ms.embedding != nil && ms.embedding.Mode() == "onnx" {
		query := fmt.Sprintf(`
			UPDATE PICO_DAILY_NOTES
			SET content = :1, embedding = VECTOR_EMBEDDING(%s USING :2 AS DATA), updated_at = CURRENT_TIMESTAMP
			WHERE agent_id = :3 AND note_date = TRUNC(SYSDATE)`,
			ms.modelName,
		)
		_, err := ms.db.Exec(query, newContent, newContent, ms.agentID)
		return err
	}

	_, err := ms.db.Exec(`
		UPDATE PICO_DAILY_NOTES
		SET content = :1, updated_at = CURRENT_TIMESTAMP
		WHERE agent_id = :2 AND note_date = TRUNC(SYSDATE)`,
		newContent, ms.agentID,
	)
	return err
}

// GetRecentDailyNotes returns daily notes from the last N days.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	rows, err := ms.db.Query(
		"SELECT content FROM PICO_DAILY_NOTES WHERE agent_id = :1 AND note_date >= TRUNC(SYSDATE) - :2 ORDER BY note_date DESC",
		ms.agentID, days,
	)
	if err != nil {
		logger.WarnCF("oracle", "Failed to read recent daily notes", map[string]interface{}{"error": err.Error()})
		return ""
	}
	defer rows.Close()

	var notes []string
	for rows.Next() {
		var content sql.NullString
		if err := rows.Scan(&content); err == nil && content.Valid {
			notes = append(notes, content.String)
		}
	}

	if len(notes) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, note := range notes {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString(note)
	}
	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent prompt.
func (ms *MemoryStore) GetMemoryContext() string {
	var parts []string

	longTerm := ms.ReadLongTerm()
	if longTerm != "" {
		parts = append(parts, "## Long-term Memory\n\n"+longTerm)
	}

	recentNotes := ms.GetRecentDailyNotes(3)
	if recentNotes != "" {
		parts = append(parts, "## Recent Daily Notes\n\n"+recentNotes)
	}

	if len(parts) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, part := range parts {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString(part)
	}
	return fmt.Sprintf("# Memory\n\n%s", sb.String())
}

// Remember stores a new memory with embedding for vector search.
// Uses Oracle's in-database VECTOR_EMBEDDING() to compute the embedding inline.
// Checks for near-duplicate memories before inserting to prevent clutter.
func (ms *MemoryStore) Remember(text string, importance float64, category string) (string, error) {
	// Check for near-duplicate memories before inserting
	if existingID, updated := ms.deduplicateMemory(text, importance); updated {
		return existingID, nil
	}

	memoryID := uuid.New().String()[:8]

	if ms.modelName != "" && ms.embedding != nil && ms.embedding.Mode() == "onnx" {
		// Use VECTOR_EMBEDDING() inline - Oracle computes the embedding in-database
		// Pass text twice: once for content column, once for VECTOR_EMBEDDING
		query := fmt.Sprintf(`
			INSERT INTO PICO_MEMORIES (memory_id, agent_id, content, embedding, importance, category)
			VALUES (:1, :2, :3, VECTOR_EMBEDDING(%s USING :4 AS DATA), :5, :6)`,
			ms.modelName,
		)
		_, err := ms.db.Exec(query, memoryID, ms.agentID, text, text, importance, category)
		if err != nil {
			return "", fmt.Errorf("failed to remember: %w", err)
		}
	} else if ms.embedding != nil && ms.embedding.Mode() == "api" {
		// API mode: compute embedding via external API, convert to string for TO_VECTOR()
		emb, err := ms.embedding.EmbedText(text)
		if err != nil {
			logger.WarnCF("oracle", "Embedding failed, storing without vector", map[string]interface{}{"error": err.Error()})
			_, err = ms.db.Exec(`
				INSERT INTO PICO_MEMORIES (memory_id, agent_id, content, importance, category)
				VALUES (:1, :2, :3, :4, :5)`,
				memoryID, ms.agentID, text, importance, category,
			)
			if err != nil {
				return "", fmt.Errorf("failed to remember: %w", err)
			}
		} else {
			vecStr := float32SliceToString(emb)
			_, err = ms.db.Exec(`
				INSERT INTO PICO_MEMORIES (memory_id, agent_id, content, embedding, importance, category)
				VALUES (:1, :2, :3, TO_VECTOR(:4), :5, :6)`,
				memoryID, ms.agentID, text, vecStr, importance, category,
			)
			if err != nil {
				return "", fmt.Errorf("failed to remember: %w", err)
			}
		}
	} else {
		// No embedding available
		_, err := ms.db.Exec(`
			INSERT INTO PICO_MEMORIES (memory_id, agent_id, content, importance, category)
			VALUES (:1, :2, :3, :4, :5)`,
			memoryID, ms.agentID, text, importance, category,
		)
		if err != nil {
			return "", fmt.Errorf("failed to remember: %w", err)
		}
	}

	logger.InfoCF("oracle", "Memory stored", map[string]interface{}{
		"memory_id":  memoryID,
		"importance": importance,
		"category":   category,
	})
	return memoryID, nil
}

// Recall performs hybrid semantic + lexical search on memories using defaults.
func (ms *MemoryStore) Recall(query string, maxResults int) ([]MemoryRecallResult, error) {
	return ms.RecallOpts(query, maxResults, RecallOptions{Lexical: true})
}

// RecallOpts performs hybrid retrieval: a vector channel and a lexical channel
// (INSTR-based, privilege-free), fused with reciprocal rank fusion in Go, then
// re-ranked by importance × recency × accessibility decay.
//
// go-ora quirk: every bind placeholder must appear exactly once per statement,
// so the fused query is split into two simple single-use-bind queries and the
// fusion happens in Go. This is equivalent to a single SQL RRF query and is
// robust across Oracle versions and driver behaviors.
func (ms *MemoryStore) RecallOpts(query string, maxResults int, opts RecallOptions) ([]MemoryRecallResult, error) {
	if ms.embedding == nil {
		return nil, fmt.Errorf("embedding service not available")
	}
	opts = opts.normalized()
	if maxResults <= 0 {
		maxResults = 5
	}

	// --- Channel A: vector (simple query, single-use binds) ---
	vecRows, err := ms.vectorChannel(query, opts)
	if err != nil {
		return nil, err
	}

	// --- Channel B: lexical (tokenized INSTR, single-use binds) ---
	lexRows := []lexHit{}
	if opts.Lexical && ms.lexicalMode != "off" {
		lexRows, err = ms.lexicalChannel(query, opts)
		if err != nil {
			logger.WarnCF("oracle", "Lexical channel failed, continuing with vector only", map[string]interface{}{"error": err.Error()})
		}
	}

	// --- Fuse via RRF over rank positions ---
	rrf := map[string]float64{}
	for i, r := range vecRows {
		rrf[r.MemoryID] += 1.0 / (60.0 + float64(i+1))
	}
	for i, r := range lexRows {
		rrf[r.memoryID] += 1.0 / (60.0 + float64(i+1))
	}

	// --- Load details for lexical-only ids ---
	known := map[string]bool{}
	for _, r := range vecRows {
		known[r.MemoryID] = true
	}
	var extraIDs []string
	for _, r := range lexRows {
		if !known[r.memoryID] {
			extraIDs = append(extraIDs, r.memoryID)
		}
	}
	details := map[string]MemoryRecallResult{}
	for _, r := range vecRows {
		details[r.MemoryID] = r.result
	}
	if len(extraIDs) > 0 {
		extra, err := ms.detailsForIDs(extraIDs, opts)
		if err == nil {
			for id, res := range extra {
				details[id] = res
			}
		}
	}

	// --- Decay score + filter + sort ---
	results := make([]MemoryRecallResult, 0, len(rrf))
	maxRRF := 0.0
	for _, v := range rrf {
		if v > maxRRF {
			maxRRF = v
		}
	}
	var memoryIDs []string
	for id, v := range rrf {
		res, ok := details[id]
		if !ok {
			continue
		}
		res.Score = decayScore(v, maxRRF, res.Importance, res.AccessCount, res.CreatedAt, res.AccessedAt)
		if res.Score >= opts.MinScore {
			results = append(results, res)
			memoryIDs = append(memoryIDs, id)
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	if len(memoryIDs) > 0 {
		ms.updateAccessTimestamps(memoryIDs[:len(results)])
	}
	return results, nil
}

// vecHit is a ranked vector-channel result with its details.
type vecHit struct {
	MemoryID string
	result   MemoryRecallResult
}

// lexHit is a ranked lexical-channel hit (id + rank).
type lexHit struct {
	memoryID string
}

// vectorChannel runs the top-N vector similarity query (single-use binds).
func (ms *MemoryStore) vectorChannel(query string, opts RecallOptions) ([]vecHit, error) {
	limit := 100
	args := []interface{}{ms.agentID}
	filterSQL, args := appendRecallFilters(args, opts)
	var rows *sql.Rows
	var err error
	if ms.modelName != "" && ms.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT memory_id, content, importance, category, access_count, created_at, accessed_at,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS dist
			FROM PICO_MEMORIES
			WHERE agent_id = :2 AND embedding IS NOT NULL%s
			ORDER BY dist ASC
			FETCH FIRST %d ROWS ONLY`, ms.modelName, filterSQL, limit)
		rows, err = ms.db.Query(sqlQuery, append([]interface{}{query}, args...)...)
	} else {
		queryVec, embErr := ms.embedding.EmbedText(query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to embed query: %w", embErr)
		}
		sqlQuery := `
			SELECT memory_id, content, importance, category, access_count, created_at, accessed_at,
			       VECTOR_DISTANCE(embedding, TO_VECTOR(:1), COSINE) AS dist
			FROM PICO_MEMORIES
			WHERE agent_id = :2 AND embedding IS NOT NULL` + filterSQL + `
			ORDER BY dist ASC
			FETCH FIRST ` + strconv.Itoa(limit) + ` ROWS ONLY`
		rows, err = ms.db.Query(sqlQuery, append([]interface{}{float32SliceToString(queryVec)}, args...)...)
	}
	if err != nil {
		return nil, fmt.Errorf("recall vector query failed: %w", err)
	}
	defer rows.Close()

	var hits []vecHit
	for rows.Next() {
		var h vecHit
		var content, category sql.NullString
		var accessed sql.NullTime
		var dist float64
		if err := rows.Scan(&h.MemoryID, &content, &h.result.Importance, &category, &h.result.AccessCount, &h.result.CreatedAt, &accessed, &dist); err != nil {
			continue
		}
		if content.Valid {
			h.result.Text = content.String
		}
		if category.Valid {
			h.result.Category = category.String
		}
		h.result.MemoryID = h.MemoryID
		h.result.AccessedAt = accessed.Time
		hits = append(hits, h)
	}
	return hits, nil
}

// appendRecallFilters appends category/recency filter SQL using fresh bind
// numbers and returns the matching args (SQL text order == args order).
func appendRecallFilters(args []interface{}, opts RecallOptions) (string, []interface{}) {
	var sb strings.Builder
	if opts.Category != "" {
		sb.WriteString(" AND category = :" + strconv.Itoa(len(args)+1))
		args = append(args, opts.Category)
	}
	if opts.Days > 0 {
		sb.WriteString(" AND created_at >= SYSDATE - :" + strconv.Itoa(len(args)+1))
		args = append(args, opts.Days)
	}
	return sb.String(), args
}

// lexicalChannel returns the top-N lexical matches by INSTR score
// (single-use binds; ids only, scores computed in SQL).
func (ms *MemoryStore) lexicalChannel(query string, opts RecallOptions) ([]lexHit, error) {
	tokens := lexicalTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	if len(tokens) > 6 {
		tokens = tokens[:6]
	}
	args := []interface{}{ms.agentID}
	filterSQL, args := appendRecallFilters(args, opts)
	preds := make([]string, 0, len(tokens))
	scores := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		b := len(args) + 1
		preds = append(preds, fmt.Sprintf("INSTR(LOWER(content), LOWER(:%d)) > 0", b))
		scores = append(scores, fmt.Sprintf("INSTR(LOWER(content), LOWER(:%d))", b))
		args = append(args, tok)
	}
	limit := 100
	sqlQuery := fmt.Sprintf(`
		SELECT memory_id, (%s) AS score
		FROM PICO_MEMORIES
		WHERE agent_id = :1%s AND (%s)
		ORDER BY score DESC
		FETCH FIRST %d ROWS ONLY`,
		strings.Join(scores, " + "), filterSQL, strings.Join(preds, " OR "), limit)
	rows, err := ms.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []lexHit
	for rows.Next() {
		var id string
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			continue
		}
		if score > 0 {
			hits = append(hits, lexHit{memoryID: id})
		}
	}
	return hits, nil
}

// detailsForIDs fetches full rows for lexical-only ids using an IN clause
// (each bind used once).
func (ms *MemoryStore) detailsForIDs(ids []string, opts RecallOptions) (map[string]MemoryRecallResult, error) {
	out := map[string]MemoryRecallResult{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		ids = ids[:100]
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, ms.agentID)
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf(":%d", i+2)
		args = append(args, id)
	}
	sqlQuery := fmt.Sprintf(`
		SELECT memory_id, content, importance, category, access_count, created_at, accessed_at
		FROM PICO_MEMORIES
		WHERE agent_id = :1 AND memory_id IN (%s)`, strings.Join(placeholders, ", "))
	rows, err := ms.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var r MemoryRecallResult
		var content, category sql.NullString
		var accessed sql.NullTime
		if err := rows.Scan(&r.MemoryID, &content, &r.Importance, &category, &r.AccessCount, &r.CreatedAt, &accessed); err != nil {
			continue
		}
		if content.Valid {
			r.Text = content.String
		}
		if category.Valid {
			r.Category = category.String
		}
		r.AccessedAt = accessed.Time
		out[r.MemoryID] = r
	}
	return out, nil
}

// recallVectorOnly is the fallback when the fused query fails (e.g. older
// Oracle without CTE support). It keeps the vector channel and decay scoring.
func (ms *MemoryStore) recallVectorOnly(query string, maxResults int, opts RecallOptions, vecExpr string, binds []interface{}, filterSQL string) ([]MemoryRecallResult, error) {
	sqlQuery := "SELECT memory_id, content, importance, category, access_count, created_at, accessed_at, 0.0 FROM (" +
		"SELECT memory_id, content, importance, category, access_count, created_at, accessed_at, " + vecExpr + " AS dist " +
		"FROM PICO_MEMORIES WHERE agent_id = :2" + filterSQL + " AND embedding IS NOT NULL" +
		") ORDER BY dist ASC FETCH FIRST :" + strconv.Itoa(len(binds)) + " ROWS ONLY"
	rows, err := ms.db.Query(sqlQuery, binds...)
	if err != nil {
		return nil, fmt.Errorf("recall query failed: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		result   MemoryRecallResult
		rrf      float64
		access   int
		created  time.Time
		accessed time.Time
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		var content, category sql.NullString
		var created, accessed sql.NullTime
		var dist float64
		if err := rows.Scan(&c.result.MemoryID, &content, &c.result.Importance, &category, &c.access, &created, &accessed, &dist); err != nil {
			continue
		}
		if content.Valid {
			c.result.Text = content.String
		}
		if category.Valid {
			c.result.Category = category.String
		}
		c.created = created.Time
		c.accessed = accessed.Time
		// Emulate RRF magnitude from similarity so decay scoring stays consistent.
		c.rrf = (1.0 - dist) * 100
		cands = append(cands, c)
	}

	results := make([]MemoryRecallResult, 0, len(cands))
	var memoryIDs []string
	maxRRF := 0.0
	for _, c := range cands {
		if c.rrf > maxRRF {
			maxRRF = c.rrf
		}
	}
	for _, c := range cands {
		c.result.Score = decayScore(c.rrf, maxRRF, c.result.Importance, c.access, c.created, c.accessed)
		if c.result.Score >= opts.MinScore {
			results = append(results, c.result)
			memoryIDs = append(memoryIDs, c.result.MemoryID)
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	if len(memoryIDs) > 0 {
		ms.updateAccessTimestamps(memoryIDs)
	}
	return results, nil
}

// buildHybridRecallSQL assembles the fused CTE query. Bind layout:
// :1 vec query expr, :2 agent_id, then filters/tokens, limitBind = limit.
func buildHybridRecallSQL(vecExpr, filterSQL string, lexChannel bool, lexPred, lexScore string, limitBind, maxResults int) string {
	if !lexChannel || lexPred == "" {
		return "SELECT memory_id, content, importance, category, access_count, created_at, accessed_at, 0.0 FROM (" +
			"SELECT memory_id, content, importance, category, access_count, created_at, accessed_at, " + vecExpr + " AS dist " +
			"FROM PICO_MEMORIES WHERE agent_id = :2" + filterSQL + " AND embedding IS NOT NULL" +
			") ORDER BY dist ASC FETCH FIRST :" + strconv.Itoa(limitBind) + " ROWS ONLY"
	}

	vecSQL := "SELECT * FROM (SELECT memory_id, ROW_NUMBER() OVER (ORDER BY " + vecExpr + ") rn " +
		"FROM PICO_MEMORIES WHERE agent_id = :2" + filterSQL + " AND embedding IS NOT NULL" +
		") ORDER BY rn FETCH FIRST 100 ROWS ONLY"
	lexSQL := "SELECT * FROM (SELECT memory_id, ROW_NUMBER() OVER (ORDER BY " + lexScore + " DESC) rn " +
		"FROM PICO_MEMORIES WHERE agent_id = :2" + filterSQL + " AND (" + lexPred + ")" +
		") ORDER BY rn FETCH FIRST 100 ROWS ONLY"
	return "WITH vec AS (" + vecSQL + "), lex AS (" + lexSQL + "), fused AS (" +
		"SELECT memory_id, SUM(1.0/(60+rn)) rrf FROM (SELECT memory_id, rn FROM vec UNION ALL SELECT memory_id, rn FROM lex) GROUP BY memory_id) " +
		"SELECT m.memory_id, m.content, m.importance, m.category, m.access_count, m.created_at, m.accessed_at, f.rrf " +
		"FROM fused f JOIN PICO_MEMORIES m ON m.memory_id = f.memory_id " +
		"WHERE m.agent_id = :2" +
		" ORDER BY f.rrf DESC FETCH FIRST :" + strconv.Itoa(limitBind) + " ROWS ONLY"
}

// decayScore ranks a candidate by hybrid evidence (rrf), declared importance,
// and retrievability (accessibility + Ebbinghaus recency decay). Evidence-gated:
// a candidate with no retrieval evidence at all scores 0 and is filtered out.
func decayScore(rrf, maxRRF, importance float64, accessCount int, created, accessed time.Time) float64 {
	rrfNorm := 0.0
	if maxRRF > 0 {
		rrfNorm = rrf / maxRRF
	}
	acc := 1.0
	if accessCount > 0 {
		acc = 1.0 / (1.0 + math.Log2(float64(accessCount)+1.0))
	}
	ref := created
	if !accessed.IsZero() {
		ref = accessed
	}
	days := time.Since(ref).Hours() / 24
	if days < 0 {
		days = 0
	}
	recency := math.Exp(-0.03 * days)
	retrievability := 0.5*acc + 0.5*recency
	quality := 0.4 + 0.35*importance + 0.25*retrievability
	return rrfNorm * quality
}

// lexicalTokens splits a query into lowercase search tokens, keeping the full
// trimmed phrase first (so exact-string lookups like "ORA-30081" match) and
// dropping stopwords and single characters.
func lexicalTokens(query string) []string {
	var out []string
	phrase := strings.ToLower(strings.TrimSpace(query))
	if phrase != "" {
		out = append(out, phrase)
	}
	re := regexp.MustCompile(`[^a-z0-9]+`)
	seen := map[string]bool{}
	for _, tok := range re.Split(phrase, -1) {
		if len(tok) < 2 {
			continue
		}
		if stopwords[tok] {
			continue
		}
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "what": true,
	"how": true, "why": true, "when": true, "where": true, "who": true,
	"that": true, "this": true, "from": true, "have": true, "was": true,
	"were": true, "are": true, "you": true, "your": true, "about": true,
	"into": true, "will": true, "would": true, "can": true, "could": true,
	"did": true, "does": true, "not": true, "has": true, "been": true,
}

// Forget deletes a memory by ID.
func (ms *MemoryStore) Forget(memoryID string) error {
	result, err := ms.db.Exec(
		"DELETE FROM PICO_MEMORIES WHERE memory_id = :1 AND agent_id = :2",
		memoryID, ms.agentID,
	)
	if err != nil {
		return fmt.Errorf("forget failed: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("memory %s not found", memoryID)
	}
	return nil
}

// float32SliceToString converts a float32 slice to Oracle VECTOR string format.
// Example output: "[0.123,0.456,-0.789]"
func float32SliceToString(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.Grow(len(v)*8 + 2) // pre-allocate roughly
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		// strconv.FormatFloat avoids the fmt reflection cost on the hot
		// embedding-write path; 'g'/-1/32 matches the prior %g formatting.
		sb.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}

// updateAccessTimestamps updates access_count and accessed_at for recalled memories
// using a single batch UPDATE to avoid N+1 query overhead.
func (ms *MemoryStore) updateAccessTimestamps(memoryIDs []string) {
	if len(memoryIDs) == 0 {
		return
	}
	placeholders := make([]string, len(memoryIDs))
	args := make([]interface{}, len(memoryIDs))
	for i, id := range memoryIDs {
		placeholders[i] = fmt.Sprintf(":%d", i+1)
		args[i] = id
	}
	query := fmt.Sprintf(
		"UPDATE PICO_MEMORIES SET accessed_at = CURRENT_TIMESTAMP, access_count = access_count + 1 WHERE memory_id IN (%s)",
		strings.Join(placeholders, ", "),
	)
	if _, err := ms.db.Exec(query, args...); err != nil {
		logger.WarnCF("oracle", "Failed to update access timestamps", map[string]interface{}{"error": err.Error()})
	}
}

// deduplicateMemory checks for near-duplicate memories before inserting.
// Returns (existingID, true) if a duplicate was found and updated, or ("", false) if no duplicate.
func (ms *MemoryStore) deduplicateMemory(text string, importance float64) (string, bool) {
	// Try exact text match first (cheap)
	var existingID string
	var existingImportance float64
	err := ms.db.QueryRow(
		"SELECT memory_id, importance FROM PICO_MEMORIES WHERE agent_id = :1 AND content = :2 FETCH FIRST 1 ROW ONLY",
		ms.agentID, text,
	).Scan(&existingID, &existingImportance)
	if err == nil {
		// Exact match found - update importance if new one is higher
		if importance > existingImportance {
			if _, execErr := ms.db.Exec("UPDATE PICO_MEMORIES SET importance = :1, accessed_at = CURRENT_TIMESTAMP WHERE memory_id = :2",
				importance, existingID); execErr != nil {
				logger.WarnCF("oracle", "Failed to update importance for duplicate memory", map[string]interface{}{
					"memory_id": existingID, "error": execErr.Error()})
			}
		}
		logger.InfoCF("oracle", "Duplicate memory detected, reusing existing", map[string]interface{}{
			"memory_id": existingID,
		})
		return existingID, true
	}

	// Try semantic deduplication via vector similarity if ONNX is available
	if ms.modelName != "" && ms.embedding != nil && ms.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT memory_id, importance,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS distance
			FROM PICO_MEMORIES
			WHERE agent_id = :2 AND embedding IS NOT NULL
			ORDER BY distance ASC
			FETCH FIRST 1 ROW ONLY`, ms.modelName)
		var distance float64
		err := ms.db.QueryRow(sqlQuery, text, ms.agentID).Scan(&existingID, &existingImportance, &distance)
		if err == nil && distance < dedupMaxDistance { // 95%+ similarity
			if importance > existingImportance {
				if _, execErr := ms.db.Exec("UPDATE PICO_MEMORIES SET importance = :1, accessed_at = CURRENT_TIMESTAMP WHERE memory_id = :2",
					importance, existingID); execErr != nil {
					logger.WarnCF("oracle", "Failed to update importance for near-duplicate memory", map[string]interface{}{
						"memory_id": existingID, "error": execErr.Error()})
				}
			}
			logger.InfoCF("oracle", "Near-duplicate memory detected via embedding similarity", map[string]interface{}{
				"memory_id": existingID,
				"distance":  distance,
			})
			return existingID, true
		}
	}

	return "", false
}
