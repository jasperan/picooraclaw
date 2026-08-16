package oracle

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Episode is one agent run: goal → tool trajectory → outcome (Spec 02).
type Episode struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	SessionKey string    `json:"session_key"`
	Goal       string    `json:"goal"`
	Trajectory string    `json:"trajectory"` // JSON array of tool events
	Outcome    string    `json:"outcome"`
	Status     string    `json:"status"` // success | failed | interrupted
	Importance float64   `json:"importance"`
	CreatedAt  time.Time `json:"created_at"`
	DurationMS int64     `json:"duration_ms"`
}

// EpisodeResult is a semantically-matched episode for replay.
type EpisodeResult struct {
	Episode
	Score float64 `json:"score"`
}

// EpisodeStore persists and searches agent runs.
type EpisodeStore struct {
	db        *sql.DB
	agentID   string
	modelName string
	embedding *EmbeddingService
}

// NewEpisodeStore creates an episode store.
func NewEpisodeStore(db *sql.DB, agentID string, embedding *EmbeddingService) *EpisodeStore {
	modelName := ""
	if embedding != nil {
		modelName = embedding.ModelName()
	}
	if modelName != "" {
		if err := validateSQLIdentifier(modelName); err != nil {
			modelName = ""
		}
	}
	return &EpisodeStore{db: db, agentID: agentID, modelName: modelName, embedding: embedding}
}

// Save inserts (or upserts) an episode with an embedding of its goal.
func (es *EpisodeStore) Save(ep *Episode) error {
	if es == nil || es.db == nil {
		return fmt.Errorf("episode store not available")
	}
	if ep.ID == "" {
		ep.ID = uuid.New().String()[:12]
	}
	if ep.AgentID == "" {
		ep.AgentID = es.agentID
	}
	if ep.Status == "" {
		ep.Status = "unknown"
	}
	if ep.Importance <= 0 {
		ep.Importance = 0.5
	}
	if len(ep.Goal) > 4000 {
		ep.Goal = ep.Goal[:4000]
	}
	if len(ep.Trajectory) > 20000 {
		ep.Trajectory = ep.Trajectory[:20000]
	}
	if len(ep.Outcome) > 4000 {
		ep.Outcome = ep.Outcome[:4000]
	}

	var err error
	if es.modelName != "" && es.embedding != nil && es.embedding.Mode() == "onnx" {
		_, err = es.db.Exec(fmt.Sprintf(`
			INSERT INTO PICO_EPISODES (episode_id, agent_id, session_key, goal, trajectory, outcome, status, importance, embedding, duration_ms)
			VALUES (:1, :2, :3, :4, :5, :6, :7, :8, VECTOR_EMBEDDING(%s USING :9 AS DATA), :10)`,
			es.modelName),
			ep.ID, ep.AgentID, ep.SessionKey, ep.Goal, ep.Trajectory, ep.Outcome, ep.Status, ep.Importance, ep.Goal, ep.DurationMS)
	} else if es.embedding != nil && es.embedding.Mode() == "api" {
		if vec, embErr := es.embedding.EmbedText(ep.Goal); embErr == nil {
			_, err = es.db.Exec(`
				INSERT INTO PICO_EPISODES (episode_id, agent_id, session_key, goal, trajectory, outcome, status, importance, embedding, duration_ms)
				VALUES (:1, :2, :3, :4, :5, :6, :7, :8, TO_VECTOR(:9), :10)`,
				ep.ID, ep.AgentID, ep.SessionKey, ep.Goal, ep.Trajectory, ep.Outcome, ep.Status, ep.Importance, float32SliceToString(vec), ep.DurationMS)
		} else {
			_, err = es.db.Exec(`
				INSERT INTO PICO_EPISODES (episode_id, agent_id, session_key, goal, trajectory, outcome, status, importance, duration_ms)
				VALUES (:1, :2, :3, :4, :5, :6, :7, :8, :9)`,
				ep.ID, ep.AgentID, ep.SessionKey, ep.Goal, ep.Trajectory, ep.Outcome, ep.Status, ep.Importance, ep.DurationMS)
		}
	} else {
		_, err = es.db.Exec(`
			INSERT INTO PICO_EPISODES (episode_id, agent_id, session_key, goal, trajectory, outcome, status, importance, duration_ms)
			VALUES (:1, :2, :3, :4, :5, :6, :7, :8, :9)`,
			ep.ID, ep.AgentID, ep.SessionKey, ep.Goal, ep.Trajectory, ep.Outcome, ep.Status, ep.Importance, ep.DurationMS)
	}
	if err != nil {
		return fmt.Errorf("episode save failed: %w", err)
	}
	return nil
}

