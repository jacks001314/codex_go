//go:build linux

package execserver

import (
	"encoding/json"
	"testing"

	"codex_go/sandbox"
)

// TestPrepareExecProcessForwardsUnixSocketPolicyLikeRust mirrors Rust #45534's
// Linux launch wiring: a proxy-routed sandbox launch hands the prepared
// managed-network context (including the Unix socket grant) to the helper.
func TestPrepareExecProcessForwardsUnixSocketPolicyLikeRust(t *testing.T) {
	sandboxContext, err := json.Marshal(FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	command, _, sandboxType, err := prepareExecProcess(&ExecParams{
		Argv:                  []string{"true"},
		CWD:                   t.TempDir(),
		Env:                   map[string]string{},
		Sandbox:               sandboxContext,
		EnforceManagedNetwork: true,
		ManagedNetwork: &ManagedNetworkSandboxContext{
			LoopbackPorts:                  []uint16{3128},
			AllowUnixSockets:               []string{"/tmp/allowed.sock"},
			DangerouslyAllowAllUnixSockets: true,
		},
	}, "")
	if err != nil {
		t.Fatalf("prepareExecProcess() error = %v", err)
	}
	if sandboxType != sandbox.SandboxTypeLinuxSeccomp {
		t.Fatalf("sandbox type = %q, want %q", sandboxType, sandbox.SandboxTypeLinuxSeccomp)
	}
	want := `{"loopbackPorts":[3128],"allowLocalBinding":false,"allowUnixSockets":["/tmp/allowed.sock"],"dangerouslyAllowAllUnixSockets":true}`
	found := false
	for i := 0; i+1 < len(command); i++ {
		if command[i] != "--managed-network" {
			continue
		}
		found = true
		if command[i+1] != want {
			t.Fatalf("--managed-network = %s, want %s", command[i+1], want)
		}
	}
	if !found {
		t.Fatalf("command is missing --managed-network: %#v", command)
	}
}
