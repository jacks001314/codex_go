package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
	"codex_go/turn"
)

// Rust #61134: a legacy sandbox policy admitted together with a working
// directory change is converted against the new directory, clears the named
// identity, keeps ordinary files in the new workspace writable, and refuses
// writes to git metadata reached through the .git pointer file.
func TestLegacyPermissionCWDChangeProtectsGitMetadataLikeRust(t *testing.T) {
	threadRouter, record, _ := newPersistedResumeSettingsFixture(t)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(t.TempDir()),
	})
	defer router.Close()

	nextCWD := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "git-metadata")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nextCWD, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	legacy := string(sandbox.SandboxWorkspaceWrite)
	_, err := router.commitThreadSettingsUpdate(&SettingsUpdateParams{
		ThreadID: string(record.ID), CWD: &nextCWD, SandboxPolicy: &legacy,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	settings := router.threadSettingsForTurn(string(record.ID))
	if settings.ActivePermissionProfile != nil {
		t.Fatalf("legacy admission kept the active profile identity: %q", *settings.ActivePermissionProfile)
	}
	if len(settings.normalizedPermissionProfile) == 0 {
		t.Fatal("legacy admission did not install a canonical permission profile")
	}

	params := &turn.TurnStartParams{ThreadID: string(record.ID)}
	if err := router.prepareTurnStartParams(params); err != nil {
		t.Fatal(err)
	}
	if !sameAppPath(params.CWD, nextCWD) {
		t.Fatalf("next turn cwd = %q, want %q", params.CWD, nextCWD)
	}
	cfg, err := router.effectiveConfigForTurn(params)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := turnSandboxPermissionProfile(cfg, params.CWD, params)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Profile == nil {
		t.Fatal("no permission profile resolved for the next turn")
	}
	enforced, err := sandbox.ResolveFileSystemPermissionsForEnforcement(resolved.Profile, nextCWD, sandboxpath.ProjectionNative)
	if err != nil {
		t.Fatal(err)
	}
	writable := func(path string) bool {
		if enforced.HasFullDiskWriteAccess {
			return true
		}
		for _, root := range enforced.WritableRoots {
			if root.IsPathWritable(path) {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{filepath.Join(nextCWD, "ordinary-file"), true},
		{filepath.Join(gitDir, "config"), false},
		{path: filepath.Join(nextCWD, ".git"), want: false},
	} {
		if got := writable(tc.path); got != tc.want {
			t.Errorf("write(%q) = %v, want %v; roots=%+v", tc.path, got, tc.want, enforced.WritableRoots)
		}
	}
}
