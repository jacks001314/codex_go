//go:build windows

package mxc

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func testHostEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	t.Setenv("SystemRoot", `C:\Windows`)
}

// Mirrors Rust's wrapper regression (#44903): exact argument preservation,
// Windows quoting, separate policy and command working directories, and
// explicit environment forwarding.
func TestBuildRequestPreservesExactArgvAndSeparateCommandCWDLikeRust(t *testing.T) {
	testHostEnvironment(t)

	root := t.TempDir()
	commandCWD := filepath.Join(root, "command-cwd")
	argv := []string{
		`C:\Program Files\tool.exe`,
		"--permissions",
		"",
		`quotes" and slash\`,
	}
	env := map[string]string{}
	wrapped, err := CreateCommandArgs(
		append([]string(nil), argv...),
		sandbox.ReadOnlyPermissionProfile(),
		root,
		nil,
		env,
	)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if !reflect.DeepEqual(wrapped, []string{LauncherArg1}) {
		t.Fatalf("wrapped = %#v", wrapped)
	}
	request, err := DecodeCommandArgs(env)
	if err != nil {
		t.Fatalf("DecodeCommandArgs() error = %v", err)
	}
	if request.SandboxPolicyCWD != root || !reflect.DeepEqual(request.Command, argv) {
		t.Fatalf("decoded request = %#v", request)
	}

	built, err := BuildRequest(request, commandCWD, map[string]string{"CUSTOM": "value"})
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	wantScript := `"C:\Program Files\tool.exe" --permissions "" "quotes\" and slash\\"`
	if built.ScriptCode != wantScript {
		t.Fatalf("ScriptCode = %q, want %q", built.ScriptCode, wantScript)
	}
	if built.WorkingDirectory != commandCWD {
		t.Fatalf("WorkingDirectory = %q, want %q", built.WorkingDirectory, commandCWD)
	}
	if !reflect.DeepEqual(built.Env, []string{"CUSTOM=value"}) {
		t.Fatalf("Env = %#v", built.Env)
	}
}

// PowerShell needs Win32k calls and desktop handles during DLL startup; the
// clipboard, input-injection, and system-control restrictions stay in place.
func TestBuildRequestEnablesDesktopUIWithoutDisablingRestrictionsLikeRust(t *testing.T) {
	testHostEnvironment(t)

	env := map[string]string{}
	if _, err := CreateCommandArgs(
		[]string{"powershell.exe", "-NoProfile"},
		sandbox.ReadOnlyPermissionProfile(),
		t.TempDir(),
		nil,
		env,
	); err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	request, err := DecodeCommandArgs(env)
	if err != nil {
		t.Fatalf("DecodeCommandArgs() error = %v", err)
	}
	built, err := BuildRequest(request, t.TempDir(), map[string]string{})
	if err != nil {
		t.Fatalf("BuildRequest() error = %v", err)
	}
	if !reflect.DeepEqual(built.Policy.Capabilities, []string{"registryRead"}) {
		t.Fatalf("Capabilities = %#v", built.Policy.Capabilities)
	}
	if built.Policy.UI.Disable {
		t.Fatal("UI policy disabled: desktop handles would be unavailable")
	}
	if built.Policy.BaseProcessUI.Isolation != "desktop" {
		t.Fatalf("BaseProcessUI.Isolation = %q, want desktop", built.Policy.BaseProcessUI.Isolation)
	}
	if !built.Policy.NetworkSpecified || !built.Policy.NetworkModeSpecified {
		t.Fatalf("network policy not marked specified: %#v", built.Policy)
	}
	if built.Env != nil {
		t.Fatalf("Env = %#v, want nil for an inherited-empty child environment", built.Env)
	}
}

func TestBuildRequestRejectsUnusableRequestsLikeRust(t *testing.T) {
	testHostEnvironment(t)

	policyCWD := t.TempDir()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(sandbox.ReadOnlyPermissionProfile())
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	permissions := json.RawMessage(profileJSON)

	if _, err := BuildRequest(
		&Request{Permissions: permissions, SandboxPolicyCWD: policyCWD},
		policyCWD,
		map[string]string{},
	); err == nil || err.Error() != "MXC command must not be empty" {
		t.Fatalf("empty command err = %v", err)
	}
	if _, err := BuildRequest(
		&Request{Permissions: permissions, SandboxPolicyCWD: "relative", Command: []string{"cmd.exe"}},
		policyCWD,
		map[string]string{},
	); err == nil || !strings.Contains(err.Error(), "absolute policy working directory") {
		t.Fatalf("relative policy cwd err = %v", err)
	}
	relative, err := filepath.Rel(policyCWD, filepath.Join(policyCWD, "child"))
	if err != nil {
		t.Fatalf("Rel() error = %v", err)
	}
	if _, err := BuildRequest(
		&Request{Permissions: permissions, SandboxPolicyCWD: policyCWD, Command: []string{"cmd.exe"}},
		relative,
		map[string]string{},
	); err == nil || !strings.Contains(err.Error(), "absolute command working directory") {
		t.Fatalf("relative command cwd err = %v", err)
	}
}
