//go:build darwin

package execserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestPrepareExecProcessUsesPreparedUnixSocketPolicyInSeatbeltLikeRust mirrors
// Rust #45548's macOS launch wiring: prepared socket permissions reach the
// Seatbelt policy, and only the explicit allow-all grant emits broad rules.
func TestPrepareExecProcessUsesPreparedUnixSocketPolicyInSeatbeltLikeRust(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	allowedSocket := filepath.Join(cwd, "allowed.sock")
	sandboxContext, err := json.Marshal(FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	params := &ExecParams{
		Argv:                  []string{"/usr/bin/true"},
		CWD:                   cwd,
		Env:                   map[string]string{},
		Sandbox:               sandboxContext,
		EnforceManagedNetwork: true,
		ManagedNetwork: &ManagedNetworkSandboxContext{
			LoopbackPorts:    []uint16{43123},
			AllowUnixSockets: []string{allowedSocket},
		},
	}

	for _, allowAll := range []bool{false, true} {
		params.ManagedNetwork.DangerouslyAllowAllUnixSockets = allowAll
		command, _, sandboxType, err := prepareExecProcess(params, "")
		if err != nil {
			t.Fatalf("prepareExecProcess(allowAll=%t) error = %v", allowAll, err)
		}
		if sandboxType != sandbox.SandboxTypeMacosSeatbelt {
			t.Fatalf("sandbox type = %q, want %q", sandboxType, sandbox.SandboxTypeMacosSeatbelt)
		}
		policy := ""
		for i := 0; i+1 < len(command); i++ {
			if command[i] == "-p" {
				policy = command[i+1]
				break
			}
		}
		if policy == "" {
			t.Fatalf("command is missing Seatbelt policy: %#v", command)
		}
		if allowAll {
			for _, want := range []string{
				"(allow system-socket (socket-domain AF_UNIX))",
				"(allow network-bind (local unix-socket))",
				"(allow network-outbound (remote unix-socket))",
			} {
				if !strings.Contains(policy, want) {
					t.Fatalf("allow-all policy missing %q:\n%s", want, policy)
				}
			}
			if strings.Contains(strings.Join(command, "\n"), "-DUNIX_SOCKET_PATH_") {
				t.Fatalf("allow-all command emitted path parameters: %#v", command)
			}
			continue
		}
		wantParameter := "-DUNIX_SOCKET_PATH_0=" + allowedSocket
		if !strings.Contains(strings.Join(command, "\n"), wantParameter) {
			t.Fatalf("command missing %q: %#v", wantParameter, command)
		}
		if !strings.Contains(policy, `(allow network-outbound (remote unix-socket (subpath (param "UNIX_SOCKET_PATH_0"))))`) {
			t.Fatalf("policy missing restricted socket rule:\n%s", policy)
		}
		if strings.Contains(policy, "(allow network-outbound (remote unix-socket))\n") {
			t.Fatalf("restricted policy grants every Unix socket:\n%s", policy)
		}
	}
}
