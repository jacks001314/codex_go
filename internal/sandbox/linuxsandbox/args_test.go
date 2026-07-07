package linuxsandbox

import (
	"path/filepath"
	"testing"
)

func TestCreateCommandArgsWithSandboxExeOverride(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "custom-codex-linux-sandbox")
	cwd := filepath.Join(t.TempDir(), "repo")
	args, err := CreateCommandArgsWithSandboxExe(helper, []string{"echo", "hi"}, cwd, `{"type":"disabled"}`, "", false, false)
	if err != nil {
		t.Fatalf("CreateCommandArgsWithSandboxExe() error = %v", err)
	}
	if len(args) == 0 || args[0] != filepath.Clean(helper) {
		t.Fatalf("args[0] = %q, want helper %q in %#v", args[0], filepath.Clean(helper), args)
	}
	if args[2] != filepath.Clean(cwd) || args[4] != filepath.Clean(cwd) {
		t.Fatalf("cwd args = %#v, want %q", args, filepath.Clean(cwd))
	}
}

func TestCreateCommandArgsUsesDefaultSandboxExe(t *testing.T) {
	args, err := CreateCommandArgs([]string{"echo"}, "", `{"type":"disabled"}`, "", false, false)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if len(args) == 0 || args[0] != "codex-linux-sandbox" {
		t.Fatalf("args[0] = %q in %#v", args[0], args)
	}
}

func TestCreateCommandArgsAllowsNetworkForProxy(t *testing.T) {
	args, err := CreateCommandArgs([]string{"echo"}, "", `{"type":"managed"}`, "", true, true)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if !containsArg(args, "--allow-network-for-proxy") {
		t.Fatalf("args missing managed proxy flag: %#v", args)
	}
	if containsArg(args, "--use-legacy-landlock") {
		t.Fatalf("args should not combine legacy landlock with managed proxy routing: %#v", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
