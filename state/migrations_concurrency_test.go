package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent processes starting against one fresh database must serialize the
// schema check and migrations instead of racing CREATE/ALTER statements
// (Rust #59015).
func TestMigrateRuntimeDBConcurrentStartupSharesOneSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "logs.sqlite")
	const starters = 2
	dbs := make([]*sql.DB, starters)
	for i := range dbs {
		db, err := OpenSQLite(ctx, path)
		if err != nil {
			t.Fatalf("OpenSQLite(%d) error = %v", i, err)
		}
		db.SetMaxOpenConns(1)
		dbs[i] = db
		defer db.Close()
	}
	errs := make([]error, starters)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range dbs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = migrateRuntimeDB(ctx, dbs[index], RuntimeDBLogs)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d migrate error = %v", i, err)
		}
	}
	migrations, err := loadRuntimeMigrations(RuntimeDBLogs)
	if err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := dbs[0].QueryRowContext(ctx, `SELECT COUNT(*) FROM _sqlx_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied != len(migrations) {
		t.Fatalf("applied migrations = %d, want %d", applied, len(migrations))
	}
	// Reopening a current schema is a read under the same transaction and must
	// stay a no-op.
	if err := migrateRuntimeDB(ctx, dbs[0], RuntimeDBLogs); err != nil {
		t.Fatalf("second migrate error = %v", err)
	}
}
