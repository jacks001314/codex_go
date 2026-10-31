package install

import (
	"context"
	"testing"
)

// Mirrors Rust #58403 package_update_runs_outside_the_project: a package
// update must not run inside the user project, because package managers search
// parent directories for project configuration and hooks.
func TestNonWindowsUpdateCommandRunsOutsideTheProjectLikeRust(t *testing.T) {
	cmd := newNonWindowsUpdateCommand(context.Background(), "pnpm", []string{"add", "-g", NPMPackageName + "@latest"})
	if cmd.Dir != "/" {
		t.Fatalf("update command dir = %q, want /", cmd.Dir)
	}
	if cmd.Path == "" || len(cmd.Args) != 4 || cmd.Args[1] != "add" {
		t.Fatalf("update command = %#v", cmd.Args)
	}
}
