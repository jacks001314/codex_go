package state

import (
	"os"
	"path/filepath"
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
}

func TestBackupFilesForFreshStartMovesFailedDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.sqlite")
	if err := os.WriteFile(db, []byte("bad"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	backups, err := BackupDBFilesForFreshStart(&DBRecoveryStartupError{DatabasePath: db, Detail: "corrupt"}, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("BackupFilesForFreshStart() error = %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("unexpected backups: %#v", backups)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("original db should be moved, stat err=%v", err)
	}
	if _, err := os.Stat(backups[0].BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if DBRecoveryBackupFolder(backups) != filepath.Dir(backups[0].BackupPath) {
		t.Fatalf("unexpected backup folder")
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
}
