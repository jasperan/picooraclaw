package oracle

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/jasperan/picooraclaw/pkg/logger"
)

// Migration is a single ordered, idempotent schema step.
// Apply must tolerate being run against a database where the step has
// already partially or fully applied (additive-only policy).
type Migration struct {
	Version string // semver-style "1.1.0"
	Apply   func(db *sql.DB) error
}

// migrations is the ordered list of schema migrations applied after the base
// tables. Versions are strictly increasing. Never edit an applied step:
// append a new one instead.
var migrations = []Migration{
	{
		Version: "1.1.0",
		Apply: func(db *sql.DB) error {
			// Spec 01: transcripts become semantically searchable + memory
			// gains a lexical channel.
			// 1. Embedding columns on transcripts (ORA-01430 tolerated).
			_ = execTolerating(db, []string{"ORA-01430", "ORA-00955"},
				"ALTER TABLE PICO_TRANSCRIPTS ADD (embedding VECTOR, embedding_ts TIMESTAMP)")
			// 2. Vector index over transcript embeddings.
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				`CREATE VECTOR INDEX IDX_PICO_TRANSCRIPTS_VEC ON PICO_TRANSCRIPTS(embedding)
				 ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95`)
			// 3. Oracle Text CONTEXT index over memory content (best effort).
			lexMode := "instr" // INSTR-based lexical fallback, works everywhere
			if err := execTolerating(db, []string{"ORA-00955", "ORA-01408", "ORA-29855", "ORA-29830", "ORA-20000", "ORA-01031"},
				`CREATE INDEX IDX_PICO_MEMORIES_CTX ON PICO_MEMORIES(content) INDEXTYPE IS CTXSYS.CONTEXT`); err == nil {
				lexMode = "text"
			}
			_ = setMetaValue(db, "oracle.lexical_mode", lexMode)
			return nil
		},
	},
	{
		Version: "1.2.0",
		Apply: func(db *sql.DB) error {
			// Spec 02: episodic memory + consolidation log.
			_ = execTolerating(db, []string{"ORA-00955"},
				`CREATE TABLE PICO_EPISODES (
					episode_id   VARCHAR2(64) PRIMARY KEY,
					agent_id     VARCHAR2(64) NOT NULL,
					session_key  VARCHAR2(255),
					goal         CLOB,
					trajectory   CLOB,
					outcome      CLOB,
					status       VARCHAR2(16) DEFAULT 'unknown',
					embedding    VECTOR,
					importance   NUMBER(3,2) DEFAULT 0.5,
					created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
					duration_ms  NUMBER
				)`)
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				"CREATE INDEX IDX_PICO_EPISODES_AGENT ON PICO_EPISODES(agent_id, created_at)")
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				`CREATE VECTOR INDEX IDX_PICO_EPISODES_VEC ON PICO_EPISODES(embedding)
				 ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95`)
			_ = execTolerating(db, []string{"ORA-00955"},
				`CREATE TABLE PICO_CONSOLIDATION (
					run_id       VARCHAR2(64) PRIMARY KEY,
					agent_id     VARCHAR2(64) NOT NULL,
					started_at   TIMESTAMP,
					finished_at  TIMESTAMP,
					episodes_in  NUMBER DEFAULT 0,
					memories_out NUMBER DEFAULT 0,
					pruned       NUMBER DEFAULT 0,
					log          CLOB
				)`)
			return nil
		},
	},
	{
		Version: "1.3.0",
		Apply: func(db *sql.DB) error {
			// Spec 03: code knowledge graph (vertex/edge tables + PGQ).
			_ = execTolerating(db, []string{"ORA-00955"},
				`CREATE TABLE PICO_CODE_NODES (
					node_id     VARCHAR2(64) PRIMARY KEY,
					agent_id    VARCHAR2(64) NOT NULL,
					repo        VARCHAR2(512) NOT NULL,
					kind        VARCHAR2(32),
					name        VARCHAR2(512),
					path        VARCHAR2(1024),
					signature   VARCHAR2(1024),
					doc         CLOB,
					summary     CLOB,
					embedding   VECTOR,
					start_line  NUMBER,
					end_line    NUMBER,
					created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
				)`)
			_ = execTolerating(db, []string{"ORA-00955"},
				`CREATE TABLE PICO_CODE_EDGES (
					edge_id     VARCHAR2(64) PRIMARY KEY,
					agent_id    VARCHAR2(64) NOT NULL,
					repo        VARCHAR2(512) NOT NULL,
					src_node_id VARCHAR2(64) NOT NULL,
					dst_node_id VARCHAR2(64) NOT NULL,
					kind        VARCHAR2(32),
					weight      NUMBER DEFAULT 1
				)`)
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				"CREATE INDEX IDX_PICO_CODE_NODES_REPO ON PICO_CODE_NODES(agent_id, repo)")
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				"CREATE INDEX IDX_PICO_CODE_EDGES_SRC ON PICO_CODE_EDGES(agent_id, src_node_id)")
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				"CREATE INDEX IDX_PICO_CODE_EDGES_DST ON PICO_CODE_EDGES(agent_id, dst_node_id)")
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				`CREATE VECTOR INDEX IDX_PICO_CODE_NODES_VEC ON PICO_CODE_NODES(embedding)
				 ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95`)
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408", "ORA-01031", "ORA-00942", "ORA-42405"},
				`CREATE PROPERTY GRAPH PICO_CODE_GRAPH
				 VERTEX TABLES (PICO_CODE_NODES)
				 EDGE TABLES (PICO_CODE_EDGES AS EDGE KEY (edge_id)
				              SOURCE KEY (src_node_id) REFERENCES PICO_CODE_NODES (node_id)
				              DESTINATION KEY (dst_node_id) REFERENCES PICO_CODE_NODES (node_id)
				              PROPERTIES (kind, weight))`)
			// Spec 08: skill usage tracking.
			_ = execTolerating(db, []string{"ORA-00955"},
				`CREATE TABLE PICO_SKILL_USAGE (
					skill_name VARCHAR2(128) NOT NULL,
					agent_id   VARCHAR2(64) NOT NULL,
					use_count  NUMBER DEFAULT 0,
					last_used  TIMESTAMP,
					embedding  VECTOR,
					PRIMARY KEY (skill_name, agent_id)
				)`)
			_ = execTolerating(db, []string{"ORA-00955", "ORA-01408"},
				`CREATE VECTOR INDEX IDX_PICO_SKILL_USAGE_VEC ON PICO_SKILL_USAGE(embedding)
				 ORGANIZATION NEIGHBOR PARTITIONS DISTANCE COSINE WITH TARGET ACCURACY 95`)
			return nil
		},
	},
}

