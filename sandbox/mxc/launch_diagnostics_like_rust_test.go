//go:build windows

package mxc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// Rust #62439 logs every launch phase into the sandbox runtime log, tagged with
// the launcher process id, so a slow or failed launch is attributable to one
// stage.
func TestPrepareFileSystemPolicyLogsLaunchPhasesLikeRust(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "workspace")
	temp := filepath.Join(home, "temp")
	for _, path := range []string{workspace, temp} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// InitRuntimeLog pins the first codex home it sees, so another test in this
	// package may already have claimed the log directory. Reset it first so this
	// test always reads the log file it just wrote.
	windowssandbox.CloseRuntimeLog()
	windowssandbox.InitRuntimeLog(home)
	t.Cleanup(windowssandbox.CloseRuntimeLog)
	logLaunch("phase=prepare_request")
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = append(profile.SandboxPolicy.WritableRoots, temp)
	if _, err := PrepareFileSystemPolicy(&profile, workspace, map[string]string{"TEMP": temp, "TMP": temp}, workspace); err != nil {
		t.Fatalf("PrepareFileSystemPolicy() error = %v", err)
	}

	data, err := os.ReadFile(windowssandbox.CurrentLogFilePath(home))
	if err != nil {
		t.Fatalf("read sandbox log: %v", err)
	}
	contents := string(data)
	for _, want := range []string{
		"phase=prepare_request",
		"phase=prepare_deny_parents",
		"phase=resolve_filesystem_policy",
		"phase=resolve_deny_paths",
		"phase=expand_volume_grants",
		"filesystem grants read=",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("sandbox log = %q, want %q", contents, want)
		}
	}
	if !strings.Contains(contents, "MXC pid=") {
		t.Fatalf("sandbox log = %q, want the launcher process id", contents)
	}
}

// The launcher distinguishes the filesystem helper from a regular command.
func TestLaunchKindClassifiesFilesystemHelperLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		command []string
		want    string
	}{
		{name: "command", command: []string{"cmd", "/c", "echo"}, want: "command"},
		{name: "fs_helper", command: []string{"codex", fsHelperArg}, want: "fs_helper"},
		{name: "fs_helper_not_second", command: []string{"codex", "exec", fsHelperArg}, want: "command"},
		{name: "empty", command: nil, want: "command"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := launchKind(testCase.command); got != testCase.want {
				t.Fatalf("launchKind(%v) = %q, want %q", testCase.command, got, testCase.want)
			}
		})
	}
}
