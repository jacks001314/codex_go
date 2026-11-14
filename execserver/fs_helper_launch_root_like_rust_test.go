package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Rust #46112 launches the filesystem helper from the filesystem root so a
// removed policy cwd cannot block absolute-path requests.
func TestFSHelperLaunchRootLikeRust(t *testing.T) {
	type launchRootCase struct {
		name string
		cwd  string
		want string
	}
	cases := []launchRootCase{
		{"blank", "   ", ""},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			launchRootCase{"volume", `C:\Users\codex\repo`, `C:\`},
			launchRootCase{"volume with forward slashes", "C:/Users/codex/repo", `C:\`},
			launchRootCase{"unc share", `\\server\share\repo`, `\\server\share\`},
			launchRootCase{"volume-less path", "/tmp/codex/session", `\`},
		)
	} else {
		cases = append(cases,
			launchRootCase{"posix directory", "/tmp/codex/session", "/"},
			launchRootCase{"posix root", "/", "/"},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fsHelperLaunchRoot(tc.cwd); got != tc.want {
				t.Fatalf("fsHelperLaunchRoot(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

// A removed policy cwd must not become the helper launch cwd. Validate the
// actual plan without disabling the sandbox to run a restricted read.
func TestFSHelperPlanSurvivesRemovedPolicyCWDLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper policy binding")
	}
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"glob_pattern","pattern":"hidden/*.env"},"access":"deny"}]},"network":"restricted"}`)
	ctx := &FileSystemSandboxContext{Permissions: raw, CWD: selected, WorkspaceRoots: []string{selected}}
	required, profile, profileJSON, cwd, roots, err := prepareFSSandboxForOperation(ctx, MethodFSReadFile)
	if err != nil || !required {
		t.Fatalf("prepare required=%v err=%v", required, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fsSandboxRunPlan(ctx, profile, profileJSON, cwd, roots, "", executable, []string{executable})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CommandCWD != fsHelperLaunchRoot(selected) || plan.CWD != selected {
		t.Fatalf("command cwd=%q policy cwd=%q", plan.CommandCWD, plan.CWD)
	}
	var wire struct {
		FileSystem struct {
			Entries []struct {
				Path struct {
					Pattern string `json:"pattern"`
				} `json:"path"`
			} `json:"entries"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal([]byte(plan.PermissionProfileJSON), &wire); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(selected, "hidden", "*.env")
	found := false
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Pattern == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing anchored denial %q in %s", want, plan.PermissionProfileJSON)
	}
}
