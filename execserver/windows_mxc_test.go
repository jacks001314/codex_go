//go:build windows

package execserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/sandbox/mxc"
)

// Mirrors Rust's prepare_exec_request MXC gate (#45524, updated by #46554):
// an MXC launch accepts TTY and managed networking, still rejects a custom
// argv0, and requires native MXC on the executor. Go now runs MXC through
// sandbox/mxc's PSEC launcher, so the outcome depends on the host's probe.
func TestExecServerRejectsMXCLevelLikeRust(t *testing.T) {
	mxcLevel := windowsSandboxLevelMxc
	sandboxPayload := func(level string) json.RawMessage {
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
			Argv:      []string{"cmd.exe", "/c", "exit", "0"},
			CWD:       t.TempDir(),
			Env:       map[string]string{},
			Sandbox:   sandboxPayload(mxcLevel),
		}
	}

	available := sandbox.WindowsMXCAvailable()
	process, supported, err := startExecServerSandboxProcess(base())
	if !supported {
		t.Fatalf("supported = false, want true")
	}
	if !available {
		if err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
			t.Fatalf("unavailable host: err = %v", err)
		}
	} else {
		if err != nil {
			t.Fatalf("available host: pipe launch err = %v", err)
		}
		if process == nil {
			t.Fatal("available host: nil process")
		}
		// Drain and reap the process so the test leaves no sandboxed child.
		reapMXCProcess(t, process)
	}

	// TTY support is driven by sandbox/mxc's capability bit, so this assertion
	// moves with the implementation instead of freezing today's verdict (the
	// mis-verification trap the ledger lane flagged).
	tty := base()
	tty.TTY = true
	ttyProcess, ttySupported, ttyErr := startExecServerSandboxProcess(tty)
	if !ttySupported {
		t.Fatal("tty launch: supported = false, want true")
	}
	switch {
	case !available:
		if ttyErr == nil || !strings.Contains(ttyErr.Error(), "native MXC is unavailable on this executor") {
			t.Fatalf("unavailable host tty err = %v", ttyErr)
		}
	case !mxc.NativeTTYLaunchSupported():
		if ttyErr == nil || !strings.Contains(ttyErr.Error(), mxc.TTYUnsupportedMessage) {
			t.Fatalf("tty launch err = %v, want %q", ttyErr, mxc.TTYUnsupportedMessage)
		}
		if ttyProcess != nil {
			t.Fatal("rejected tty launch returned a process")
		}
	default:
		if ttyErr != nil {
			t.Fatalf("tty launch err = %v", ttyErr)
		}
		reapMXCProcess(t, ttyProcess)
	}
	arg0 := "custom-arg0"
	arg0Params := base()
	arg0Params.Arg0 = &arg0
	if _, _, err := startExecServerSandboxProcess(arg0Params); err == nil || !strings.Contains(err.Error(), "MXC custom argv0 is not supported") {
		t.Fatalf("arg0 launch err = %v", err)
	}

	// A proxy launch travels through the managed-network context, exactly like
	// Rust: the sandbox then denies direct egress and allows host loopback so
	// the child can reach the managed proxy. There is no MXC-specific proxy
	// rejection, and MXC's own `network_policy.proxy` form stays unused.
	proxy := base()
	proxy.NetworkProxy = &RemoteNetworkProxyLaunchConfig{Proxy: RemoteNetworkProxyConfig{Enabled: true}}
	proxy.EnforceManagedNetwork = true
	proxy.ManagedNetwork = &ManagedNetworkSandboxContext{LoopbackPorts: []uint16{43123}, AllowLocalBinding: true}
	process, supported, err = startExecServerSandboxProcess(proxy)
	if !supported {
		t.Fatal("proxied launch: supported = false, want true")
	}
	if !available {
		if err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
			t.Fatalf("unavailable host proxied err = %v", err)
		}
	} else {
		if err != nil {
			t.Fatalf("proxied launch err = %v", err)
		}
		reapMXCProcess(t, process)
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

// reapMXCProcess drains stdio and waits for a launched sandbox child so a test
// never leaks a sandboxed process.
func reapMXCProcess(t *testing.T, process *startedExecServerSandboxProcess) {
	t.Helper()
	if process == nil {
		return
	}
	if process.stdin != nil {
		_ = process.stdin.Close()
	}
	for _, reader := range process.readers {
		if reader != nil {
			_ = reader.Close()
		}
	}
	_, _ = process.wait()
	_ = process.close()
}
