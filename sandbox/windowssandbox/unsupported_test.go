package windowssandbox

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestUnsupportedIncludesFeatureAndSentinel(t *testing.T) {
	err := Unsupported("wfp.install_wfp_filters")
	if !IsUnsupported(err) {
		t.Fatalf("Unsupported() error = %v, want unsupported sentinel", err)
	}
	if !strings.Contains(err.Error(), "wfp.install_wfp_filters") {
		t.Fatalf("Unsupported() error = %q, want feature name", err.Error())
	}
}

func TestRunWindowsSandboxWrapperDisabledRunsCommand(t *testing.T) {
	profile := WorkspaceWritePermissionProfileForTest()
	command := []string{"sh", "-c", "printf hi"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd", "/c", "echo hi"}
	}
	home := t.TempDir()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             command,
		CommandCWD:          home,
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelDisabled,
		CodexHome:           home,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	var stdout bytes.Buffer
	exitCode, err := RunWindowsSandboxWrapperExitCode(args, nil, &stdout, nil)
	if err != nil {
		t.Fatalf("RunWindowsSandboxWrapperExitCode() error = %v", err)
	}
	if exitCode != 0 || !strings.Contains(stdout.String(), "hi") {
		t.Fatalf("exit=%d stdout=%q", exitCode, stdout.String())
	}
}
