package state

import (
	"bytes"
	"context"
	"crypto/sha512"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/state/*.sql migrations/logs/*.sql migrations/goals/*.sql migrations/memories/*.sql migrations/thread_history/*.sql
var embeddedRuntimeMigrations embed.FS

type sqliteMigration struct {
	version     int64
	description string
	sql         string
	checksum    [sha512.Size384]byte
	noTx        bool
}

const legacyRemoteControlTable = "_codex_go_legacy_remote_control_enrollments"

type legacyRemoteControlAdoption struct {
	pending    bool
	hasEnabled bool
}

func migrationDirectory(kind RuntimeDBKind) (string, error) {
	switch kind {
	case RuntimeDBState:
		return "migrations/state", nil
	case RuntimeDBLogs:
		return "migrations/logs", nil
	case RuntimeDBGoals:
		return "migrations/goals", nil
	case RuntimeDBMemories:
		return "migrations/memories", nil
	case RuntimeDBThreadHistory:
		return "migrations/thread_history", nil
	default:
		return "", fmt.Errorf("unknown runtime database kind %q", kind)
	}
}

func loadRuntimeMigrations(kind RuntimeDBKind) ([]sqliteMigration, error) {
	directory, err := migrationDirectory(kind)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(embeddedRuntimeMigrations, directory)
	if err != nil {
		return nil, fmt.Errorf("read %s migrations: %w", kind, err)
	}
	migrations := make([]sqliteMigration, 0, len(entries))
	seen := make(map[int64]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) != 2 {
			continue
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration filename %q: %w", entry.Name(), err)
		}
		if seen[version] {
			return nil, fmt.Errorf("duplicate %s migration version %d", kind, version)
		}
		data, err := embeddedRuntimeMigrations.ReadFile(path.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		description := strings.ReplaceAll(strings.TrimSuffix(parts[1], ".sql"), "_", " ")
		migrations = append(migrations, sqliteMigration{
			version:     version,
			description: description,
			sql:         string(data),
			checksum:    sha512.Sum384(data),
			noTx:        bytes.HasPrefix(data, []byte("-- no-transaction")),
		})
		seen[version] = true
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

func migrateRuntimeDB(ctx context.Context, db *sql.DB, kind RuntimeDBKind) error {
	if ctx == nil {
		ctx = context.Background()
	}
	migrations, err := loadRuntimeMigrations(kind)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire %s migration connection: %w", kind, err)
	}
	defer conn.Close()

	var legacyRemoteControl legacyRemoteControlAdoption
	if kind == RuntimeDBState {
		legacyRemoteControl, err = prepareLegacyRemoteControlAdoption(ctx, conn)
		if err != nil {
			return fmt.Errorf("prepare legacy Go remote control table: %w", err)
		}
		if err := repairLegacyRecencyMigrationVersion(ctx, conn, migrations); err != nil {
			return fmt.Errorf("repair legacy state migration: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS _sqlx_migrations (
    version BIGINT PRIMARY KEY,
    description TEXT NOT NULL,
    installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    checksum BLOB NOT NULL,
    execution_time BIGINT NOT NULL
);`); err != nil {
		return fmt.Errorf("create SQLx migrations table: %w", err)
	}

	var dirtyVersion int64
	err = conn.QueryRowContext(ctx, `SELECT version FROM _sqlx_migrations WHERE success = FALSE ORDER BY version LIMIT 1`).Scan(&dirtyVersion)
	if err == nil {
		return fmt.Errorf("migration %d is partially applied", dirtyVersion)
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read dirty migration: %w", err)
	}

	applied := map[int64][]byte{}
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM _sqlx_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}
	for rows.Next() {
		var version int64
		var checksum []byte
		if err := rows.Scan(&version, &checksum); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = append([]byte(nil), checksum...)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close applied migration rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}

	for _, migration := range migrations {
		if checksum, ok := applied[migration.version]; ok {
			if !bytes.Equal(checksum, migration.checksum[:]) {
				return fmt.Errorf("%s migration %d checksum mismatch", kind, migration.version)
			}
			continue
		}
		if err := applyRuntimeMigration(ctx, conn, migration); err != nil {
			return fmt.Errorf("apply %s migration %d: %w", kind, migration.version, err)
		}
	}
	if legacyRemoteControl.pending {
		if err := finishLegacyRemoteControlAdoption(ctx, conn, legacyRemoteControl.hasEnabled); err != nil {
			return fmt.Errorf("finish legacy Go remote control table adoption: %w", err)
		}
	}
	return nil
}

func prepareLegacyRemoteControlAdoption(ctx context.Context, conn *sql.Conn) (legacyRemoteControlAdoption, error) {
	legacyExists, err := sqliteTableExists(ctx, conn, legacyRemoteControlTable)
	if err != nil {
		return legacyRemoteControlAdoption{}, err
	}
	if legacyExists {
		hasEnabled, err := validateLegacyRemoteControlSchema(ctx, conn, legacyRemoteControlTable)
		return legacyRemoteControlAdoption{pending: true, hasEnabled: hasEnabled}, err
	}
	originalExists, err := sqliteTableExists(ctx, conn, "remote_control_enrollments")
	if err != nil || !originalExists {
		return legacyRemoteControlAdoption{}, err
	}
	migrationsTableExists, err := sqliteTableExists(ctx, conn, "_sqlx_migrations")
	if err != nil {
		return legacyRemoteControlAdoption{}, err
	}
	if migrationsTableExists {
		var applied int
		err := conn.QueryRowContext(ctx, `SELECT 1 FROM _sqlx_migrations WHERE version = 24`).Scan(&applied)
		if err == nil {
			return legacyRemoteControlAdoption{}, nil
		}
		if err != sql.ErrNoRows {
			return legacyRemoteControlAdoption{}, err
		}
	}
	hasEnabled, err := validateLegacyRemoteControlSchema(ctx, conn, "remote_control_enrollments")
	if err != nil {
		return legacyRemoteControlAdoption{}, err
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE remote_control_enrollments RENAME TO `+legacyRemoteControlTable); err != nil {
		return legacyRemoteControlAdoption{}, err
	}
	return legacyRemoteControlAdoption{pending: true, hasEnabled: hasEnabled}, nil
}

type sqliteColumn struct {
	name     string
	typeName string
	notNull  int
	pk       int
}

func validateLegacyRemoteControlSchema(ctx context.Context, conn *sql.Conn, table string) (bool, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	columns := map[string]sqliteColumn{}
	for rows.Next() {
		var cid int
		var column sqliteColumn
		var defaultValue any
		if err := rows.Scan(&cid, &column.name, &column.typeName, &column.notNull, &defaultValue, &column.pk); err != nil {
			return false, err
		}
		column.typeName = strings.ToUpper(strings.TrimSpace(column.typeName))
		columns[column.name] = column
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	expected := map[string]sqliteColumn{
		"websocket_url":          {typeName: "TEXT", notNull: 1, pk: 1},
		"account_id":             {typeName: "TEXT", notNull: 1, pk: 2},
		"app_server_client_name": {typeName: "TEXT", notNull: 1, pk: 3},
		"server_id":              {typeName: "TEXT", notNull: 1},
		"environment_id":         {typeName: "TEXT", notNull: 1},
		"server_name":            {typeName: "TEXT", notNull: 1},
		"updated_at":             {typeName: "INTEGER"},
	}
	hasEnabled := false
	if _, ok := columns["remote_control_enabled"]; ok {
		hasEnabled = true
		expected["remote_control_enabled"] = sqliteColumn{typeName: "INTEGER"}
	}
	if len(columns) != len(expected) {
		return false, fmt.Errorf("legacy table %s has unexpected columns", table)
	}
	for name, want := range expected {
		got, ok := columns[name]
		if !ok || got.typeName != want.typeName || got.pk != want.pk {
			return false, fmt.Errorf("legacy table %s column %s is incompatible", table, name)
		}
		if name != "updated_at" && got.notNull != want.notNull {
			return false, fmt.Errorf("legacy table %s column %s nullability is incompatible", table, name)
		}
	}
	return hasEnabled, nil
}

func finishLegacyRemoteControlAdoption(ctx context.Context, conn *sql.Conn, hasEnabled bool) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	enabledExpression := "NULL"
	if hasEnabled {
		enabledExpression = "remote_control_enabled"
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO remote_control_enrollments (
    websocket_url, account_id, app_server_client_name, server_id,
    environment_id, server_name, updated_at, remote_control_enabled
)
SELECT websocket_url, account_id, app_server_client_name, server_id,
       environment_id, server_name, COALESCE(updated_at, 0), `+enabledExpression+`
FROM `+legacyRemoteControlTable+`
WHERE TRUE
ON CONFLICT(websocket_url, account_id, app_server_client_name) DO UPDATE SET
    server_id = excluded.server_id,
    environment_id = excluded.environment_id,
    server_name = excluded.server_name,
    updated_at = excluded.updated_at,
    remote_control_enabled = excluded.remote_control_enabled`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE `+legacyRemoteControlTable); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func sqliteTableExists(ctx context.Context, conn *sql.Conn, table string) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func applyRuntimeMigration(ctx context.Context, conn *sql.Conn, migration sqliteMigration) error {
	started := time.Now()
	if migration.noTx {
		if _, err := conn.ExecContext(ctx, migration.sql); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time)
VALUES (?, ?, TRUE, ?, -1)`, migration.version, migration.description, migration.checksum[:]); err != nil {
			return err
		}
	} else {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time)
VALUES (?, ?, TRUE, ?, -1)`, migration.version, migration.description, migration.checksum[:]); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err := conn.ExecContext(ctx, `UPDATE _sqlx_migrations SET execution_time = ? WHERE version = ?`, time.Since(started).Nanoseconds(), migration.version)
	return err
}

func repairLegacyRecencyMigrationVersion(ctx context.Context, conn *sql.Conn, migrations []sqliteMigration) error {
	var tableExists int
	err := conn.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = '_sqlx_migrations'`).Scan(&tableExists)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var recency *sqliteMigration
	for i := range migrations {
		if migrations[i].version == 39 {
			recency = &migrations[i]
			break
		}
	}
	if recency == nil {
		return nil
	}
	_, err = conn.ExecContext(ctx, `
UPDATE _sqlx_migrations
SET version = ?, description = ?
WHERE version = 38
  AND checksum = ?
  AND NOT EXISTS (SELECT 1 FROM _sqlx_migrations WHERE version = ?)`,
		recency.version, recency.description, recency.checksum[:], recency.version)
	return err
}
