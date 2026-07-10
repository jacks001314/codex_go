package appserver

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	rustStateSQLiteFilename    = "state_5.sqlite"
	rustMemoriesSQLiteFilename = "memories_1.sqlite"
)

func rustSQLiteHome(codexHome string) string {
	if sqliteHome := strings.TrimSpace(os.Getenv("CODEX_SQLITE_HOME")); sqliteHome != "" {
		return sqliteHome
	}
	return strings.TrimSpace(codexHome)
}

func updateRustStateThreadMemoryMode(codexHome string, threadID string, mode ThreadMemoryMode) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || mode == "" {
		return nil
	}
	dbPath := filepath.Join(rustSQLiteHome(codexHome), rustStateSQLiteFilename)
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE threads SET memory_mode = ? WHERE id = ?`, string(mode), threadID)
	if isSQLiteMissingSchemaError(err) {
		return nil
	}
	return err
}

func updateRustStateThreadGitInfo(codexHome string, threadID string, git map[string]string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	dbPath := filepath.Join(rustSQLiteHome(codexHome), rustStateSQLiteFilename)
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(
		`UPDATE threads SET git_sha = ?, git_branch = ?, git_origin_url = ? WHERE id = ?`,
		sqliteNullableString(git["sha"]),
		sqliteNullableString(git["branch"]),
		sqliteNullableString(firstNonEmpty(git["origin_url"], git["originUrl"])),
		threadID,
	)
	if isSQLiteMissingSchemaError(err) {
		return nil
	}
	return err
}

func clearRustMemoriesSQLiteData(codexHome string) error {
	dbPath := filepath.Join(rustSQLiteHome(codexHome), rustMemoriesSQLiteFilename)
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM stage1_outputs`); err != nil {
		_ = tx.Rollback()
		if isSQLiteMissingSchemaError(err) {
			return nil
		}
		return err
	}
	if _, err = tx.Exec(`DELETE FROM jobs WHERE kind = ? OR kind = ?`, "memory_stage1", "memory_consolidate_global"); err != nil {
		_ = tx.Rollback()
		if isSQLiteMissingSchemaError(err) {
			return nil
		}
		return err
	}
	return tx.Commit()
}

func sqliteNullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isSQLiteMissingSchemaError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") || strings.Contains(message, "no such column")
}
