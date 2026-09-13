//go:build windows

package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust's prepare_exec_request MXC gate (#45176): an MXC launch accepts
// only ordinary pipe launches without managed networking, and requires native
// MXC on the executor. Go has no native MXC runner, so the availability check
// rejects the request exactly like Rust on such a host.
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
	if _, _, err := startExecServerSandboxProcess(tty); err == nil || !strings.Contains(err.Error(), "MXC currently supports ordinary pipe launches only") {
		t.Fatalf("tty launch err = %v", err)
	}
	arg0 := "custom-arg0"
	arg0Params := base()
	arg0Params.Arg0 = &arg0
	if _, _, err := startExecServerSandboxProcess(arg0Params); err == nil || !strings.Contains(err.Error(), "MXC currently supports ordinary pipe launches only") {
		t.Fatalf("arg0 launch err = %v", err)
	}

	managed := base()
	managed.EnforceManagedNetwork = true
	managed.ManagedNetwork = &ManagedNetworkSandboxContext{AllowLocalBinding: true}
	if _, _, err := startExecServerSandboxProcess(managed); err == nil || !strings.Contains(err.Error(), "MXC managed networking is not supported yet") {
		t.Fatalf("managed network launch err = %v", err)
	}
	proxy := base()
	proxy.NetworkProxy = &RemoteNetworkProxyLaunchConfig{}
	if _, _, err := startExecServerSandboxProcess(proxy); err == nil || !strings.Contains(err.Error(), "MXC managed networking is not supported yet") {
		t.Fatalf("network proxy launch err = %v", err)
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
