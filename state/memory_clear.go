package state

import (
	"context"
	"database/sql"
	"os"
)

// ClearMemoryDataInSqliteHome clears the memory rows of every memories
// database that already exists, mirroring Rust
// StateRuntime::clear_memory_data_in_sqlite_home: a database that was never
// created is left alone, and the boolean reports whether any was cleared.
func ClearMemoryDataInSqliteHome(ctx context.Context, config SqliteConfig) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	targets := []struct {
		path string
		open func(context.Context) (*sql.DB, error)
	}{
		{config.MemoriesDBPath(), config.OpenMemoriesDB},
		{config.MemoriesV2DBPath(), config.OpenMemoriesV2DB},
	}
	cleared := false
	for _, target := range targets {
		if _, err := os.Stat(target.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cleared, err
		}
		database, err := target.open(ctx)
		if err != nil {
			return cleared, err
		}
		clearErr := clearMemoryDataInPool(ctx, database)
		closeErr := closeSQLiteDB(database)
		if clearErr != nil {
			return cleared, clearErr
		}
		if closeErr != nil {
			return cleared, closeErr
		}
		cleared = true
	}
	return cleared, nil
}
