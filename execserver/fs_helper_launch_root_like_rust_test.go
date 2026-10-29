package execserver

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"codex_go/utils"
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

// TestFSHelperReadSurvivesRemovedPolicyCWDLikeRust is the Go counterpart of
// Rust's apply_patch-after-removal regression: the policy cwd is gone, yet an
// absolute-path filesystem RPC still reaches the sandboxed helper because it
// launches from the filesystem root.
func TestFSHelperReadSurvivesRemovedPolicyCWDLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the piped helper launch is covered on Windows; other hosts need the platform sandbox")
	}
	target := filepath.Join(t.TempDir(), "allowed.txt")
	if err := os.WriteFile(target, []byte("allowed"), 0o600); err != nil {
		t.Fatalf("write allowed file: %v", err)
	}
	deniedURI, err := utils.FromHostNativePath(filepath.Join(t.TempDir(), "denied"))
	if err != nil {
		t.Fatalf("denied path URI: %v", err)
	}
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatalf("create selected directory: %v", err)
	}
	oldCommand := fsHelperCommandForExecutable
	fsHelperCommandForExecutable = func(string) []string {
		return []string{os.Args[0], "-test.run=^TestExecServerFSHelperProcess$", "--", FSHelperArg1}
	}
	defer func() { fsHelperCommandForExecutable = oldCommand }()
	// Remove the policy cwd before the request: launching the helper with this
	// directory as its working directory would fail at process start.
	if err := os.Remove(selected); err != nil {
		t.Fatalf("remove selected directory: %v", err)
	}
	// The deny entry forces the sandboxed helper path. Sandbox enforcement is
	// disabled here, so this test isolates helper launch behaviour.
	permissions := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[` +
		`{"path":{"type":"path","path":"` + deniedURI.String() + `"},"access":"deny"}]},"network":"restricted"}`)
	params := &FSReadFileParams{
		Path: target,
		Sandbox: &FileSystemSandboxContext{
			Permissions:         permissions,
			CWD:                 selected,
			WindowsSandboxLevel: "disabled",
		},
	}
	response, err := readFile(params)
	if err != nil {
		t.Fatalf("readFile(sandbox, removed cwd) error = %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil || string(data) != "allowed" {
		t.Fatalf("readFile(sandbox, removed cwd) data = %q, err = %v", data, err)
	}
}
