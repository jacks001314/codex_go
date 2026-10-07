package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	modernsqlite "modernc.org/sqlite"
)

// This file ports the typed-corruption classification from Rust upstream
// #49710 "Classify SQLite corruption using typed error codes" (596ae94fb0).
//
// Rust reference:
//   - codex-rs/tui/src/startup_error.rs: LocalStateDbStartupError { #[source]
//     source } + is_corruption() -> codex_state::is_sqlite_corruption_error(&source)
//   - codex-rs/state/src/runtime/recovery.rs: sqlite_error_source_is_corruption
//     accepts DatabaseCorrupt (11) | NotADatabase (26); message-text matching was
//     deleted.
//   - Rust tests: codex-rs/tui/src/lib.rs
//     `embedded_state_db_corruption_preserves_failed_database_for_cli_recovery`
//     and codex-rs/state/src/runtime/recovery_tests.rs
//     `sqlite_error_detail_classifies_lock_errors` (renamed from
//     `sqlite_error_detail_classifies_corruption_and_lock_errors`).

// sqliteTypedError extracts the underlying *modernsqlite.Error carried by err.
func sqliteTypedError(t *testing.T, err error) *modernsqlite.Error {
	t.Helper()
	var se *modernsqlite.Error
	if !errors.As(err, &se) {
		t.Fatalf("expected a typed *sqlite.Error in chain, got %T: %v", err, err)
	}
	return se
}

// sqliteNotADatabaseError drives a real driver failure for a file whose header
// is not a SQLite database (SQLITE_NOTADB, primary code 26).
func sqliteNotADatabaseError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logs.sqlite")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQLite(context.Background(), path)
	if err == nil {
		_ = db.Close()
		t.Fatal("opening a non-sqlite file should fail")
	}
	return err
}

// sqliteCorruptImageError drives a real driver failure for a valid header whose
// data page was overwritten (SQLITE_CORRUPT, primary code 11).
func sqliteCorruptImageError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(),
		`create table t (a text);
		 insert into t values (printf('%.*c', 3000, 'x')),(printf('%.*c', 3000, 'y'))`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8192 {
		t.Fatalf("expected a multi-page sqlite file, got %d bytes", len(data))
	}
	// Keep page 1 (header + sqlite_master) intact and overwrite the data page so
	// the driver reports SQLITE_CORRUPT (11) instead of SQLITE_NOTADB (26).
	for i := 4096; i < len(data); i++ {
		data[i] = 0x41
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db2, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	rows, err := db2.QueryContext(context.Background(), "select * from t")
	if err == nil {
		_ = rows.Close()
		t.Fatal("querying an overwritten data page should fail")
	}
	return err
}

// sqliteNonCorruptionError returns a real typed driver error whose result code is
// neither 11 nor 26 (SQLITE_ERROR from an unknown table).
func sqliteNonCorruptionError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(), "select * from does_not_exist")
	if err == nil {
		t.Fatal("querying a missing table should fail")
	}
	return err
}

// TestDBRecoveryStartupErrorClassifiesCorruptionByTypedCodeLikeRust mirrors Rust
// #49710: LocalStateDbStartupError::is_corruption() classifies DatabaseCorrupt
// (11) and NotADatabase (26) from the preserved source, and the failed database
// path is retained (tui/src/lib.rs
// embedded_state_db_corruption_preserves_failed_database_for_cli_recovery).
func TestDBRecoveryStartupErrorClassifiesCorruptionByTypedCodeLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"database disk image is malformed", sqliteCorruptImageError(t), 11},
		{"file is not a database", sqliteNotADatabaseError(t), 26},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqliteTypedError(t, tc.err).Code() & 0xff; got != tc.wantCode {
				t.Fatalf("typed sqlite code = %d, want %d", got, tc.wantCode)
			}
			dbPath := filepath.Join(t.TempDir(), "state.sqlite")
			startup := &DBRecoveryStartupError{
				DatabasePath: dbPath,
				Detail:       tc.err.Error(),
				Source:       tc.err,
			}
			if !startup.IsCorruption() {
				t.Fatalf("IsCorruption() = false, want true for typed SQLite corruption: %v", tc.err)
			}
			if !startup.AutoBackupRecoverable() {
				t.Fatalf("AutoBackupRecoverable() = false, want true for typed SQLite corruption: %v", tc.err)
			}
			if startup.DatabasePath != dbPath {
				t.Fatalf("failed database path not preserved: %q", startup.DatabasePath)
			}
		})
	}
}

// TestDBRecoveryStartupErrorIgnoresCorruptionTextWithoutTypedCodeLikeRust mirrors
// Rust #49710 replacing message-text matching with result-code classification: a
// detail string that *looks* corrupt must not qualify when the preserved source
// is a non-corruption driver error.
func TestDBRecoveryStartupErrorIgnoresCorruptionTextWithoutTypedCodeLikeRust(t *testing.T) {
	typed := sqliteNonCorruptionError(t)
	if code := sqliteTypedError(t, typed).Code() & 0xff; code == 11 || code == 26 {
		t.Fatalf("fixture error unexpectedly classified as corruption, code=%d", code)
	}
	startup := &DBRecoveryStartupError{
		DatabasePath: filepath.Join(t.TempDir(), "state.sqlite"),
		Detail:       "file is not a database",
		Source:       typed,
	}
	if startup.IsCorruption() {
		t.Fatal("IsCorruption() = true, want false: typed source disagrees with corrupt-looking text")
	}
	if startup.AutoBackupRecoverable() {
		t.Fatal("AutoBackupRecoverable() = true, want false: corrupt-looking text must not trigger backup")
	}
}

// TestDBRecoveryStartupErrorTextFallbackOnlyWhenSourceMissingLikeRust documents a
// deliberate Go difference: Rust's LocalStateDbStartupError::new always attaches a
// source and derives detail from it, so Rust never classifies by text. Go keeps
// Detail as an independent field, so a Source-less value keeps the legacy text
// classification for existing call sites.
func TestDBRecoveryStartupErrorTextFallbackOnlyWhenSourceMissingLikeRust(t *testing.T) {
	startup := &DBRecoveryStartupError{
		DatabasePath: filepath.Join(t.TempDir(), "state.sqlite"),
		Detail:       "file is not a database",
	}
	if !startup.IsCorruption() || !startup.AutoBackupRecoverable() {
		t.Fatal("Source-less corruption detail should keep the documented text fallback")
	}
}

// TestSQLiteRecoveryLockClassificationLikeRust mirrors the Rust test that survived
// #49710 unchanged (recovery_tests.rs::sqlite_error_detail_classifies_lock_errors):
// lock/busy classification stays text based.
func TestSQLiteRecoveryLockClassificationLikeRust(t *testing.T) {
	if !IsDBRecoveryLocked("database is locked") || !IsDBRecoveryLocked("database is busy") {
		t.Fatal("lock classification should accept locked/busy details")
	}
	if IsDBRecoveryLocked("file lock unavailable") {
		t.Fatal("unrelated text must not classify as a SQLite lock")
	}
}
