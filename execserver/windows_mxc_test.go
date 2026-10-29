//go:build windows

package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust's prepare_exec_request MXC gate (#45524, updated by #46554): an
// MXC launch accepts TTY and managed networking, still rejects a custom argv0,
// and requires native MXC on the executor. Go has no native MXC runner, so the
// availability check rejects every remaining request exactly like Rust on such
// a host.
func TestExecServerRejectsMXCLevelLikeRust(t *testing.T) {
	mxcLevel := windowsSandboxLevelMxc
	sandbox := func(level string) json.RawMessage {
		payload, err := json.Marshal(FileSystemSandboxContext{
			Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
			WindowsSandboxLevel: level,
		})
		if err != nil {
			t.Fatalf("Marshal(sandbox context) error = %v", err)
		}
		return payload
	}
	base := func() *ExecParams {
		return &ExecParams{
			ProcessID: "mxc-1",
			Argv:      []string{"echo", "hi"},
			CWD:       t.TempDir(),
			Env:       map[string]string{},
			Sandbox:   sandbox(mxcLevel),
		}
	}

	_, supported, err := startExecServerSandboxProcess(base())
	if !supported || err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("pipe launch: supported=%v err=%v", supported, err)
	}

	tty := base()
	tty.TTY = true
	if _, _, err := startExecServerSandboxProcess(tty); err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("tty launch err = %v", err)
	}
	arg0 := "custom-arg0"
	arg0Params := base()
	arg0Params.Arg0 = &arg0
	if _, _, err := startExecServerSandboxProcess(arg0Params); err == nil || !strings.Contains(err.Error(), "MXC custom argv0 is not supported") {
		t.Fatalf("arg0 launch err = %v", err)
	}

	managed := base()
	managed.EnforceManagedNetwork = true
	managed.ManagedNetwork = &ManagedNetworkSandboxContext{LoopbackPorts: []uint16{43123}, AllowLocalBinding: true}
	if _, _, err := startExecServerSandboxProcess(managed); err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("managed network launch err = %v", err)
	}
	// Rust validates managed-network details only after the native-availability
	// check, so an unconfigured network also reports the fail-closed message.
	invalidManaged := base()
	invalidManaged.EnforceManagedNetwork = true
	invalidManaged.ManagedNetwork = &ManagedNetworkSandboxContext{AllowLocalBinding: true}
	if _, _, err := startExecServerSandboxProcess(invalidManaged); err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("invalid managed network launch err = %v", err)
	}
	proxy := base()
	proxy.NetworkProxy = &RemoteNetworkProxyLaunchConfig{}
	if _, _, err := startExecServerSandboxProcess(proxy); err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("network proxy launch err = %v", err)
	}
}

// Mirrors Rust's EnvironmentInfo::local windows_mxc capability (#45524): Rust
// reports native MXC availability on Windows, and Go always reports false
// because `sandbox/mxc` has no native runner. The field is always serialized,
// so the wire shape matches Rust even when the value is false.
func TestLocalEnvironmentReportsWindowsMxcCapabilityLikeRust(t *testing.T) {
	info := localEnvironmentInfo()
	if info.Capabilities.WindowsMxc {
		t.Fatal("local windowsMxc capability = true, want false without a native MXC runner")
	}
	encoded, err := json.Marshal(info.Capabilities)
	if err != nil {
		t.Fatalf("Marshal(capabilities) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"windowsMxc":false`) {
		t.Fatalf("capabilities wire form = %s", encoded)
	}
}

// Mirrors Rust's kebab-case WindowsSandboxLevel deserialization: an unknown
// level is an invalid-params error rather than a silent fallback.
func TestExecServerRejectsUnknownWindowsSandboxLevelLikeRust(t *testing.T) {
	payload, err := json.Marshal(FileSystemSandboxContext{
		Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
		WindowsSandboxLevel: "bogus",
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	_, _, err = startExecServerSandboxProcess(&ExecParams{
		ProcessID: "level-1",
		Argv:      []string{"echo", "hi"},
		CWD:       t.TempDir(),
		Env:       map[string]string{},
		Sandbox:   payload,
	})
	want := "invalid sandbox context: unknown variant `bogus`, expected one of `disabled`, `restricted-token`, `elevated`, `mxc`"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	for _, level := range []string{"", windowsSandboxLevelDisabled, windowsSandboxLevelRestrictedToken, windowsSandboxLevelElevated} {
		if _, err := parseWindowsSandboxLevelValue(level); err != nil {
			t.Fatalf("parseWindowsSandboxLevelValue(%q) error = %v", level, err)
		}
	}
}
