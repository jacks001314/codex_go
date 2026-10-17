package doctor

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDiagnosticIntegrityCheckDeadlineLikeRust pins Rust #40688: a diagnostic
// attachment bounds each integrity scan, reports an incomplete clean scan as a
// warning, and leaves direct doctor checks unbounded.
func TestDiagnosticIntegrityCheckDeadlineLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE sample (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		_ = db.Close()
		t.Fatalf("Create table error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var details []string
	status := sqliteIntegrityDetailForDoctor(&details, "state.db", path, time.Now().Add(-time.Second))
	if status != CheckStatusWarning {
		t.Fatalf("expired deadline status = %v, details = %v", status, details)
	}
	if len(details) != 1 || !strings.Contains(details[0], "scan exceeded time limit") {
		t.Fatalf("expired deadline details = %v", details)
	}

	details = nil
	status = sqliteIntegrityDetailForDoctor(&details, "state.db", path, time.Time{})
	if status != CheckStatusOK {
		t.Fatalf("unbounded status = %v, details = %v", status, details)
	}
	if len(details) != 1 || !strings.Contains(details[0], "integrity: ok") {
		t.Fatalf("unbounded details = %v", details)
	}
}

func TestIntegrityResultKeepsCorruptionRowsLikeRust(t *testing.T) {
	if integrityResultHasCorruption(nil) {
		t.Fatal("empty result classified as corruption")
	}
	if integrityResultHasCorruption([]string{"ok"}) {
		t.Fatal("clean result classified as corruption")
	}
	if !integrityResultHasCorruption([]string{"ok", "row 4 missing from index"}) {
		t.Fatal("partial corruption rows were dropped")
	}
}
