package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust startup_reports_shared_and_fallback_recovery
// (codex-rs/app-server/tests/suite/v2/sqlite_recovery.rs, upstream 3620b2caf8 /
// #49701): the logs database recovers inside the pool opener, an unreadable
// goals header forces the outer startup fallback, and the restored initialize
// warning lists every preserved backup location.
func TestStartupReportsSharedAndFallbackRecoveryLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome: %v", err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	writeCorruptRuntimeDB(t, config.LogsDBPath())
	// The unreadable goals header is not a SQLite database, so the opener fails
	// and the startup fallback backs it up before retrying.
	if err := os.WriteFile(config.GoalsDBPath(), []byte("damaged goals database"), 0o600); err != nil {
		t.Fatalf("damage goals database: %v", err)
	}

	prepared, owned, err := prepareSharedStateRuntime(ctx, home, nil)
	if err != nil {
		t.Fatalf("prepareSharedStateRuntime: %v", err)
	}
	if owned == nil {
		t.Fatal("expected the shared startup path to own a state runtime")
	}
	defer owned.Close()

	backups, err := os.ReadDir(filepath.Join(home, "db-backups"))
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("backup folders = %d, want 2", len(backups))
	}
	notice := prepared.StateDBRecoveryNotice
	if notice == nil {
		t.Fatal("startup did not report the rebuilt databases")
	}
	if notice.Summary != stateDatabaseRecoveryWarningSummary {
		t.Fatalf("warning summary = %q, want %q", notice.Summary, stateDatabaseRecoveryWarningSummary)
	}
	if notice.Details == nil {
		t.Fatal("recovery warning has no details")
	}
	details := *notice.Details
	if !strings.Contains(details, "Some database-only metadata may be unavailable") {
		t.Fatalf("details missing the upstream guidance: %q", details)
	}
	for _, backup := range backups {
		folder := filepath.Join(home, "db-backups", backup.Name())
		if !strings.Contains(details, folder) {
			t.Fatalf("details %q missing backup folder %q", details, folder)
		}
	}
	for _, path := range []string{config.LogsDBPath(), config.GoalsDBPath()} {
		if !strings.Contains(details, path) {
			t.Fatalf("details %q missing recovered database %q", details, path)
		}
	}

	// The notice reaches initialize as a single config warning; the retry-stage
	// messages stay on the logger (Rust #49701 warns per event, but the Go
	// warning surface emits one notice per initialization).
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, prepared)
	defer router.Close()
	recoveryWarnings := 0
	for _, warning := range router.configWarningsForInitialize() {
		if warning.Summary == stateDatabaseRecoveryWarningSummary {
			recoveryWarnings++
		}
		if strings.Contains(warning.Summary, "appears damaged") {
			t.Fatalf("retry-stage warning leaked into the config warning surface: %q", warning.Summary)
		}
	}
	if recoveryWarnings != 1 {
		t.Fatalf("recovery config warnings = %d, want 1", recoveryWarnings)
	}
}
