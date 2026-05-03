package oracle

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jasperan/picooraclaw/pkg/config"
	"github.com/jasperan/picooraclaw/pkg/providers"
)

type vectorArgConverter struct{}

func (vectorArgConverter) ConvertValue(v interface{}) (driver.Value, error) {
	if vec, ok := v.([]float32); ok {
		return float32SliceToString(vec), nil
	}
	return driver.DefaultParameterConverter.ConvertValue(v)
}

func newAPIEmbeddingServiceForTest(t *testing.T, handler http.HandlerFunc) (*EmbeddingService, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	svc := NewAPIEmbeddingService(nil, server.URL, "test-key", "API_MODEL")
	svc.httpClient = server.Client()
	return svc, server.Close
}

func embeddingResponse(values []float32) map[string]interface{} {
	return map[string]interface{}{
		"data": []map[string]interface{}{
			{"embedding": values, "index": 0},
		},
		"model": "API_MODEL",
	}
}

func TestConnectionManager_Methods(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	cm := &ConnectionManager{db: db}
	if cm.DB() != db {
		t.Fatal("DB returned a different handle")
	}

	mock.ExpectPing()
	if err := cm.Ping(); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectCommit()
	if err := cm.WithTx(func(tx *sql.Tx) error { return nil }); err != nil {
		t.Fatalf("WithTx commit path failed: %v", err)
	}

	wantErr := errors.New("work failed")
	mock.ExpectBegin()
	mock.ExpectRollback()
	if err := cm.WithTx(func(tx *sql.Tx) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("WithTx returned %v, want %v", err, wantErr)
	}

	mock.ExpectClose()
	if err := cm.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestConnectionManager_WithTxErrorPaths(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		cm := &ConnectionManager{db: db}
		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err = cm.WithTx(func(tx *sql.Tx) error {
			t.Fatal("transaction function should not run")
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "failed to begin transaction") {
			t.Fatalf("WithTx begin error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("rollback error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		cm := &ConnectionManager{db: db}
		mock.ExpectBegin()
		mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

		err = cm.WithTx(func(tx *sql.Tx) error {
			return errors.New("work failed")
		})
		if err == nil || !strings.Contains(err.Error(), "rollback failed") {
			t.Fatalf("WithTx rollback error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})
}

func TestNewConnectionManager_PingFailure(t *testing.T) {
	cfg := &config.OracleDBConfig{
		Mode:        "adb",
		DSN:         "://invalid",
		PoolMaxOpen: 1,
		PoolMaxIdle: 1,
	}

	cm, err := NewConnectionManager(cfg)
	if err == nil {
		_ = cm.Close()
		t.Fatal("NewConnectionManager should fail when ping cannot connect")
	}
	if !strings.Contains(err.Error(), "Oracle ping failed") {
		t.Fatalf("NewConnectionManager error = %v", err)
	}
}

func TestEmbeddingService_APIEmbedTextBatchAndMetadata(t *testing.T) {
	var inputs []string
	svc, closeServer := newAPIEmbeddingServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization header = %q", got)
		}

		var req embeddingAPIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		inputs = append(inputs, req.Input)
		_ = json.NewEncoder(w).Encode(embeddingResponse([]float32{0.25, 0.5}))
	})
	defer closeServer()

	embedding, err := svc.EmbedText(strings.Repeat("x", 600))
	if err != nil {
		t.Fatalf("EmbedText failed: %v", err)
	}
	if len(embedding) != 2 {
		t.Fatalf("embedding length = %d, want 2", len(embedding))
	}
	if len(inputs) == 0 || len(inputs[0]) != 512 {
		t.Fatalf("first input length = %d, want 512", len(inputs[0]))
	}
	if svc.Dims() != 2 {
		t.Fatalf("Dims = %d, want 2", svc.Dims())
	}
	if svc.ModelName() != "API_MODEL" {
		t.Fatalf("ModelName = %q, want API_MODEL", svc.ModelName())
	}
	if svc.Mode() != "api" {
		t.Fatalf("Mode = %q, want api", svc.Mode())
	}

	batch, err := svc.EmbedTexts([]string{"one", "two"})
	if err != nil {
		t.Fatalf("EmbedTexts failed: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("EmbedTexts returned %d vectors, want 2", len(batch))
	}
	if !svc.TestEmbedding() {
		t.Fatal("TestEmbedding should succeed against the test API")
	}
}

func TestEmbeddingService_APIErrorResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "non-200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", http.StatusBadGateway)
			},
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{"))
			},
		},
		{
			name: "empty result",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, closeServer := newAPIEmbeddingServiceForTest(t, tt.handler)
			defer closeServer()

			if _, err := svc.EmbedText("hello"); err == nil {
				t.Fatal("EmbedText should fail")
			}
		})
	}
}

