package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// diffProfileModel builds an attached local-daemon session whose workspace is
// the single root the diff working directory names (Rust #63400
// diff_permission_profile).
func diffProfileModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.ThreadID = "thread-diff"
	state.Sandbox = "trusted"
	model := NewModel(state, Options{Width: 100, Height: 24, LocalDaemonSession: true, DiffPermissionProfileAllowed: true})
	model.sessionCWD = "/workspace"
	model.sessionWorkspaceRoots = []string{"/workspace"}
	return model
}

// TestDiffPermissionProfileRequiresMatchingLocalDaemonWorkspaceLikeRust covers
// the selection rules: a named profile only travels when the local daemon can
// resolve it for a workspace the command cwd fully represents.
func TestDiffPermissionProfileRequiresMatchingLocalDaemonWorkspaceLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Model)
		want   string
	}{
		{"single matching root", func(*Model) {}, "trusted"},
		{"extra workspace root", func(m *Model) { m.sessionWorkspaceRoots = []string{"/workspace", "/workspace/lib"} }, ""},
		{"root is another directory", func(m *Model) { m.sessionWorkspaceRoots = []string{"/elsewhere"} }, ""},
		{"remote app server", func(m *Model) { m.localDaemonSession = false }, ""},
		{"launch permission overrides", func(m *Model) { m.diffPermissionProfileAllowed = false }, ""},
		{"read-only history", func(m *Model) { m.readOnlyThread = true }, ""},
		{"archived view", func(m *Model) { m.archivedThread = true }, ""},
		{"pending profile", func(m *Model) { m.pendingServerProfile = "trusted" }, ""},
		{"built-in profile", func(m *Model) { m.State.Sandbox = ":workspace" }, ""},
		{"no profile", func(m *Model) { m.State.Sandbox = "" }, ""},
		{"attached thread without roots", func(m *Model) { m.sessionWorkspaceRoots = nil }, ""},
		{"startup workspace before a thread", func(m *Model) {
			m.State.ThreadID = ""
			m.sessionWorkspaceRoots = nil
		}, "trusted"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := diffProfileModel(t)
			testCase.mutate(model)
			if got := model.diffPermissionProfileID(); got != testCase.want {
				t.Fatalf("profile = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestDiffCommandPassesSessionProfileLikeRust: the async diff request carries
// the selected profile to the reader.
func TestDiffCommandPassesSessionProfileLikeRust(t *testing.T) {
	model := diffProfileModel(t)
	received := ""
	model.onReadGitDiff = func(cwd string, permissionProfile string) (string, bool, error) {
		received = permissionProfile
		return "diff --git a/a b/a\n", true, nil
	}
	cmd := model.applyDiffCommand()
	if cmd == nil {
		t.Fatal("diff command missing")
	}
	runTeaCmd(t, model, cmd)
	if received != "trusted" {
		t.Fatalf("reader profile = %q, want trusted", received)
	}
	if model.overlay == nil {
		t.Fatal("/diff did not open its pager")
	}
}
