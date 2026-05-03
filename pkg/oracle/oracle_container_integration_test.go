//go:build oracle_integration

package oracle

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasperan/picooraclaw/pkg/config"
)

var (
	oracleIntegrationOnce sync.Once
	oracleIntegrationDB   *sql.DB
	oracleIntegrationErr  error
	oracleSchemaOnce      sync.Once
	oracleSchemaErr       error
)

func realOracleDB(t *testing.T) *sql.DB {
	t.Helper()

	oracleIntegrationOnce.Do(func() {
		cfg := &config.OracleDBConfig{
			Mode:        "freepdb",
			Host:        getenvDefault("PICO_ORACLE_HOST", "127.0.0.1"),
			Port:        getenvIntDefault("PICO_ORACLE_PORT", 1521),
			Service:     getenvDefault("PICO_ORACLE_SERVICE", "FREEPDB1"),
			User:        getenvDefault("PICO_ORACLE_USER", "picooraclaw"),
			Password:    getenvDefault("PICO_ORACLE_PASSWORD", "OraclePass123"),
			PoolMaxOpen: 4,
			PoolMaxIdle: 2,
		}

		deadline := time.Now().Add(3 * time.Minute)
		for {
			cm, err := NewConnectionManager(cfg)
			if err == nil {
				oracleIntegrationDB = cm.DB()
				oracleIntegrationErr = nil
				return
			}
			oracleIntegrationErr = err
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(5 * time.Second)
		}
	})

	if oracleIntegrationErr != nil {
		t.Fatalf("connect to Oracle integration database: %v", oracleIntegrationErr)
	}
	return oracleIntegrationDB
}

func ensureRealOracleSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	oracleSchemaOnce.Do(func() {
		oracleSchemaErr = InitSchema(db)
	})
	if oracleSchemaErr != nil {
		t.Fatalf("InitSchema against Oracle integration database: %v", oracleSchemaErr)
	}
}

func uniqueIntegrationAgentID(t *testing.T) string {
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	if len(name) > 24 {
		name = name[len(name)-24:]
	}
	return fmt.Sprintf("it_%s_%d", name, time.Now().UnixNano()%1_000_000_000)
}

func cleanupIntegrationAgent(t *testing.T, db *sql.DB, agentID string) {
	t.Helper()
	t.Cleanup(func() {
		statements := []string{
			"DELETE FROM PICO_CONFIG WHERE agent_id = :1",
			"DELETE FROM PICO_DAILY_NOTES WHERE agent_id = :1",
			"DELETE FROM PICO_MEMORIES WHERE agent_id = :1",
			"DELETE FROM PICO_PROMPTS WHERE agent_id = :1",
			"DELETE FROM PICO_SESSIONS WHERE agent_id = :1",
			"DELETE FROM PICO_STATE WHERE agent_id = :1",
			"DELETE FROM PICO_TRANSCRIPTS WHERE agent_id = :1",
		}
		for _, statement := range statements {
			_, _ = db.Exec(statement, agentID)
		}
	})
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvIntDefault(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func TestOracleContainerIntegration_ConfigStore(t *testing.T) {
	db := realOracleDB(t)
	ensureRealOracleSchema(t, db)

	agentID := uniqueIntegrationAgentID(t)
	cleanupIntegrationAgent(t, db, agentID)

	store := NewConfigStore(db, agentID)
	if err := store.SetConfigValue("theme", "dark"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	value, err := store.GetConfigValue("theme")
	if err != nil {
		t.Fatalf("GetConfigValue: %v", err)
	}
	if value != "dark" {
		t.Fatalf("GetConfigValue = %q, want dark", value)
	}

	const configJSON = `{"providers":{"openai":{"api_base":"http://localhost:11434/v1"}}}`
	if err := store.SaveConfig(configJSON); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded != configJSON {
		t.Fatalf("LoadConfig = %q, want %q", loaded, configJSON)
	}
}

func TestOracleContainerIntegration_StateAndSessionStores(t *testing.T) {
	db := realOracleDB(t)
	ensureRealOracleSchema(t, db)

	agentID := uniqueIntegrationAgentID(t)
	cleanupIntegrationAgent(t, db, agentID)

	stateStore := NewStateStore(db, agentID)
	if err := stateStore.SetLastChannel("telegram"); err != nil {
		t.Fatalf("SetLastChannel: %v", err)
	}
	if err := stateStore.SetLastChatID("chat-123"); err != nil {
		t.Fatalf("SetLastChatID: %v", err)
	}

	sessionStore := NewSessionStore(db, agentID)
	sessionStore.AddMessage("telegram:chat-123", "user", "hello")
	sessionStore.AddMessage("telegram:chat-123", "assistant", "hi")
	sessionStore.SetSummary("telegram:chat-123", "short greeting")
	if err := sessionStore.Save("telegram:chat-123"); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	reloadedState := NewStateStore(db, agentID)
	if got := reloadedState.GetLastChannel(); got != "telegram" {
		t.Fatalf("GetLastChannel after reload = %q", got)
	}
	if got := reloadedState.GetLastChatID(); got != "chat-123" {
		t.Fatalf("GetLastChatID after reload = %q", got)
	}

	reloadedSessions := NewSessionStore(db, agentID)
	history := reloadedSessions.GetHistory("telegram:chat-123")
	if len(history) != 2 {
		t.Fatalf("reloaded history length = %d, want 2", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("first reloaded message = %+v", history[0])
	}
	if got := reloadedSessions.GetSummary("telegram:chat-123"); got != "short greeting" {
		t.Fatalf("reloaded summary = %q", got)
	}
}

func TestOracleContainerIntegration_MemoryStore(t *testing.T) {
	db := realOracleDB(t)
	ensureRealOracleSchema(t, db)

	agentID := uniqueIntegrationAgentID(t)
	cleanupIntegrationAgent(t, db, agentID)

	store := NewMemoryStore(db, agentID, nil)
	memoryID, err := store.Remember("User likes integration tests", 0.8, "preference")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if memoryID == "" {
		t.Fatal("Remember returned empty memory ID")
	}

	longTerm := store.ReadLongTerm()
	if !strings.Contains(longTerm, "User likes integration tests") {
		t.Fatalf("ReadLongTerm = %q", longTerm)
	}

	if err := store.AppendToday("first integration note"); err != nil {
		t.Fatalf("AppendToday first: %v", err)
	}
	if err := store.AppendToday("second integration note"); err != nil {
		t.Fatalf("AppendToday second: %v", err)
	}
	today := store.ReadToday()
	if !strings.Contains(today, "first integration note") || !strings.Contains(today, "second integration note") {
		t.Fatalf("ReadToday = %q", today)
	}

	if err := store.Forget(memoryID); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if err := store.Forget(memoryID); err == nil {
		t.Fatal("Forget should fail after memory has already been deleted")
	}
}