func TestEmbeddingService_ONNXAndAPIModes(t *testing.T) {
	t.Run("onnx query error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		svc, err := NewEmbeddingService(db, "TEST_MODEL")
		if err != nil {
			t.Fatalf("NewEmbeddingService failed: %v", err)
		}

		longText := strings.Repeat("z", 700)
		mock.ExpectQuery("SELECT VECTOR_EMBEDDING").
			WithArgs(strings.Repeat("z", 512)).
			WillReturnError(errors.New("vector failed"))

		if _, err := svc.EmbedText(longText); err == nil {
			t.Fatal("EmbedText should return ONNX query error")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("api skips onnx operations", func(t *testing.T) {
		svc := NewAPIEmbeddingService(nil, "http://127.0.0.1", "key", "API_MODEL")

		loaded, err := svc.CheckONNXLoaded()
		if err != nil {
			t.Fatalf("CheckONNXLoaded api mode failed: %v", err)
		}
		if !loaded {
			t.Fatal("CheckONNXLoaded should return true in api mode")
		}
		if err := svc.LoadONNXModel("BAD DIR", "bad/file.onnx"); err != nil {
			t.Fatalf("LoadONNXModel should be a no-op in api mode: %v", err)
		}
	})

	t.Run("invalid onnx model inputs", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		if _, err := NewEmbeddingService(db, "1_BAD"); err == nil {
			t.Fatal("NewEmbeddingService should reject invalid model names")
		}

		svc, err := NewEmbeddingService(db, "TEST_MODEL")
		if err != nil {
			t.Fatalf("NewEmbeddingService failed: %v", err)
		}
		if err := svc.LoadONNXModel("BAD-DIR", "model.onnx"); err == nil {
			t.Fatal("LoadONNXModel should reject invalid directory identifiers")
		}
		if err := svc.LoadONNXModel("ONNX_DIR", "bad/file.onnx"); err == nil {
			t.Fatal("LoadONNXModel should reject invalid file names")
		}
	})
}

func TestMemoryStore_AppendTodayInsertAndUpdate(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	store := NewMemoryStore(db, "agent-1", nil)

	mock.ExpectQuery("SELECT content FROM PICO_DAILY_NOTES").
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}))
	mock.ExpectExec("INSERT INTO PICO_DAILY_NOTES").
		WithArgs(sqlmock.AnyArg(), "agent-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.AppendToday("first note"); err != nil {
		t.Fatalf("AppendToday insert failed: %v", err)
	}

	mock.ExpectQuery("SELECT content FROM PICO_DAILY_NOTES").
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("existing note"))
	mock.ExpectExec("UPDATE PICO_DAILY_NOTES").
		WithArgs("existing note\nsecond note", "agent-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.AppendToday("second note"); err != nil {
		t.Fatalf("AppendToday update failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestMemoryStore_AppendTodayWithONNXEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	embedding, err := NewEmbeddingService(db, "TEST_MODEL")
	if err != nil {
		t.Fatalf("NewEmbeddingService failed: %v", err)
	}
	store := NewMemoryStore(db, "agent-1", embedding)

	mock.ExpectQuery("SELECT content FROM PICO_DAILY_NOTES").
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}))
	mock.ExpectExec("VECTOR_EMBEDDING").
		WithArgs(sqlmock.AnyArg(), "agent-1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.AppendToday("with embedding"); err != nil {
		t.Fatalf("AppendToday ONNX insert failed: %v", err)
	}

	mock.ExpectQuery("SELECT content FROM PICO_DAILY_NOTES").
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow("existing"))
	mock.ExpectExec("VECTOR_EMBEDDING").
		WithArgs("existing\nagain", "existing\nagain", "agent-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.AppendToday("again"); err != nil {
		t.Fatalf("AppendToday ONNX update failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestMemoryStore_DeduplicateExactMemory(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	store := NewMemoryStore(db, "agent-1", nil)
	mock.ExpectQuery("SELECT memory_id, importance FROM PICO_MEMORIES").
		WithArgs("agent-1", "same memory").
		WillReturnRows(sqlmock.NewRows([]string{"memory_id", "importance"}).AddRow("mem-1", 0.2))
	mock.ExpectExec("UPDATE PICO_MEMORIES SET importance").
		WithArgs(0.9, "mem-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := store.Remember("same memory", 0.9, "note")
	if err != nil {
		t.Fatalf("Remember duplicate failed: %v", err)
	}
	if id != "mem-1" {
		t.Fatalf("Remember returned %q, want mem-1", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestMemoryStore_RememberAPIEmbeddingPaths(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	embedding, closeServer := newAPIEmbeddingServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
		var req embeddingAPIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Input == "fallback memory" {
			http.Error(w, "embedding failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(embeddingResponse([]float32{0.1, 0.2}))
	})
	defer closeServer()

	store := NewMemoryStore(db, "agent-1", embedding)

	mock.ExpectQuery("SELECT memory_id, importance FROM PICO_MEMORIES").
		WithArgs("agent-1", "vector memory").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO PICO_MEMORIES").
		WithArgs(sqlmock.AnyArg(), "agent-1", "vector memory", "[0.1,0.2]", 0.7, "note").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if id, err := store.Remember("vector memory", 0.7, "note"); err != nil || id == "" {
		t.Fatalf("Remember API success id=%q err=%v", id, err)
	}

	mock.ExpectQuery("SELECT memory_id, importance FROM PICO_MEMORIES").
		WithArgs("agent-1", "fallback memory").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO PICO_MEMORIES").
		WithArgs(sqlmock.AnyArg(), "agent-1", "fallback memory", 0.4, "note").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if id, err := store.Remember("fallback memory", 0.4, "note"); err != nil || id == "" {
		t.Fatalf("Remember API fallback id=%q err=%v", id, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestMemoryStore_RememberONNXPaths(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	embedding, err := NewEmbeddingService(db, "TEST_MODEL")
	if err != nil {
		t.Fatalf("NewEmbeddingService failed: %v", err)
	}
	store := NewMemoryStore(db, "agent-1", embedding)

	mock.ExpectQuery("SELECT memory_id, importance FROM PICO_MEMORIES").
		WithArgs("agent-1", "near duplicate").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("VECTOR_DISTANCE").
		WithArgs("near duplicate", "agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"memory_id", "importance", "distance"}).AddRow("mem-near", 0.3, 0.01))
	mock.ExpectExec("UPDATE PICO_MEMORIES SET importance").
		WithArgs(0.8, "mem-near").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, err := store.Remember("near duplicate", 0.8, "note")
	if err != nil {
		t.Fatalf("Remember near duplicate failed: %v", err)
	}
	if id != "mem-near" {
		t.Fatalf("Remember returned %q, want mem-near", id)
	}

	mock.ExpectQuery("SELECT memory_id, importance FROM PICO_MEMORIES").
		WithArgs("agent-1", "new onnx memory").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("VECTOR_DISTANCE").
		WithArgs("new onnx memory", "agent-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO PICO_MEMORIES").
		WithArgs(sqlmock.AnyArg(), "agent-1", "new onnx memory", "new onnx memory", 0.5, "note").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if id, err := store.Remember("new onnx memory", 0.5, "note"); err != nil || id == "" {
		t.Fatalf("Remember ONNX insert id=%q err=%v", id, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestMemoryStore_RecallAPIAndONNX(t *testing.T) {
	t.Run("api recall filters and updates access", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		embedding, closeServer := newAPIEmbeddingServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(embeddingResponse([]float32{0.2, 0.4}))
		})
		defer closeServer()

		store := NewMemoryStore(db, "agent-1", embedding)
		rows := sqlmock.NewRows([]string{"memory_id", "content", "importance", "category", "distance"}).
			AddRow("mem-1", "keep this", 0.9, "note", 0.1).
			AddRow("mem-2", "drop this", 0.1, "note", 0.9)
		mock.ExpectQuery("SELECT memory_id, content, importance, category").
			WithArgs("[0.2,0.4]", "agent-1", 3).
			WillReturnRows(rows)
		mock.ExpectExec("UPDATE PICO_MEMORIES SET accessed_at").
			WithArgs("mem-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		results, err := store.Recall("find it", 3)
		if err != nil {
			t.Fatalf("Recall API failed: %v", err)
		}
		if len(results) != 1 || results[0].MemoryID != "mem-1" || results[0].Score < 0.89 {
			t.Fatalf("Recall results = %+v", results)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("onnx recall", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		embedding, err := NewEmbeddingService(db, "TEST_MODEL")
		if err != nil {
			t.Fatalf("NewEmbeddingService failed: %v", err)
		}
		store := NewMemoryStore(db, "agent-1", embedding)

		rows := sqlmock.NewRows([]string{"memory_id", "content", "importance", "category", "distance"}).
			AddRow("mem-onnx", "onnx result", 0.6, "note", 0.2)
		mock.ExpectQuery("VECTOR_EMBEDDING").
			WithArgs("find it", "agent-1", 2).
			WillReturnRows(rows)
		mock.ExpectExec("UPDATE PICO_MEMORIES SET accessed_at").
			WithArgs("mem-onnx").
			WillReturnResult(sqlmock.NewResult(0, 1))

		results, err := store.Recall("find it", 2)
		if err != nil {
			t.Fatalf("Recall ONNX failed: %v", err)
		}
		if len(results) != 1 || results[0].MemoryID != "mem-onnx" {
			t.Fatalf("Recall results = %+v", results)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})
}

func TestMemoryStore_RecallErrorPathsAndHelpers(t *testing.T) {
	t.Run("api embedding error", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		embedding, closeServer := newAPIEmbeddingServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no embedding", http.StatusInternalServerError)
		})
		defer closeServer()

		store := NewMemoryStore(db, "agent-1", embedding)
		if _, err := store.Recall("find it", 3); err == nil {
			t.Fatal("Recall should fail when query embedding fails")
		}
	})

	t.Run("query error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		embedding, err := NewEmbeddingService(db, "TEST_MODEL")
		if err != nil {
			t.Fatalf("NewEmbeddingService failed: %v", err)
		}
		store := NewMemoryStore(db, "agent-1", embedding)
		mock.ExpectQuery("VECTOR_EMBEDDING").
			WithArgs("find it", "agent-1", 3).
			WillReturnError(errors.New("query failed"))

		if _, err := store.Recall("find it", 3); err == nil {
			t.Fatal("Recall should fail on query errors")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("update access empty and error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		store := NewMemoryStore(db, "agent-1", nil)
		store.updateAccessTimestamps(nil)
		mock.ExpectExec("UPDATE PICO_MEMORIES SET accessed_at").
			WithArgs("mem-1", "mem-2").
			WillReturnError(errors.New("update failed"))
		store.updateAccessTimestamps([]string{"mem-1", "mem-2"})
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	if got := float32SliceToString(nil); got != "[]" {
		t.Fatalf("float32SliceToString(nil) = %q", got)
	}
	if got := float32SliceToString([]float32{1, -2.5}); got != "[1,-2.5]" {
		t.Fatalf("float32SliceToString = %q", got)
	}
}

func TestVectorSearch(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.ValueConverterOption(vectorArgConverter{}))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"memory_id", "content", "distance"}).
		AddRow("mem-1", "first", 0.2).
		AddRow("mem-2", nil, 0.9)
	mock.ExpectQuery("SELECT memory_id, content").
		WithArgs("[0.1,0.2]", "agent-1", 5).
		WillReturnRows(rows)

	results, err := VectorSearch(db, "PICO_MEMORIES", "memory_id", "content", "embedding", "agent-1", []float32{0.1, 0.2}, 5, 0.3)
	if err != nil {
		t.Fatalf("VectorSearch failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "mem-1" || results[0].Text != "first" || results[0].Score < 0.79 {
		t.Fatalf("VectorSearch results = %+v", results)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestVectorSearchErrorPaths(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.ValueConverterOption(vectorArgConverter{}))
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	if _, err := VectorSearch(db, "PICO_MEMORIES;DROP", "memory_id", "content", "embedding", "agent-1", []float32{0.1}, 5, 0); err == nil {
		t.Fatal("VectorSearch should reject invalid identifiers")
	}

	mock.ExpectQuery("SELECT memory_id, content").
		WillReturnError(errors.New("query failed"))
	if _, err := VectorSearch(db, "PICO_MEMORIES", "memory_id", "content", "embedding", "agent-1", []float32{0.1}, 5, 0); err == nil {
		t.Fatal("VectorSearch should return query errors")
	}

	mock.ExpectQuery("SELECT memory_id, content").
		WillReturnRows(sqlmock.NewRows([]string{"memory_id", "content", "distance"}).AddRow("mem-1", "text", "bad-distance"))
	if _, err := VectorSearch(db, "PICO_MEMORIES", "memory_id", "content", "embedding", "agent-1", []float32{0.1}, 5, 0); err == nil {
		t.Fatal("VectorSearch should return scan errors")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestSessionStore_SetHistoryAndLoadAllBranches(t *testing.T) {
	t.Run("set history copies and truncate clears", func(t *testing.T) {
		store, mock := newMockSessionStore(t)

		store.GetOrCreate("sess-1")
		history := []providers.Message{{Role: "user", Content: "original"}}
		store.SetHistory("sess-1", history)
		history[0].Content = "mutated"

		got := store.GetHistory("sess-1")
		if len(got) != 1 || got[0].Content != "original" {
			t.Fatalf("SetHistory did not copy messages: %+v", got)
		}

		store.SetHistory("missing", []providers.Message{{Role: "user", Content: "ignored"}})
		store.TruncateHistory("sess-1", 0)
		if got := store.GetHistory("sess-1"); len(got) != 0 {
			t.Fatalf("TruncateHistory keepLast=0 left %d messages", len(got))
		}
		store.TruncateHistory("missing", 1)

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("load all handles valid invalid and empty rows", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		now := time.Now()
		rows := sqlmock.NewRows([]string{"session_key", "messages", "summary", "created_at", "updated_at"}).
			AddRow("valid", `[{"role":"user","content":"hello"}]`, "summary", now, now).
			AddRow("invalid", `{`, nil, now, now).
			AddRow("empty", nil, nil, now, now)
		mock.ExpectQuery("SELECT session_key, messages, summary, created_at, updated_at FROM PICO_SESSIONS").
			WithArgs("agent-1").
			WillReturnRows(rows)

		store := NewSessionStore(db, "agent-1")
		if got := store.GetHistory("valid"); len(got) != 1 || got[0].Content != "hello" {
			t.Fatalf("valid session history = %+v", got)
		}
		if got := store.GetSummary("valid"); got != "summary" {
			t.Fatalf("valid session summary = %q", got)
		}
		if got := store.GetHistory("invalid"); len(got) != 0 {
			t.Fatalf("invalid session history length = %d", len(got))
		}
		if got := store.GetHistory("empty"); len(got) != 0 {
			t.Fatalf("empty session history length = %d", len(got))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})
}

func TestSessionStore_SaveBranches(t *testing.T) {
	store, mock := newMockSessionStore(t)

	if err := store.Save("missing"); err != nil {
		t.Fatalf("Save missing session should not fail: %v", err)
	}

	store.GetOrCreate("sess-1")
	mock.ExpectExec("MERGE INTO PICO_SESSIONS").
		WillReturnError(errors.New("save failed"))
	if err := store.Save("sess-1"); err == nil {
		t.Fatal("Save should return exec errors")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestStateStore_Branches(t *testing.T) {
	t.Run("get cache miss populates and timestamp", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		store := &StateStore{db: db, agentID: "agent-1", cache: make(map[string]string)}
		mock.ExpectQuery("SELECT state_value FROM PICO_STATE").
			WithArgs("theme", "agent-1").
			WillReturnRows(sqlmock.NewRows([]string{"state_value"}).AddRow("dark"))
		if got := store.Get("theme"); got != "dark" {
			t.Fatalf("Get cache miss = %q", got)
		}
		if got := store.Get("theme"); got != "dark" {
			t.Fatalf("Get cache hit = %q", got)
		}

		now := time.Now()
		mock.ExpectQuery("SELECT MAX\\(updated_at\\) FROM PICO_STATE").
			WithArgs("agent-1").
			WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(now))
		if got := store.GetTimestamp(); !got.Equal(now) {
			t.Fatalf("GetTimestamp = %v, want %v", got, now)
		}

		mock.ExpectQuery("SELECT MAX\\(updated_at\\) FROM PICO_STATE").
			WithArgs("agent-1").
			WillReturnError(errors.New("timestamp failed"))
		if got := store.GetTimestamp(); !got.IsZero() {
			t.Fatalf("GetTimestamp error = %v, want zero", got)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("set error and load error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		store := &StateStore{db: db, agentID: "agent-1", cache: make(map[string]string)}
		mock.ExpectExec("MERGE INTO PICO_STATE").
			WillReturnError(errors.New("set failed"))
		if err := store.Set("theme", "dark"); err == nil {
			t.Fatal("Set should return exec errors")
		}

		mock.ExpectQuery("SELECT state_key, state_value FROM PICO_STATE").
			WithArgs("agent-1").
			WillReturnError(errors.New("load failed"))
		store.loadAll()

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})
}

func TestConfigStore_ErrorAndNullBranches(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	store := NewConfigStore(db, "agent-1")

	mock.ExpectQuery("SELECT config_value FROM PICO_CONFIG").
		WithArgs("null-key", "agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"config_value"}).AddRow(nil))
	value, err := store.GetConfigValue("null-key")
	if err != nil || value != "" {
		t.Fatalf("GetConfigValue null value=%q err=%v", value, err)
	}

	mock.ExpectQuery("SELECT config_value FROM PICO_CONFIG").
		WithArgs("bad-key", "agent-1").
		WillReturnError(errors.New("query failed"))
	if _, err := store.GetConfigValue("bad-key"); err == nil {
		t.Fatal("GetConfigValue should return query errors")
	}

	mock.ExpectExec("MERGE INTO PICO_CONFIG").
		WillReturnError(errors.New("set failed"))
	if err := store.SetConfigValue("key", "value"); err == nil {
		t.Fatal("SetConfigValue should return exec errors")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestSchemaErrorBranches(t *testing.T) {
	t.Run("table create failure", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		mock.ExpectExec("CREATE TABLE").
			WillReturnError(errors.New("create failed"))
		if err := InitSchema(db); err == nil {
			t.Fatal("InitSchema should return table creation errors")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("index warnings and schema version warning", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		for range tableDDL {
			mock.ExpectExec("CREATE TABLE").
				WillReturnResult(sqlmock.NewResult(0, 0))
		}
		for range indexDDL {
			mock.ExpectExec("CREATE INDEX").
				WillReturnError(errors.New("index failed"))
		}
		for range vectorIndexDDL {
			mock.ExpectExec("CREATE VECTOR INDEX").
				WillReturnError(errors.New("vector index failed"))
		}
		mock.ExpectExec("MERGE INTO PICO_META").
			WillReturnError(errors.New("version failed"))

		if err := InitSchema(db); err != nil {
			t.Fatalf("InitSchema should ignore index/version warnings: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled expectations: %v", err)
		}
	})
}

func TestValidateSQLIdentifier(t *testing.T) {
	valid := []string{"MODEL", "MODEL_1", "model_name"}
	for _, value := range valid {
		if err := validateSQLIdentifier(value); err != nil {
			t.Fatalf("validateSQLIdentifier(%q) error = %v", value, err)
		}
	}

	invalid := []string{"", "1_MODEL", "BAD-NAME"}
	for _, value := range invalid {
		if err := validateSQLIdentifier(value); err == nil {
			t.Fatalf("validateSQLIdentifier(%q) should fail", value)
		}
	}
}
