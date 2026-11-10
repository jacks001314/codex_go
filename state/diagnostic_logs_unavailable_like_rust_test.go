package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #60466: a diagnostic logs database that cannot be opened must leave
// thread storage and database-only history usable, surface the original error
// from log operations, and reject thread deletion before destructive cleanup.
func TestStateRuntimeKeepsThreadStorageWhenDiagnosticLogsFailLikeRust(t *testing.T) {
	home := t.TempDir()
	blockedLogs := filepath.Join(home, "blocked-logs")
	if err := os.WriteFile(blockedLogs, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked logs path: %v", err)
	}
	config := mustSQLiteConfig(t, home).WithLocalLogsHome(blockedLogs)
	runtime, err := InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v, want a usable runtime without diagnostic logs", err)
	}
	defer func() { _ = runtime.Close() }()

	diagErr := runtime.DiagnosticLogsError()
	if diagErr == nil {
		t.Fatal("DiagnosticLogsError() = nil, want the startup failure")
	}
	if !strings.Contains(diagErr.Error(), blockedLogs) {
		t.Fatalf("DiagnosticLogsError() = %v, want the logs path %s", diagErr, blockedLogs)
	}

	// Thread storage and database-only history listing keep working.
	var threads int
	if err := runtime.StateDB().QueryRowContext(context.Background(), `SELECT count(*) FROM threads`).Scan(&threads); err != nil {
		t.Fatalf("list threads from the state database: %v", err)
	}

	// Log operations report the original failure rather than a nil handle.
	if err := runtime.InsertLog(context.Background(), LogEntry{TS: 1, Level: "INFO", Target: "test"}); err == nil {
		t.Fatal("InsertLog() = nil, want the diagnostic database error")
	} else if !strings.Contains(err.Error(), blockedLogs) {
		t.Fatalf("InsertLog() error = %v, want it to carry the original cause", err)
	}
	if _, err := runtime.MaxLogID(context.Background(), LogQuery{}); err == nil {
		t.Fatal("MaxLogID() = nil error, want the diagnostic database error")
	}

	// Deletion is refused before any conversation data is touched.
	if _, err := runtime.DeleteThreadsStrict(context.Background(), []string{"thread-1"}); err == nil {
		t.Fatal("DeleteThreadsStrict() = nil error, want the diagnostic database error")
	} else if !errors.Is(err, diagErr) && !strings.Contains(err.Error(), blockedLogs) {
		t.Fatalf("DeleteThreadsStrict() error = %v, want it to wrap the diagnostic database error", err)
	}
}

// The same outage must keep a healthy runtime reporting no diagnostic error,
// so the check above is not passing for every runtime.
func TestStateRuntimeReportsNoDiagnosticLogsErrorWhenHealthy(t *testing.T) {
	home := t.TempDir()
	runtime, err := InitStateRuntime(context.Background(), mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()
	if err := runtime.DiagnosticLogsError(); err != nil {
		t.Fatalf("DiagnosticLogsError() = %v, want nil", err)
	}
	if err := runtime.InsertLog(context.Background(), LogEntry{TS: 1, Level: "INFO", Target: "test"}); err != nil {
		t.Fatalf("InsertLog() error = %v", err)
	}
}
