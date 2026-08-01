package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const dbRecoveryBackupDirName = "db-backups"

type DBRecoveryStartupError struct {
	DatabasePath string
	Detail       string
}

type DBRecoveryBackup struct {
	OriginalPath string
	BackupPath   string
}

func IsDBRecoveryLocked(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "database is locked") ||
		strings.Contains(detail, "database is busy")
}

func IsDBRecoveryCorruption(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "database disk image is malformed") ||
		strings.Contains(detail, "database schema is malformed") ||
		strings.Contains(detail, "database is corrupt") ||
		strings.Contains(detail, "file is not a database") ||
		strings.Contains(detail, "sqlite_corrupt") ||
		strings.Contains(detail, "sqlite_notadb") ||
		strings.Contains(detail, "(code: 11)") ||
		strings.Contains(detail, "(code: 26)")
}

func IsSQLiteCorruptionError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == 11 || code == 26 || IsDBRecoveryCorruption(sqliteErr.Error())
}

func RuntimeDBPathForCorruptionError(err error) (string, bool) {
	if !IsSQLiteCorruptionError(err) {
		return "", false
	}
	var initErr *RuntimeDBInitError
	if !errors.As(err, &initErr) || strings.TrimSpace(initErr.Path) == "" {
		return "", false
	}
	return initErr.Path, true
}

func (e *DBRecoveryStartupError) AutoBackupRecoverable() bool {
	if e == nil {
		return false
	}
	if IsDBRecoveryCorruption(e.Detail) {
		return true
	}
	parent := filepath.Dir(e.DatabasePath)
	info, err := os.Stat(parent)
	return err == nil && info.Mode().IsRegular()
}

func BackupDBFilesForFreshStart(startupError *DBRecoveryStartupError, now time.Time) ([]DBRecoveryBackup, error) {
	if startupError == nil || strings.TrimSpace(startupError.DatabasePath) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	dbPath := filepath.Clean(startupError.DatabasePath)
	sqliteHome := filepath.Dir(dbPath)
	info, err := os.Stat(sqliteHome)
	switch {
	case err == nil && info.IsDir():
		return backupRuntimeDBFiles(dbPath, now)
	case err == nil:
		return backupBlockingSQLiteHome(sqliteHome, now)
	case os.IsNotExist(err):
		if err := os.MkdirAll(sqliteHome, 0o700); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("no Codex runtime database files were found to back up for %s", dbPath)
	default:
		return nil, err
	}
}

func backupRuntimeDBFiles(dbPath string, now time.Time) ([]DBRecoveryBackup, error) {
	sqliteHome := filepath.Dir(dbPath)
	paths := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	backupDir, err := createUniqueDBBackupDir(filepath.Join(sqliteHome, dbRecoveryBackupDirName), now)
	if err != nil {
		return nil, err
	}
	backups := make([]DBRecoveryBackup, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		backupPath := filepath.Join(backupDir, filepath.Base(path))
		if err := os.Rename(path, backupPath); err != nil {
			return nil, err
		}
		backups = append(backups, DBRecoveryBackup{OriginalPath: path, BackupPath: backupPath})
	}
	if len(backups) == 0 {
		_ = os.Remove(backupDir)
		return nil, fmt.Errorf("no Codex runtime database files were found to back up")
	}
	return backups, nil
}

func backupBlockingSQLiteHome(sqliteHome string, now time.Time) ([]DBRecoveryBackup, error) {
	parent := filepath.Dir(sqliteHome)
	base := filepath.Base(sqliteHome)
	backupParent := filepath.Join(parent, base+"."+dbRecoveryBackupDirName)
	backupDir, err := createUniqueDBBackupDir(backupParent, now)
	if err != nil {
		return nil, err
	}
	backupPath := filepath.Join(backupDir, base)
	if err := os.Rename(sqliteHome, backupPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sqliteHome, 0o700); err != nil {
		return nil, err
	}
	return []DBRecoveryBackup{{OriginalPath: sqliteHome, BackupPath: backupPath}}, nil
}

func createUniqueDBBackupDir(backupParent string, now time.Time) (string, error) {
	if err := os.MkdirAll(backupParent, 0o700); err != nil {
		return "", err
	}
	if now.IsZero() {
		now = time.Now()
	}
	timestamp := now.Unix()
	for sequence := uint32(0); ; sequence++ {
		backupDir := filepath.Join(backupParent, fmt.Sprintf("sqlite-%d-%d", timestamp, sequence))
		err := os.Mkdir(backupDir, 0o700)
		if err == nil {
			return backupDir, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
}

func DBRecoveryBackupFolder(backups []DBRecoveryBackup) string {
	if len(backups) == 0 {
		return ""
	}
	return filepath.Dir(backups[0].BackupPath)
}

func DBRecoveryTechnicalDetails(startupError *DBRecoveryStartupError) []string {
	if startupError == nil {
		return nil
	}
	return []string{
		"Location: " + startupError.DatabasePath,
		"Cause: " + startupError.Detail,
	}
}
