package mxc

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/sandbox"
)

func TestCreateAndDecodeCommandArgsLikeRust(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	t.Setenv("SystemRoot", `C:\Windows`)

	cwd := t.TempDir()
	env := map[string]string{"PATH": `C:\Windows\System32`, "KEEP": "value"}
	args, err := CreateCommandArgs(
		[]string{"cmd.exe", "/c", "echo", "hello"},
		sandbox.WorkspaceWritePermissionProfile(),
		cwd,
		&ManagedNetworkContext{LoopbackPorts: []uint16{43123}, AllowLocalBinding: true},
		env,
	)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if !reflect.DeepEqual(args, []string{LauncherArg1}) {
		t.Fatalf("args = %#v", args)
	}
	if env["SystemDrive"] != "C:" || env["LOCALAPPDATA"] == "" {
		t.Fatalf("host-owned environment missing: %#v", env)
	}
	request, err := DecodeCommandArgs(env)
	if err != nil {
		t.Fatalf("DecodeCommandArgs() error = %v", err)
	}
	if request.SandboxPolicyCWD != cwd || !reflect.DeepEqual(request.Command, []string{"cmd.exe", "/c", "echo", "hello"}) {
		t.Fatalf("decoded request = %#v", request)
	}
	if request.ManagedNetwork == nil || !reflect.DeepEqual(request.ManagedNetwork.LoopbackPorts, []uint16{43123}) ||
		!request.ManagedNetwork.AllowLocalBinding {
		t.Fatalf("managed network = %#v", request.ManagedNetwork)
	}
	var profile map[string]any
	if err := json.Unmarshal(request.Permissions, &profile); err != nil || len(profile) == 0 {
		t.Fatalf("permissions = %s, %v", request.Permissions, err)
	}
	if env["KEEP"] != "value" {
		t.Fatalf("explicit child environment was cleared: %#v", env)
	}
}

func TestCreateCommandArgsRejectsManagedNetworkBoundariesLikeRust(t *testing.T) {
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	t.Setenv("SystemRoot", `C:\Windows`)
	for _, network := range []*ManagedNetworkContext{
		{LoopbackPorts: nil, AllowLocalBinding: true},
		{LoopbackPorts: []uint16{0}, AllowLocalBinding: true},
		{LoopbackPorts: []uint16{1234}},
	} {
		if _, err := CreateCommandArgs(
			[]string{"cmd.exe"},
			sandbox.ReadOnlyPermissionProfile(),
			t.TempDir(),
			network,
			map[string]string{},
		); err == nil {
			t.Fatalf("CreateCommandArgs(%#v) accepted an invalid managed network", network)
		}
	}
}
