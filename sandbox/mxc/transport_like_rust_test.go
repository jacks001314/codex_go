package mxc

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"codex_go/sandbox"
)

const sandboxLaunchTransportPrefix = "CODEX_SANDBOX_LAUNCH_"

func setMXCTransportHostEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("SystemDrive", "C:")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	t.Setenv("SystemRoot", `C:\Windows`)
}

// Mirrors Rust #44626's large Unicode launcher roundtrip: every transport
// value stays below the Windows environment limit, host-owned startup values
// replace caller spellings, and the helper scrubs launcher-only entries.
func TestMXCLaunchTransportRoundTripsLargeUnicodeLikeRust(t *testing.T) {
	setMXCTransportHostEnvironment(t)
	cwd := t.TempDir()
	args := []string{
		"codex-windows-mxc",
		strings.Repeat("🧊", 20_000),
		`a"\b`,
	}
	env := map[string]string{
		"CUSTOM":                   "value",
		"codex_sandbox_launch_999": "spoofed",
		"systemdrive":              "caller-only",
		"LocalAppData":             "caller-only",
	}
	wrapped, err := CreateCommandArgs(args, sandbox.WorkspaceWritePermissionProfile(), cwd, nil, env)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if !reflect.DeepEqual(wrapped, []string{LauncherArg1}) {
		t.Fatalf("wrapped = %#v", wrapped)
	}
	if _, ok := env["codex_sandbox_launch_999"]; ok {
		t.Fatal("stale transport entry survived encoding")
	}
	for value := range env {
		if value := env[value]; len(utf16.Encode([]rune(value))) >= 32_767 {
			t.Fatalf("transport value exceeds the Windows environment limit: %d UTF-16 units", len(utf16.Encode([]rune(value))))
		}
	}

	request, err := DecodeCommandArgs(env)
	if err != nil {
		t.Fatalf("DecodeCommandArgs() error = %v", err)
	}
	if !reflect.DeepEqual(request.Command, args) || request.SandboxPolicyCWD != cwd || request.DefaultChildEnv {
		t.Fatalf("decoded request = %#v", request)
	}
	gotProfile, wantProfile := any(nil), any(nil)
	if err := json.Unmarshal(request.Permissions, &gotProfile); err != nil {
		t.Fatalf("decode permissions: %v", err)
	}
	encodedProfile, err := sandbox.RuntimePermissionProfileJSON(sandbox.WorkspaceWritePermissionProfile())
	if err != nil {
		t.Fatalf("encode expected permissions: %v", err)
	}
	if err := json.Unmarshal([]byte(encodedProfile), &wantProfile); err != nil {
		t.Fatalf("decode expected permissions: %v", err)
	}
	if !reflect.DeepEqual(gotProfile, wantProfile) {
		t.Fatalf("permissions = %#v, want %#v", gotProfile, wantProfile)
	}
	if env["SystemDrive"] != "C:" || env["LOCALAPPDATA"] == "caller-only" {
		t.Fatalf("host-owned startup values were not replaced: %#v", env)
	}
	delete(env, "SystemDrive")
	delete(env, "LOCALAPPDATA")
	if !reflect.DeepEqual(env, map[string]string{"CUSTOM": "value"}) {
		t.Fatalf("decoded child environment = %#v", env)
	}
}

// Mirrors Rust #44626's malformed-transport contract: every rejected payload
// is scrubbed and unrelated child variables survive.
func TestMXCLaunchTransportRejectsAndScrubsMalformedPayloadsLikeRust(t *testing.T) {
	setMXCTransportHostEnvironment(t)
	template := map[string]string{"CUSTOM": "value"}
	if _, err := CreateCommandArgs(
		[]string{"program.exe"},
		sandbox.ReadOnlyPermissionProfile(),
		t.TempDir(),
		nil,
		template,
	); err != nil {
		t.Fatalf("CreateCommandArgs() template error = %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "zero count", mutate: func(env map[string]string) { env[sandboxLaunchTransportPrefix+"COUNT"] = "0" }},
		{name: "zero length", mutate: func(env map[string]string) { env[sandboxLaunchTransportPrefix+"BYTES"] = "0" }},
		{name: "oversized chunk", mutate: func(env map[string]string) {
			env[sandboxLaunchTransportPrefix+"0"] = strings.Repeat("x", 16*1024+1)
		}},
		{name: "duplicate count", mutate: func(env map[string]string) {
			env[strings.ToLower(sandboxLaunchTransportPrefix+"COUNT")] = "1"
		}},
		{name: "missing chunk", mutate: func(env map[string]string) {
			delete(env, sandboxLaunchTransportPrefix+"0")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := cloneMXCTransportEnvironment(template)
			if _, err := CreateCommandArgs(
				[]string{"program.exe"},
				sandbox.ReadOnlyPermissionProfile(),
				t.TempDir(),
				nil,
				env,
			); err != nil {
				t.Fatalf("CreateCommandArgs() error = %v", err)
			}
			test.mutate(env)
			if _, err := DecodeCommandArgs(env); err == nil || err.Error() != "invalid MXC launcher request" {
				t.Fatalf("DecodeCommandArgs() error = %v, want invalid MXC launcher request", err)
			}
			if env["CUSTOM"] != "value" {
				t.Fatalf("unrelated child variable changed: %#v", env)
			}
			for key := range env {
				if strings.HasPrefix(strings.ToUpper(key), sandboxLaunchTransportPrefix) {
					t.Fatalf("transport key survived scrub: %q", key)
				}
			}
		})
	}
}

// Mirrors Rust #44626: an oversized payload is rejected before existing
// launcher transport entries are replaced.
func TestMXCLaunchTransportRejectsOversizedPayloadLikeRust(t *testing.T) {
	setMXCTransportHostEnvironment(t)
	env := map[string]string{
		"CUSTOM":                           "value",
		sandboxLaunchTransportPrefix + "X": "keep",
	}
	_, err := CreateCommandArgs(
		[]string{strings.Repeat("x", 16*1024*1024)},
		sandbox.ReadOnlyPermissionProfile(),
		t.TempDir(),
		nil,
		env,
	)
	if err == nil {
		t.Fatal("CreateCommandArgs() accepted an oversized payload")
	}
	if env["CUSTOM"] != "value" || env[sandboxLaunchTransportPrefix+"X"] != "keep" {
		t.Fatalf("oversized payload modified the launcher environment: %#v", env)
	}
}

// A caller that provides only launcher transport state requested the SDK's
// default child environment; decoding must clear every injected/allowed entry.
func TestMXCLaunchTransportClearsDefaultChildEnvironmentLikeRust(t *testing.T) {
	setMXCTransportHostEnvironment(t)
	env := map[string]string{sandboxLaunchTransportPrefix + "STALE": "stale"}
	if _, err := CreateCommandArgs(
		[]string{"program.exe"},
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
	if !request.DefaultChildEnv || len(env) != 0 {
		t.Fatalf("default child environment = %#v, env = %#v", request, env)
	}
}

func cloneMXCTransportEnvironment(env map[string]string) map[string]string {
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}
