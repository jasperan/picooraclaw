package oracle

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestInitSchema_CreatesAllTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	expectedTables := []string{
		"PICO_META", "PICO_MEMORIES", "PICO_DAILY_NOTES", "PICO_SESSIONS",
		"PICO_STATE", "PICO_CONFIG", "PICO_PROMPTS", "PICO_TRANSCRIPTS",
	}

	// Expect CREATE TABLE for each
	for range expectedTables {
		mock.ExpectExec("CREATE TABLE").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Expect regular indexes (5)
	for range indexDDL {
		mock.ExpectExec("CREATE INDEX").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Expect vector indexes (2)
	for range vectorIndexDDL {
		mock.ExpectExec("CREATE VECTOR INDEX").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Migrations already applied: version checks return the latest version
	// so applyMigrations and setSchemaVersionIfEmpty skip all steps.
	latest := migrations[len(migrations)-1].Version
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT meta_value FROM PICO_META WHERE meta_key = 'schema_version'").
			WillReturnRows(sqlmock.NewRows([]string{"meta_value"}).AddRow(latest))
	}

	err = InitSchema(db)
	if err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestInitSchema_Idempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	// Simulate all tables already existing (ORA-00955)
	for i := 0; i < 8; i++ {
		mock.ExpectExec("CREATE TABLE").
			WillReturnError(fmt.Errorf("ORA-00955: name is already used by an existing object"))
	}

	// Indexes already exist (ORA-01408)
	for range indexDDL {
		mock.ExpectExec("CREATE INDEX").
			WillReturnError(fmt.Errorf("ORA-01408: such column list already indexed"))
	}

	for range vectorIndexDDL {
		mock.ExpectExec("CREATE VECTOR INDEX").
			WillReturnError(fmt.Errorf("ORA-00955: name is already used by an existing object"))
	}

	// Schema version already at latest: both version checks return it.
	latest := migrations[len(migrations)-1].Version
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT meta_value FROM PICO_META WHERE meta_key = 'schema_version'").
			WillReturnRows(sqlmock.NewRows([]string{"meta_value"}).AddRow(latest))
	}

	err = InitSchema(db)
	if err != nil {
		t.Fatalf("idempotent InitSchema should not fail: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestIsORA00955(t *testing.T) {
	if !isORA00955(fmt.Errorf("ORA-00955: name is already used")) {
		t.Error("should detect ORA-00955")
	}
	if isORA00955(fmt.Errorf("ORA-01408: column list already indexed")) {
		t.Error("should not match ORA-01408")
	}
}

func TestIsORA01408(t *testing.T) {
	if !isORA01408(fmt.Errorf("ORA-01408: such column list already indexed")) {
		t.Error("should detect ORA-01408")
	}
	if isORA01408(fmt.Errorf("ORA-00955: name already used")) {
		t.Error("should not match ORA-00955")
	}
}

func TestTableDDL_AllTablesHavePICOPrefix(t *testing.T) {
	for name := range tableDDL {
		if name[:5] != "PICO_" {
			t.Errorf("table %q does not have PICO_ prefix", name)
		}
	}
}

func TestTableDDL_ExpectedTableCount(t *testing.T) {
	if len(tableDDL) != 8 {
		t.Errorf("expected 8 tables, got %d", len(tableDDL))
	}
}

func TestApplyMigrations_AppliesInOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	// Fresh schema: no version recorded yet.
	mock.ExpectQuery("SELECT meta_value FROM PICO_META WHERE meta_key = 'schema_version'").
		WillReturnError(sql.ErrNoRows)

	// Migration 1.1.0
	mock.ExpectExec("ALTER TABLE PICO_TRANSCRIPTS ADD").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE VECTOR INDEX IDX_PICO_TRANSCRIPTS_VEC").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IDX_PICO_MEMORIES_CTX").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("MERGE INTO PICO_META").WillReturnResult(sqlmock.NewResult(0, 1)) // lexical_mode
	mock.ExpectExec("MERGE INTO PICO_META").WillReturnResult(sqlmock.NewResult(0, 1)) // 1.1.0

	// Migration 1.2.0
	mock.ExpectExec("CREATE TABLE PICO_EPISODES").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IDX_PICO_EPISODES_AGENT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE VECTOR INDEX IDX_PICO_EPISODES_VEC").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE PICO_CONSOLIDATION").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("MERGE INTO PICO_META").WillReturnResult(sqlmock.NewResult(0, 1)) // 1.2.0

	// Migration 1.3.0
	mock.ExpectExec("CREATE TABLE PICO_CODE_NODES").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE PICO_CODE_EDGES").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IDX_PICO_CODE_NODES_REPO").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IDX_PICO_CODE_EDGES_SRC").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IDX_PICO_CODE_EDGES_DST").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE VECTOR INDEX IDX_PICO_CODE_NODES_VEC").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE PROPERTY GRAPH").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE PICO_SKILL_USAGE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE VECTOR INDEX IDX_PICO_SKILL_USAGE_VEC").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("MERGE INTO PICO_META").WillReturnResult(sqlmock.NewResult(0, 1)) // 1.3.0

	if err := applyMigrations(db); err != nil {
		t.Fatalf("applyMigrations failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"1.2.0", "1.2.0", 0},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.3.0", "1.3.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
