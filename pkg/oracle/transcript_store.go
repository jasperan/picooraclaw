package oracle

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jasperan/picooraclaw/pkg/logger"
)

// TranscriptStore appends conversation rows to PICO_TRANSCRIPTS (the audit
// log) and makes them semantically searchable. Embeddings are computed
// in-database via VECTOR_EMBEDDING() when an ONNX model is loaded; otherwise
// rows are stored without vectors and backfilled by `picooraclaw reindex`.
type TranscriptStore struct {
	db        *sql.DB
	agentID   string
	modelName string
	embedding *EmbeddingService
}

// NewTranscriptStore creates a transcript audit store.
func NewTranscriptStore(db *sql.DB, agentID string, embedding *EmbeddingService) *TranscriptStore {
	modelName := ""
	if embedding != nil {
		modelName = embedding.ModelName()
	}
	if modelName != "" {
		if err := validateSQLIdentifier(modelName); err != nil {
			logger.WarnCF("oracle", "Invalid model name for TranscriptStore, disabling inline embedding", map[string]interface{}{"error": err.Error()})
			modelName = ""
		}
	}
	return &TranscriptStore{
		db:        db,
		agentID:   agentID,
		modelName: modelName,
		embedding: embedding,
	}
}

// Append records one transcript row (best-effort, never fails the caller).
// sequence_num is left NULL for live rows; ordering is by identity id.
func (ts *TranscriptStore) Append(sessionKey, role, content string) {
	if ts == nil || ts.db == nil {
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}

	var err error
	if ts.modelName != "" && ts.embedding != nil && ts.embedding.Mode() == "onnx" {
		_, err = ts.db.Exec(fmt.Sprintf(`
			INSERT INTO PICO_TRANSCRIPTS (session_key, agent_id, role, content, embedding)
			VALUES (:1, :2, :3, :4, VECTOR_EMBEDDING(%s USING :5 AS DATA))`,
			ts.modelName),
			sessionKey, ts.agentID, role, content, content)
	} else {
		_, err = ts.db.Exec(`
			INSERT INTO PICO_TRANSCRIPTS (session_key, agent_id, role, content)
			VALUES (:1, :2, :3, :4)`,
			sessionKey, ts.agentID, role, content)
	}
	if err != nil {
		logger.WarnCF("oracle", "Failed to append transcript", map[string]interface{}{"error": err.Error()})
	}
}

// TranscriptRecallResult is a single semantically-matched transcript row.
type TranscriptRecallResult struct {
	ID         int64     `json:"id"`
	SessionKey string    `json:"session_key"`
	Role       string    `json:"role"`
	Text       string    `json:"text"`
	Score      float64   `json:"score"`
	CreatedAt  time.Time `json:"created_at"`
}