// applyMigrations applies every migration newer than the stored schema version,
// stamping each applied version into PICO_META. Additive and idempotent.
func applyMigrations(db *sql.DB) error {
	current := currentSchemaVersion(db)
	for _, m := range migrations {
		if compareVersions(m.Version, current) <= 0 {
			continue
		}
		if err := m.Apply(db); err != nil {
			_ = setMetaValue(db, "last_migration_error", fmt.Sprintf("%s: %v", m.Version, err))
			return fmt.Errorf("migration %s failed: %w", m.Version, err)
		}
		setSchemaVersion(db, m.Version)
		logger.InfoCF("oracle", "Applied migration", map[string]interface{}{"version": m.Version})
	}
	return nil
}

// currentSchemaVersion reads the schema_version from PICO_META.
// Missing/empty meta returns "0.0.0".
func currentSchemaVersion(db *sql.DB) string {
	var v sql.NullString
	err := db.QueryRow(
		"SELECT meta_value FROM PICO_META WHERE meta_key = 'schema_version'",
	).Scan(&v)
	if err != nil || !v.Valid || v.String == "" {
		return "0.0.0"
	}
	return v.String
}

// setMetaValue upserts an arbitrary PICO_META key/value. The key is an
// internal constant, embedded as a SQL literal exactly like setSchemaVersion
// (avoids go-ora positional-bind quirks with MERGE).
func setMetaValue(db *sql.DB, key, value string) error {
	if strings.ContainsAny(key, "'\"") {
		return fmt.Errorf("invalid meta key %q", key)
	}
	_, err := db.Exec(`
        MERGE INTO PICO_META m
        USING (SELECT '`+key+`' AS meta_key FROM DUAL) s
        ON (m.meta_key = s.meta_key)
        WHEN MATCHED THEN
            UPDATE SET meta_value = :1, updated_at = CURRENT_TIMESTAMP
        WHEN NOT MATCHED THEN
            INSERT (meta_key, meta_value) VALUES ('`+key+`', :2)
    `, value, value)
	if err != nil {
		logger.WarnCF("oracle", "Failed to set meta value", map[string]interface{}{"key": key, "error": err.Error()})
	}
	return err
}

// getMetaValue reads a PICO_META value, returning "" when absent.
func getMetaValue(db *sql.DB, key string) string {
	var v sql.NullString
	if err := db.QueryRow("SELECT meta_value FROM PICO_META WHERE meta_key = :1", key).Scan(&v); err != nil {
		return ""
	}
	if !v.Valid {
		return ""
	}
	return v.String
}

// execTolerating executes DDL and returns nil when the error is one of the
// tolerated ORA codes (already-exists style conditions).
func execTolerating(db *sql.DB, codes []string, ddl string) error {
	_, err := db.Exec(ddl)
	if err == nil {
		return nil
	}
	for _, code := range codes {
		if strings.Contains(err.Error(), code) {
			logger.DebugCF("oracle", "DDL tolerated", map[string]interface{}{"code": code, "ddl": ddl})
			return nil
		}
	}
	logger.WarnCF("oracle", "DDL failed", map[string]interface{}{"error": err.Error(), "ddl": ddl})
	return err
}

// compareVersions compares two dotted numeric versions. Returns -1, 0, or 1.
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err == nil {
			out[i] = n
		}
	}
	return out
}

// CreateDualityViews creates JSON-Relational Duality views over memories and
// sessions (Oracle 23ai+ only). Returns an error (or nil) when unsupported so
// setup can report the feature as skipped.
func CreateDualityViews(db *sql.DB) error {
	// Probe: ensure the base tables exist first (idempotent).
	if err := execTolerating(db, []string{"ORA-00955"}, `
		CREATE JSON RELATIONAL DUALITY VIEW PICO_MEMORIES_DOC AS
		PICO_MEMORIES @INSERT @UPDATE @DELETE
		{ _id : memory_id,
		  agent : agent_id,
		  content : content,
		  importance : importance,
		  category : category }`); err != nil {
		return err
	}
	if err := execTolerating(db, []string{"ORA-00955"}, `
		CREATE JSON RELATIONAL DUALITY VIEW PICO_SESSIONS_DOC AS
		PICO_SESSIONS @INSERT @UPDATE @DELETE
		{ _id : session_key,
		  agent : agent_id,
		  summary : summary }`); err != nil {
		return err
	}
	return nil
}
