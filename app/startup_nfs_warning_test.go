package app

import (
	"errors"
	"path/filepath"
	"testing"

	"codex_go/state"
)

// TestNFSMigrationWarningForDatabasesLikeRust mirrors Rust #57226: the warning
// only fires for a runtime database inside the SQLite home that needs both a
// permission confirmation and journal migration.
func TestNFSMigrationWarningForDatabasesLikeRust(t *testing.T) {
	home := t.TempDir()
	inside := state.RuntimeDBPath{Path: filepath.Join(home, "state_5.sqlite")}
	outside := state.RuntimeDBPath{Path: filepath.Join(t.TempDir(), "state_5.sqlite")}

	needsPermission := func(string) (bool, error) { return false, nil }
	needsMigration := func(string) (bool, error) { return true, nil }
	if got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{inside}, needsPermission, needsMigration); got != "" {
		t.Fatalf("permission-clean warning = %q", got)
	}
	if got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{outside}, func(string) (bool, error) { return true, nil }, needsMigration); got != "" {
		t.Fatalf("outside-home warning = %q", got)
	}

	permissionCalls := 0
	migrationCalls := 0
	got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{inside},
		func(string) (bool, error) { permissionCalls++; return true, nil },
		func(string) (bool, error) { migrationCalls++; return true, nil })
	if got != nfsMigrationWarning {
		t.Fatalf("warning = %q", got)
	}
	if permissionCalls != 1 || migrationCalls != 1 {
		t.Fatalf("permission calls = %d, migration calls = %d", permissionCalls, migrationCalls)
	}

	migrationCalls = 0
	if got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{inside},
		func(string) (bool, error) { return false, nil },
		func(string) (bool, error) { migrationCalls++; return true, nil }); got != "" {
		t.Fatalf("permission-skip warning = %q", got)
	}
	if migrationCalls != 0 {
		t.Fatalf("migration inspected %d times without permission confirmation", migrationCalls)
	}
}

// TestNFSMigrationWarningSuppressedByInspectionError mirrors Rust's `?`: any
// inspection failure suppresses the whole warning instead of reporting a
// partially inspected database.
func TestNFSMigrationWarningSuppressedByInspectionError(t *testing.T) {
	home := t.TempDir()
	inside := state.RuntimeDBPath{Path: filepath.Join(home, "state_5.sqlite")}
	boom := errors.New("boom")

	if got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{inside},
		func(string) (bool, error) { return false, boom },
		func(string) (bool, error) { return true, nil }); got != "" {
		t.Fatalf("permission error warning = %q", got)
	}
	if got := nfsMigrationWarningForDatabases(home, []state.RuntimeDBPath{inside},
		func(string) (bool, error) { return true, nil },
		func(string) (bool, error) { return false, boom }); got != "" {
		t.Fatalf("migration error warning = %q", got)
	}
}

// TestInteractiveStartupConfigWarningsIncludesNFSWarningLikeRust pins the
// local TUI wiring: the NFS warning joins the coalesced startup warnings list
// that the model renders, while the remote TUI keeps its own warning source.
func TestInteractiveStartupConfigWarningsIncludesNFSWarningLikeRust(t *testing.T) {
	warnings := interactiveStartupConfigWarningsWithNFS(t.TempDir(), "", "ansi", func(string) string {
		return nfsMigrationWarning
	})
	if len(warnings) != 1 || warnings[0] != nfsMigrationWarning {
		t.Fatalf("startup warnings = %#v", warnings)
	}
}