// RecallEpisodes replays past runs semantically similar to the goal query.
func (es *EpisodeStore) RecallEpisodes(query string, maxResults int) ([]EpisodeResult, error) {
	if es == nil || es.db == nil {
		return nil, fmt.Errorf("episode store not available")
	}
	if es.embedding == nil {
		return nil, fmt.Errorf("embedding service not available")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	var rows *sql.Rows
	var err error
	if es.modelName != "" && es.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT episode_id, session_key, goal, trajectory, outcome, status, importance, created_at, duration_ms,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS dist
			FROM PICO_EPISODES
			WHERE agent_id = :2 AND embedding IS NOT NULL AND status <> 'interrupted'
			ORDER BY dist ASC
			FETCH FIRST :3 ROWS ONLY`, es.modelName)
		rows, err = es.db.Query(sqlQuery, query, es.agentID, maxResults)
	} else {
		qvec, embErr := es.embedding.EmbedText(query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to embed query: %w", embErr)
		}
		rows, err = es.db.Query(`
			SELECT episode_id, session_key, goal, trajectory, outcome, status, importance, created_at, duration_ms,
			       VECTOR_DISTANCE(embedding, TO_VECTOR(:1), COSINE) AS dist
			FROM PICO_EPISODES
			WHERE agent_id = :2 AND embedding IS NOT NULL AND status <> 'interrupted'
			ORDER BY dist ASC
			FETCH FIRST :3 ROWS ONLY`,
			float32SliceToString(qvec), es.agentID, maxResults)
	}
	if err != nil {
		return nil, fmt.Errorf("episode recall failed: %w", err)
	}
	defer rows.Close()

	var results []EpisodeResult
	for rows.Next() {
		var r EpisodeResult
		var sessionKey sql.NullString
		var dist float64
		if err := rows.Scan(&r.ID, &sessionKey, &r.Goal, &r.Trajectory, &r.Outcome, &r.Status, &r.Importance, &r.CreatedAt, &r.DurationMS, &dist); err != nil {
			continue
		}
		if sessionKey.Valid {
			r.SessionKey = sessionKey.String
		}
		r.Score = 1.0 - dist
		if r.Score >= 0.3 {
			results = append(results, r)
		}
	}
	return results, nil
}

// ConsolidateCandidates returns successful episodes from the window, oldest
// first, for promotion into long-term pattern memories.
func (es *EpisodeStore) ConsolidateCandidates(daysOldMin, daysOldMax int) ([]Episode, error) {
	if es == nil || es.db == nil {
		return nil, nil
	}
	rows, err := es.db.Query(`
		SELECT episode_id, session_key, goal, trajectory, outcome, status, importance, created_at, duration_ms
		FROM PICO_EPISODES
		WHERE agent_id = :1 AND status = 'success' AND embedding IS NOT NULL
		  AND created_at < SYSDATE - :2 AND created_at >= SYSDATE - :3
		ORDER BY created_at ASC
		FETCH FIRST 200 ROWS ONLY`, es.agentID, daysOldMin, daysOldMax)
	if err != nil {
		return nil, fmt.Errorf("consolidate candidates failed: %w", err)
	}
	defer rows.Close()

	var out []Episode
	for rows.Next() {
		var e Episode
		var sessionKey sql.NullString
		if err := rows.Scan(&e.ID, &sessionKey, &e.Goal, &e.Trajectory, &e.Outcome, &e.Status, &e.Importance, &e.CreatedAt, &e.DurationMS); err != nil {
			continue
		}
		if sessionKey.Valid {
			e.SessionKey = sessionKey.String
		}
		out = append(out, e)
	}
	return out, nil
}

// MarkConsolidated records promoted episode IDs in the consolidation log to
// keep the operation idempotent across runs.
func (es *EpisodeStore) MarkConsolidated(runID string, promotedIDs []string, memoriesOut, pruned int) error {
	if es == nil || es.db == nil {
		return nil
	}
	logText := ""
	for _, id := range promotedIDs {
		logText += id + "\n"
	}
	_, err := es.db.Exec(`
		INSERT INTO PICO_CONSOLIDATION (run_id, agent_id, started_at, finished_at, episodes_in, memories_out, pruned, log)
		VALUES (:1, :2, SYSTIMESTAMP, SYSTIMESTAMP, :3, :4, :5, :6)`,
		runID, es.agentID, len(promotedIDs), memoriesOut, pruned, logText)
	if err != nil {
		return fmt.Errorf("consolidation log failed: %w", err)
	}
	return nil
}

// AlreadyConsolidated returns the set of episode IDs already promoted.
func (es *EpisodeStore) AlreadyConsolidated() map[string]bool {
	out := map[string]bool{}
	if es == nil || es.db == nil {
		return out
	}
	rows, err := es.db.Query(`
		SELECT log FROM PICO_CONSOLIDATION WHERE agent_id = :1 ORDER BY started_at DESC FETCH FIRST 20 ROWS ONLY`,
		es.agentID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var log sql.NullString
		if err := rows.Scan(&log); err != nil || !log.Valid {
			continue
		}
		for _, id := range splitLines(log.String) {
			if id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// Prune removes old/noisy episodes; returns the number deleted.
func (es *EpisodeStore) Prune(keepDays int) (int, error) {
	if es == nil || es.db == nil {
		return 0, nil
	}
	res, err := es.db.Exec(`
		DELETE FROM PICO_EPISODES
		WHERE agent_id = :1 AND (
			(created_at < SYSDATE - :2 AND importance < 0.5)
			OR (status = 'interrupted' AND created_at < SYSDATE - :3)
		)`, es.agentID, keepDays, 30)
	if err != nil {
		return 0, fmt.Errorf("episode prune failed: %w", err)
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// splitLines is a tiny scanner helper (avoids pulling in a full CSV parser).
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
