package oracle

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jasperan/picooraclaw/pkg/logger"
)

// SkillUsage is a single skill's tracked usage + embedding.
type SkillUsage struct {
	Name     string    `json:"name"`
	UseCount int       `json:"use_count"`
	LastUsed time.Time `json:"last_used"`
}

// SkillStore tracks skill usage and supports semantic skill search
// (Spec 08). Embeddings are in-database ONNX when available.
type SkillStore struct {
	db        *sql.DB
	agentID   string
	modelName string
	embedding *EmbeddingService
}

// NewSkillStore creates a skill usage/search store.
func NewSkillStore(db *sql.DB, agentID string, embedding *EmbeddingService) *SkillStore {
	modelName := ""
	if embedding != nil {
		modelName = embedding.ModelName()
	}
	if modelName != "" {
		if err := validateSQLIdentifier(modelName); err != nil {
			modelName = ""
		}
	}
	return &SkillStore{db: db, agentID: agentID, modelName: modelName, embedding: embedding}
}

// RecordUsage bumps a skill's counter (idempotent upsert).
func (ss *SkillStore) RecordUsage(name string) {
	if ss == nil || ss.db == nil {
		return
	}
	if _, err := ss.db.Exec(`
		MERGE INTO PICO_SKILL_USAGE t
		USING (SELECT :1 AS skill_name, :2 AS agent_id FROM DUAL) s
		ON (t.skill_name = s.skill_name AND t.agent_id = s.agent_id)
		WHEN MATCHED THEN UPDATE SET use_count = use_count + 1, last_used = CURRENT_TIMESTAMP
		WHEN NOT MATCHED THEN INSERT (skill_name, agent_id, use_count, last_used)
			VALUES (:1, :2, 1, CURRENT_TIMESTAMP)`,
		name, ss.agentID); err != nil {
		logger.WarnCF("oracle", "Failed to record skill usage", map[string]interface{}{"error": err.Error()})
	}
}

// UpsertSkill records usage and (re)embeds the skill description. The vector
// lives in PICO_SKILL_USAGE.embedding so semantic search needs no extra table.
func (ss *SkillStore) UpsertSkill(name, description string) error {
	if ss == nil || ss.db == nil {
		return fmt.Errorf("skill store not available")
	}
	text := name + ": " + description
	if len(text) > maxEmbeddingInputLen {
		text = text[:maxEmbeddingInputLen]
	}
	var err error
	if ss.modelName != "" && ss.embedding != nil && ss.embedding.Mode() == "onnx" {
		_, err = ss.db.Exec(fmt.Sprintf(`
			MERGE INTO PICO_SKILL_USAGE t
			USING (SELECT :1 AS skill_name, :2 AS agent_id FROM DUAL) s
			ON (t.skill_name = s.skill_name AND t.agent_id = s.agent_id)
			WHEN MATCHED THEN UPDATE SET use_count = use_count + 1, last_used = CURRENT_TIMESTAMP,
			                             embedding = VECTOR_EMBEDDING(%s USING :3 AS DATA)
			WHEN NOT MATCHED THEN INSERT (skill_name, agent_id, use_count, last_used, embedding)
				VALUES (:1, :2, 1, CURRENT_TIMESTAMP, VECTOR_EMBEDDING(%s USING :4 AS DATA))`,
			ss.modelName, ss.modelName), name, ss.agentID, text, text)
	} else {
		_, err = ss.db.Exec(`
			MERGE INTO PICO_SKILL_USAGE t
			USING (SELECT :1 AS skill_name, :2 AS agent_id FROM DUAL) s
			ON (t.skill_name = s.skill_name AND t.agent_id = s.agent_id)
			WHEN MATCHED THEN UPDATE SET use_count = use_count + 1, last_used = CURRENT_TIMESTAMP
			WHEN NOT MATCHED THEN INSERT (skill_name, agent_id, use_count, last_used)
				VALUES (:1, :2, 1, CURRENT_TIMESTAMP)`,
			name, ss.agentID)
	}
	if err != nil {
		return fmt.Errorf("skill upsert failed: %w", err)
	}
	return nil
}

// Usage returns the tracked usage map for the agent.
func (ss *SkillStore) Usage() ([]SkillUsage, error) {
	if ss == nil || ss.db == nil {
		return nil, fmt.Errorf("skill store not available")
	}
	rows, err := ss.db.Query(`
		SELECT skill_name, use_count, last_used FROM PICO_SKILL_USAGE
		WHERE agent_id = :1 ORDER BY use_count DESC`, ss.agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SkillUsage
	for rows.Next() {
		var u SkillUsage
		var last sql.NullTime
		if err := rows.Scan(&u.Name, &u.UseCount, &last); err != nil {
			continue
		}
		u.LastUsed = last.Time
		out = append(out, u)
	}
	return out, nil
}

// Search ranks skills by semantic similarity to the query (vector channel).
// Returns nil when embeddings are unavailable so callers can fall back to
// substring matching.
func (ss *SkillStore) Search(query string, limit int) ([]SkillUsage, error) {
	if ss == nil || ss.db == nil {
		return nil, nil
	}
	if ss.embedding == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	var rows *sql.Rows
	var err error
	if ss.modelName != "" && ss.embedding.Mode() == "onnx" {
		sqlQuery := fmt.Sprintf(`
			SELECT skill_name, use_count, last_used,
			       VECTOR_DISTANCE(embedding, VECTOR_EMBEDDING(%s USING :1 AS DATA), COSINE) AS dist
			FROM PICO_SKILL_USAGE
			WHERE agent_id = :2 AND embedding IS NOT NULL
			ORDER BY dist ASC FETCH FIRST :3 ROWS ONLY`, ss.modelName)
		rows, err = ss.db.Query(sqlQuery, query, ss.agentID, limit)
	} else {
		qvec, embErr := ss.embedding.EmbedText(query)
		if embErr != nil {
			return nil, nil
		}
		rows, err = ss.db.Query(`
			SELECT skill_name, use_count, last_used,
			       VECTOR_DISTANCE(embedding, TO_VECTOR(:1), COSINE) AS dist
			FROM PICO_SKILL_USAGE
			WHERE agent_id = :2 AND embedding IS NOT NULL
			ORDER BY dist ASC FETCH FIRST :3 ROWS ONLY`,
			float32SliceToString(qvec), ss.agentID, limit)
	}
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	var out []SkillUsage
	for rows.Next() {
		var u SkillUsage
		var last sql.NullTime
		var dist float64
		if err := rows.Scan(&u.Name, &u.UseCount, &last, &dist); err != nil {
			continue
		}
		u.LastUsed = last.Time
		if 1.0-dist >= 0.25 {
			out = append(out, u)
		}
	}
	return out, nil
}
