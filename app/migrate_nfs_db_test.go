package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/state"
)

// Rust #57357: an existing database directory with no databases reports
// "No databases found." instead of a successful migration.
func TestMigrateNFSDBReportsNoDatabasesLikeRust(t *testing.T) {
	if !state.NFSSQLiteMigrationSupported() {
		t.Skip("NFS database migration requires Unix filesystem locking")
	}
	home := t.TempDir()
	t.Setenv("GCODE_HOME", home)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("CODEX_SQLITE_HOME", "")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout, stderr strings.Builder
	err := runMigrateNFSDB(
		context.Background(),
		cli.MigrateNFSDBOptions{StagingDir: t.TempDir()},
		cli.RootOptions{},
		strings.NewReader("y\n"),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runMigrateNFSDB() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "No databases found." {
		t.Fatalf("stdout = %q, want the no-databases message", stdout.String())
	}
}
