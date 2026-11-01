package execserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust parse_snapshot: the replay renders the captured exports through
// params.env_policy with inherit forced to all, so the launch policy only
// narrows what the shell exported. A launch that inherits nothing still keeps
// the captured startup state; excluded names and policy-set originals never
// reach the replay script.
func TestShellSnapshotRenderPolicyNarrowsCapturedExportsLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return []byte(strings.Join([]string{
			"# Snapshot file\n",
			"",
			"KEPT", "declare -x KEPT='kept-sentinel'\n",
			"FILTERED", "declare -x FILTERED='filtered-sentinel'\n",
			"SECRET_SET", "declare -x SECRET_SET='original-sentinel'\n",
			"OTHER", "declare -x OTHER='other-sentinel'\n",
			"", "",
		}, "\x00")), nil
	}
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := &ExecParams{
		ProcessID: "snapshot-policy-process",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-policy",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"PATH": "/bin"},
		EnvPolicy: &ExecEnvPolicy{
			Inherit: "none",
			Exclude: []string{"FILTERED"},
			Set:     map[string]string{"SECRET_SET": "configured-value"},
		},
	}
	rewritten, reader, err := manager.rewrite(context.Background(), params, params.Env, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := shellSnapshotReplayState(t, rewritten, reader)
	if !strings.Contains(state, "kept-sentinel") || !strings.Contains(state, "other-sentinel") {
		t.Fatalf("the launch policy dropped a captured export it does not exclude: %q", state)
	}
	for _, sentinel := range []string{"filtered-sentinel", "original-sentinel"} {
		if strings.Contains(state, sentinel) {
			t.Fatalf("the replay script retains %s: %q", sentinel, state)
		}
	}
	for _, name := range []string{"FILTERED", "SECRET_SET"} {
		if strings.Contains(state, name) {
			t.Fatalf("the replay script declares the filtered or overridden export %s: %q", name, state)
		}
	}
}

func TestShellSnapshotRenderPolicyIncludeOnlyLikeRust(t *testing.T) {
	policy := shellSnapshotRenderPolicy(&ExecParams{EnvPolicy: &ExecEnvPolicy{
		Inherit:     "none",
		IncludeOnly: []string{"KEPT", "PATH*"},
	}})
	if policy == nil {
		t.Fatal("shellSnapshotRenderPolicy() = nil")
	}
	if policy.Inherit != "all" {
		t.Fatalf("render inherit = %q, want all (Rust parse_snapshot override)", policy.Inherit)
	}
	if len(policy.IncludeOnly) != 2 || policy.IncludeOnly[0].Value != "KEPT" || policy.IncludeOnly[1].Value != "PATH*" {
		t.Fatalf("render include_only = %#v", policy.IncludeOnly)
	}
	if len(policy.Exclude) != 0 {
		t.Fatalf("render exclude = %#v, want none", policy.Exclude)
	}
	if shellSnapshotRenderPolicy(&ExecParams{}) != nil {
		t.Fatal("a launch without an environment policy must keep the unrestricted render")
	}
}
