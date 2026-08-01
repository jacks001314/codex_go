package state

import (
	"context"
	"database/sql"
	"path/filepath"
)

func ThreadHistoryDBPath(sqliteHome string) string {
	return filepath.Join(sqliteHome, ThreadHistorySQLiteFilename)
}

// OpenThreadHistoryDB lazily opens the rebuildable paginated-history database.
func OpenThreadHistoryDB(ctx context.Context, sqliteHome string) (*sql.DB, error) {
	config, err := NewSqliteConfig(sqliteHome)
	if err != nil {
		return nil, err
	}
	return config.OpenThreadHistoryDB(ctx)
}

func migrateThreadHistory(ctx context.Context, db *sql.DB) error {
	return migrateRuntimeDB(ctx, db, RuntimeDBThreadHistory)
}
