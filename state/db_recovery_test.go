package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifiesSQLiteErrors(t *testing.T) {
	if !IsDBRecoveryLocked("database is locked") {
		t.Fatalf("expected lock detail")
	}
	if !IsDBRecoveryCorruption("file is not a database") {
		t.Fatalf("expected corruption detail")
	}
	if IsDBRecoveryLocked("file lock unavailable") || IsDBRecoveryCorruption("path contains corrupt") {
		t.Fatalf("generic lock/corrupt words must not trigger SQLite recovery")
	}
}

func TestBackupFilesForFreshStartMovesFailedDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.sqlite")
	for _, path := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	backups, err := BackupDBFilesForFreshStart(&DBRecoveryStartupError{DatabasePath: db, Detail: "corrupt"}, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("BackupFilesForFreshStart() error = %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("unexpected backups: %#v", backups)
	}
	for _, path := range []string{db, db + "-wal", db + "-shm"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("original %s should be moved, stat err=%v", path, err)
		}
	}
	if _, err := os.Stat(backups[0].BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if DBRecoveryBackupFolder(backups) != filepath.Dir(backups[0].BackupPath) {
		t.Fatalf("unexpected backup folder")
	}
	if got := filepath.Base(DBRecoveryBackupFolder(backups)); got != "sqlite-1-0" {
		t.Fatalf("backup folder = %q", got)
	}
}

func TestAutoBackupRecoverableForBlockingParentFile(t *testing.T) {
	dir := t.TempDir()
	blocking := filepath.Join(dir, "sqlite-home")
	if err := os.WriteFile(blocking, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	err := &DBRecoveryStartupError{DatabasePath: filepath.Join(blocking, "state.sqlite"), Detail: "file exists"}
	if !err.AutoBackupRecoverable() {
		t.Fatalf("blocking sqlite home file should be recoverable")
	}
	backups, backupErr := BackupDBFilesForFreshStart(err, time.Unix(1, 0))
	if backupErr != nil {
		t.Fatalf("BackupFilesForFreshStart() error = %v", backupErr)
	}
	if len(backups) != 1 {
		t.Fatalf("unexpected backups: %#v", backups)
	}
	info, statErr := os.Stat(blocking)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("blocking path should be recreated as dir, info=%#v err=%v", info, statErr)
	}
	wantParent := filepath.Join(dir, "sqlite-home.db-backups")
	if !strings.HasPrefix(backups[0].BackupPath, wantParent+string(filepath.Separator)) {
		t.Fatalf("blocking sqlite home backup = %s, want under %s", backups[0].BackupPath, wantParent)
	}
}

func TestBackupFilesForFreshStartIsolatesRequestedDatabaseAndUsesUniqueFolder(t *testing.T) {
	dir := t.TempDir()
	failed := filepath.Join(dir, LogsSQLiteFilename)
	unrelated := filepath.Join(dir, StateSQLiteFilename)
	for _, path := range []string{failed, failed + "-wal", failed + "-shm", unrelated, unrelated + "-wal"} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(123, 0)
	first, err := BackupDBFilesForFreshStart(&DBRecoveryStartupError{DatabasePath: failed, Detail: "file is not a database"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated database was moved: %v", err)
	}
	if _, err := os.Stat(unrelated + "-wal"); err != nil {
		t.Fatalf("unrelated WAL was moved: %v", err)
	}
	if err := os.WriteFile(failed, []byte("again"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := BackupDBFilesForFreshStart(&DBRecoveryStartupError{DatabasePath: failed, Detail: "file is not a database"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(DBRecoveryBackupFolder(first)) != "sqlite-123-0" || filepath.Base(DBRecoveryBackupFolder(second)) != "sqlite-123-1" {
		t.Fatalf("backup folders = %s, %s", DBRecoveryBackupFolder(first), DBRecoveryBackupFolder(second))
	}
}
