package app

import (
	"path/filepath"
	"runtime"

	"codex_go/state"
)

// nfsMigrationWarning is the Rust TUI startup warning verbatim (Rust #57226).
const nfsMigrationWarning = "Some Codex SQLite files on NFS have permissions other than 0600. Migration preserves these permissions and requires your confirmation. Before running `codex migrate-nfs-db`, stop Codex on every host that uses these databases."

// nfsMigrationStartupWarning mirrors Rust's TUI startup_warnings::
// warn_about_nfs_migration: automatic NFS detection only runs on Linux, and
// only for local workspaces (the remote TUI never inspects the local state DB).
func nfsMigrationStartupWarning(codexHome string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	sqlite, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return ""
	}
	isNFS, err := sqlite.IsNFS()
	if err != nil || !isNFS {
		return ""
	}
	return nfsMigrationWarningForDatabases(
		sqlite.Home(),
		sqlite.RuntimeDBPaths(),
		state.NFSDatabaseNeedsPermissionConfirmation,
		state.NFSDatabaseNeedsMigration,
	)
}

// nfsMigrationWarningForDatabases mirrors the Rust predicate: a runtime
// database directly inside the SQLite home raises the warning once it needs
// both a permission confirmation and journal migration. Rust checks the file
// mode first and only then reads the database header, and any inspection error
// suppresses the warning entirely.
func nfsMigrationWarningForDatabases(
	home string,
	databases []state.RuntimeDBPath,
	needsPermissionConfirmation func(string) (bool, error),
	needsMigration func(string) (bool, error),
) string {
	home = filepath.Clean(home)
	for _, database := range databases {
		if filepath.Clean(filepath.Dir(database.Path)) != home {
			continue
		}
		needsPermission, err := needsPermissionConfirmation(database.Path)
		if err != nil {
			return ""
		}
		if !needsPermission {
			continue
		}
		needs, err := needsMigration(database.Path)
		if err != nil {
			return ""
		}
		if needs {
			return nfsMigrationWarning
		}
	}
	return ""
}
