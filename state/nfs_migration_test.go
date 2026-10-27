package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSQLiteHeaderFile(t *testing.T, path string, readVersion, writeVersion byte) {
	t.Helper()
	header := make([]byte, 100)
	copy(header, "SQLite format 3\x00")
	header[18] = readVersion
	header[19] = writeVersion
	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatalf("write header: %v", err)
	}
}

// TestNFSDatabaseNeedsMigrationInspectsHeaderLikeRust mirrors Rust #57226's
// header inspection: the file-format read/write versions select WAL, and the
// check never opens the database (so it cannot touch shared memory).
func TestNFSDatabaseNeedsMigrationInspectsHeaderLikeRust(t *testing.T) {
	directory := t.TempDir()
	walPath := filepath.Join(directory, "wal.sqlite")
	writeSQLiteHeaderFile(t, walPath, 2, 2)
	needs, err := NFSDatabaseNeedsMigration(walPath)
	if err != nil || !needs {
		t.Fatalf("WAL header needs migration = %v, %v", needs, err)
	}

	deletePath := filepath.Join(directory, "delete.sqlite")
	writeSQLiteHeaderFile(t, deletePath, 1, 1)
	needs, err = NFSDatabaseNeedsMigration(deletePath)
	if err != nil || needs {
		t.Fatalf("rollback header needs migration = %v, %v", needs, err)
	}

	invalidPath := filepath.Join(directory, "invalid.sqlite")
	if err := os.WriteFile(invalidPath, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NFSDatabaseNeedsMigration(invalidPath); err == nil {
		t.Fatal("invalid header must report an error")
	}

	missingPath := filepath.Join(directory, "missing.sqlite")
	needs, err = NFSDatabaseNeedsMigration(missingPath)
	if err != nil || needs {
		t.Fatalf("missing database needs migration = %v, %v", needs, err)
	}
	// A pending backup means an interrupted replacement still needs attention.
	replacement := newNFSReplacement(missingPath)
	if err := os.Mkdir(replacement.backupDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement.backupDatabase, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	needs, err = NFSDatabaseNeedsMigration(missingPath)
	if err != nil || !needs {
		t.Fatalf("pending backup needs migration = %v, %v", needs, err)
	}
}

// TestNFSDatabaseNeedsPermissionConfirmationLikeRust mirrors Rust #57226: only
// files whose mode differs from 0600 require explicit confirmation.
func TestNFSDatabaseNeedsPermissionConfirmationLikeRust(t *testing.T) {
	requireUnixMigration(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "state.sqlite")
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	needs, err := NFSDatabaseNeedsPermissionConfirmation(path)
	if err != nil || needs {
		t.Fatalf("0600 confirmation = %v, %v", needs, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	needs, err = NFSDatabaseNeedsPermissionConfirmation(path)
	if err != nil || !needs {
		t.Fatalf("0644 confirmation = %v, %v", needs, err)
	}
}

func requireUnixMigration(t *testing.T) {
	t.Helper()
	if !NFSSQLiteMigrationSupported() {
		t.Skip("NFS database migration requires Unix filesystem locking")
	}
}

// TestNFSMigrationConvertsWALDatabaseLikeRust mirrors Rust #57225/#57226: an
// existing WAL database is replaced with a validated DELETE copy, its original
// files and WAL stay in the private backup directory, and the data survives.
func TestNFSMigrationConvertsWALDatabaseLikeRust(t *testing.T) {
	requireUnixMigration(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "state.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (value TEXT); INSERT INTO items (value) VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if needs, err := NFSDatabaseNeedsMigration(path); err != nil || !needs {
		t.Fatalf("WAL database needs migration = %v, %v", needs, err)
	}

	migration, err := LockNFSDBMigration(directory)
	if err != nil {
		t.Fatalf("LockNFSDBMigration: %v", err)
	}
	defer migration.Close()
	if second, err := LockNFSDBMigration(directory); err == nil {
		_ = second.Close()
		t.Fatal("a second migration lock must fail while the first is held")
	} else if !errors.Is(err, errNFSMigrationLocked) {
		t.Fatalf("second lock error = %v, want errNFSMigrationLocked", err)
	}

	outcome, err := migration.Migrate(context.Background(), path, directory)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if outcome.Kind != NFSMigrationMigrated {
		t.Fatalf("outcome = %#v, want migrated", outcome)
	}
	backup, err := os.Stat(outcome.BackupDirectory)
	if err != nil || !backup.IsDir() {
		t.Fatalf("backup directory = %v, %v", backup, err)
	}
	replacement := newNFSReplacement(path)
	for _, expected := range []string{replacement.backupDatabase, filepath.Join(replacement.backupDirectory, "migration-v1")} {
		if _, err := os.Stat(expected); err != nil {
			t.Fatalf("backup file %s: %v", expected, err)
		}
	}
	if needs, err := NFSDatabaseNeedsMigration(path); err != nil || needs {
		t.Fatalf("migrated database needs migration = %v, %v", needs, err)
	}

	check, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var mode string
	if err := check.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Fatalf("journal mode = %q, want delete", mode)
	}
	var value string
	if err := check.QueryRow(`SELECT value FROM items`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "kept" {
		t.Fatalf("value = %q, want kept", value)
	}

	// A second pass reports the database is already in DELETE mode.
	outcome, err = migration.Migrate(context.Background(), path, directory)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if outcome.Kind != NFSMigrationAlreadyDelete {
		t.Fatalf("second outcome = %#v, want alreadyDelete", outcome)
	}
}

// TestNFSMigrationResumesInterruptedReplacementLikeRust mirrors Rust #57226:
// when the original was moved to the backup and the validated copy remains,
// resume installs it instead of rebuilding.
func TestNFSMigrationResumesInterruptedReplacementLikeRust(t *testing.T) {
	requireUnixMigration(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "state.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE items (value TEXT); INSERT INTO items (value) VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migration, err := LockNFSDBMigration(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Close()
	if _, err := migration.Migrate(context.Background(), path, directory); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	replacement := newNFSReplacement(path)
	// Simulate a cutover interrupted between moving the original and installing
	// the replacement: the source is gone, the original sits in the backup, and
	// only the validated copy remains next to it (Rust #57226 moves the installed
	// database into `delete.sqlite` for the same state).
	if err := os.Rename(path, replacement.replacementPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(replacement.backupDatabase); err != nil {
		t.Fatalf("backup original: %v", err)
	}
	outcome, err := migration.Migrate(context.Background(), path, directory)
	if err != nil {
		t.Fatalf("resume Migrate: %v", err)
	}
	if outcome.Kind != NFSMigrationMigrated {
		t.Fatalf("resume outcome = %#v, want migrated", outcome)
	}
	if _, err := os.Stat(replacement.replacementPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement should be consumed, stat err = %v", err)
	}
}

// TestNFSMigrationFailureStaysOutOfCorruptionRecoveryLikeRust mirrors Rust
// #57225 (codex-rs/state/src/sqlite/nfs.rs): a failed required migration is
// reported without its SQLite error chain, because automatic backup-and-rebuild
// recovery must never treat an interrupted cutover as corruption and discard the
// preserved WAL backup.
func TestNFSMigrationFailureStaysOutOfCorruptionRecoveryLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	if err := os.WriteFile(path, []byte("this file is not a SQLite database at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	dsn, err := sqliteFileDSN(path, false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	corruptErr := db.QueryRow(`SELECT count(*) FROM sqlite_schema`).Scan(&count)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if corruptErr == nil {
		t.Fatal("garbage database unexpectedly opened")
	}
	if !IsSQLiteCorruptionError(corruptErr) {
		t.Fatalf("test precondition: %v is not classified as SQLite corruption", corruptErr)
	}

	wrapped := nfsMigrationFailure(path, corruptErr)
	if IsSQLiteCorruptionError(wrapped) {
		t.Fatalf("migration failure %q must not be classified as corruption", wrapped)
	}
	initErr := &RuntimeDBInitError{Label: "state DB", Operation: "migrate", Path: path, Err: wrapped}
	if _, ok := RuntimeDBPathForCorruptionError(initErr); ok {
		t.Fatal("migration failure must not select automatic corruption recovery")
	}
	if !strings.Contains(wrapped.Error(), path) || !strings.Contains(wrapped.Error(), "migrate-nfs-db") {
		t.Fatalf("migration failure %q must name the database and the recovery command", wrapped)
	}
}
