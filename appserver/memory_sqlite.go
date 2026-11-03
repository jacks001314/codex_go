package appserver

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"codex_go/state"
)

const (
	rustStateSQLiteFilename    = state.StateSQLiteFilename
	rustMemoriesSQLiteFilename = state.MemoriesSQLiteFilename
)

func rustSQLiteHome(codexHome string) string {
	return state.ResolveSQLiteHome(codexHome)
}

func updateRustStateThreadMemoryMode(codexHome string, threadID string, mode ThreadMemoryMode) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || mode == "" {
		return nil
	}
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return err
	}
	dbPath := config.StateDBPath()
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := config.OpenReadWrite(nil, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return updateRustStateThreadRow(db, threadID, []string{"memory_mode = ?"}, []any{string(mode)})
}

func updateRustStateThreadName(codexHome string, threadID string, name string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return err
	}
	dbPath := config.StateDBPath()
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := config.OpenReadWrite(nil, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return updateRustStateThreadRow(db, threadID, []string{"name = ?"}, []any{sqliteNullableString(name)})
}

func updateRustStateThreadGitInfo(codexHome string, threadID string, git map[string]string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return err
	}
	dbPath := config.StateDBPath()
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := config.OpenReadWrite(nil, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return updateRustStateThreadRow(
		db,
		threadID,
		[]string{"git_sha = ?", "git_branch = ?", "git_origin_url = ?"},
		[]any{
			sqliteNullableString(git["sha"]),
			sqliteNullableString(git["branch"]),
			sqliteNullableString(firstNonEmpty(git["origin_url"], git["originUrl"])),
		},
	)
}

// updateRustStateThreadRow applies one metadata patch. On the full schema the
// same statement advances recency, so a rename or memory/Git update refreshes
// recency-ordered listings atomically (Rust #59016). Minimal legacy schemas
// without recency columns keep the old statement.
func updateRustStateThreadRow(db *sql.DB, threadID string, assignments []string, args []any) error {
	if db == nil {
		return nil
	}
	if sqliteThreadsHaveRecencyColumns(db) {
		now := time.Now().UTC()
		assignments = append(assignments,
			"recency_at_ms = MAX(COALESCE(recency_at_ms, 0) + 1, ?)",
			"recency_at = MAX(COALESCE(recency_at, 0) + 1, ?)",
		)
		args = append(args, now.UnixMilli(), now.Unix())
	}
	args = append(args, threadID)
	_, err := db.Exec("UPDATE threads SET "+strings.Join(assignments, ", ")+" WHERE id = ?", args...)
	if isSQLiteMissingSchemaError(err) {
		return nil
	}
	return err
}

func sqliteThreadsHaveRecencyColumns(db *sql.DB) bool {
	rows, err := db.Query(`PRAGMA table_info(threads)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	hasAt, hasMS := false, false
	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		switch name {
		case "recency_at":
			hasAt = true
		case "recency_at_ms":
			hasMS = true
		}
	}
	return hasAt && hasMS
}

func clearRustMemoriesSQLiteData(codexHome string) error {
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return err
	}
	// Reset covers every existing memory version (Rust #43797).
	for _, dbPath := range []string{config.MemoriesDBPath(), config.MemoriesV2DBPath()} {
		if err := clearRustMemoriesDBFile(config, dbPath); err != nil {
			return err
		}
	}
	return nil
}

func clearRustMemoriesDBFile(config state.SqliteConfig, dbPath string) error {
	if !regularFileExists(dbPath) {
		return nil
	}
	db, err := config.OpenReadWrite(nil, dbPath)
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
