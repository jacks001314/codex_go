package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
		strings.Contains(detail, "database table is locked") ||
		strings.Contains(detail, "busy") ||
		strings.Contains(detail, "locked")
}

func IsDBRecoveryCorruption(detail string) bool {
	detail = strings.ToLower(detail)
	return strings.Contains(detail, "malformed") ||
		strings.Contains(detail, "corrupt") ||
		strings.Contains(detail, "not a database") ||
		strings.Contains(detail, "file is not a database")
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
	path := startupError.DatabasePath
	parent := filepath.Dir(path)
	blockingParentFile := false
	if info, err := os.Stat(parent); err == nil && info.Mode().IsRegular() {
		path = parent
		parent = filepath.Dir(parent)
		blockingParentFile = true
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	backupDir := filepath.Join(parent, "db-backups", now.UTC().Format("20060102-150405"))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, err
	}
	backupPath := filepath.Join(backupDir, filepath.Base(path))
	if err := os.Rename(path, backupPath); err != nil {
		return nil, err
	}
	if blockingParentFile {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	return []DBRecoveryBackup{{OriginalPath: path, BackupPath: backupPath}}, nil
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