// RecallTranscripts performs semantic search over transcript rows.
func (ts *TranscriptStore) RecallTranscripts(query string, maxResults int) ([]TranscriptRecallResult, error) {
	if ts == nil || ts.db == nil {
		return nil, fmt.Errorf("transcript store not available")
	}
	if ts.embedding == nil {
		return nil, fmt.Errorf("embedding service not available")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	var rows *sql.Rows
	var err error
	if ts.modelName != "" && ts.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT id, session_key, role, content, created_at,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS dist
			FROM PICO_TRANSCRIPTS
			WHERE agent_id = :2 AND embedding IS NOT NULL AND content IS NOT NULL
			ORDER BY dist ASC
			FETCH FIRST :3 ROWS ONLY`, ts.modelName)
		rows, err = ts.db.Query(sqlQuery, query, ts.agentID, maxResults)
	} else {
		queryVec, embErr := ts.embedding.EmbedText(query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to embed query: %w", embErr)
		}
		rows, err = ts.db.Query(`
			SELECT id, session_key, role, content, created_at,
			       VECTOR_DISTANCE(embedding, TO_VECTOR(:1), COSINE) AS dist
			FROM PICO_TRANSCRIPTS
			WHERE agent_id = :2 AND embedding IS NOT NULL AND content IS NOT NULL
			ORDER BY dist ASC
			FETCH FIRST :3 ROWS ONLY`,
			float32SliceToString(queryVec), ts.agentID, maxResults)
	}
	if err != nil {
		return nil, fmt.Errorf("transcript recall query failed: %w", err)
	}
	defer rows.Close()

	var results []TranscriptRecallResult
	for rows.Next() {
		var r TranscriptRecallResult
		var content sql.NullString
		var sessionKey sql.NullString
		var dist float64
		if err := rows.Scan(&r.ID, &sessionKey, &r.Role, &content, &r.CreatedAt, &dist); err != nil {
			continue
		}
		if sessionKey.Valid {
			r.SessionKey = sessionKey.String
		}
		if content.Valid {
			r.Text = content.String
		}
		r.Score = 1.0 - dist
		if r.Score >= 0.25 { // transcripts are noisy; keep a firm floor
			results = append(results, r)
		}
	}
	return results, nil
}

// RecentConversations returns the last N user/assistant exchanges per session,
// used for digests and proactive briefs (Spec 04/06).
func (ts *TranscriptStore) RecentConversations(days int, limit int) ([]TranscriptRecallResult, error) {
	if ts == nil || ts.db == nil {
		return nil, nil
	}
	rows, err := ts.db.Query(`
		SELECT id, session_key, role, content, created_at
		FROM PICO_TRANSCRIPTS
		WHERE agent_id = :1 AND content IS NOT NULL AND created_at >= SYSDATE - :2
		ORDER BY id DESC
		FETCH FIRST :3 ROWS ONLY`, ts.agentID, days, limit)
	if err != nil {
		return nil, fmt.Errorf("recent conversations failed: %w", err)
	}
	defer rows.Close()

	var results []TranscriptRecallResult
	for rows.Next() {
		var r TranscriptRecallResult
		var content, sessionKey sql.NullString
		if err := rows.Scan(&r.ID, &sessionKey, &r.Role, &content, &r.CreatedAt); err != nil {
			continue
		}
		if sessionKey.Valid {
			r.SessionKey = sessionKey.String
		}
		if content.Valid {
			r.Text = content.String
		}
		results = append(results, r)
	}
	return results, nil
}

// OpenThreads returns sessions with user messages but no assistant reply in
// the last `hours`, for proactive follow-up (Spec 06).
func (ts *TranscriptStore) OpenThreads(hours int) ([]TranscriptRecallResult, error) {
	if ts == nil || ts.db == nil {
		return nil, nil
	}
	rows, err := ts.db.Query(`
		SELECT t.id, t.session_key, t.role, t.content, t.created_at
		FROM PICO_TRANSCRIPTS t
		WHERE t.agent_id = :1
		  AND t.session_key IN (
			SELECT session_key FROM PICO_TRANSCRIPTS
			WHERE agent_id = :1 AND role = 'user' AND created_at >= SYSDATE - :2/24
			MINUS
			SELECT session_key FROM PICO_TRANSCRIPTS
			WHERE agent_id = :1 AND role = 'assistant' AND created_at > SYSDATE - :2/24
		  )
		  AND t.role = 'user'
		ORDER BY t.id DESC
		FETCH FIRST 20 ROWS ONLY`, ts.agentID, hours)
	if err != nil {
		return nil, fmt.Errorf("open threads failed: %w", err)
	}
	defer rows.Close()

	var results []TranscriptRecallResult
	for rows.Next() {
		var r TranscriptRecallResult
		var content, sessionKey sql.NullString
		if err := rows.Scan(&r.ID, &sessionKey, &r.Role, &content, &r.CreatedAt); err != nil {
			continue
		}
		if sessionKey.Valid {
			r.SessionKey = sessionKey.String
		}
		if content.Valid {
			r.Text = content.String
		}
		results = append(results, r)
	}
	return results, nil
}

// LatestAssistantReplies returns the most recent assistant messages (across
// sessions), used for EOD summaries (Spec 06).
func (ts *TranscriptStore) LatestAssistantReplies(days, limit int) ([]TranscriptRecallResult, error) {
	if ts == nil || ts.db == nil {
		return nil, nil
	}
	rows, err := ts.db.Query(`
		SELECT id, session_key, role, content, created_at
		FROM PICO_TRANSCRIPTS
		WHERE agent_id = :1 AND role = 'assistant' AND content IS NOT NULL
		  AND created_at >= SYSDATE - :2
		ORDER BY id DESC
		FETCH FIRST :3 ROWS ONLY`, ts.agentID, days, limit)
	if err != nil {
		return nil, fmt.Errorf("latest replies failed: %w", err)
	}
	defer rows.Close()

	var results []TranscriptRecallResult
	for rows.Next() {
		var r TranscriptRecallResult
		var content, sessionKey sql.NullString
		if err := rows.Scan(&r.ID, &sessionKey, &r.Role, &content, &r.CreatedAt); err != nil {
			continue
		}
		if sessionKey.Valid {
			r.SessionKey = sessionKey.String
		}
		if content.Valid {
			r.Text = content.String
		}
		results = append(results, r)
	}
	return results, nil
}
