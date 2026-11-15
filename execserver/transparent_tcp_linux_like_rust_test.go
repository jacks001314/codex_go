//go:build linux

package execserver

import (
	"encoding/json"
	"testing"
)

// TestPrepareExecProcessTransparentTCPLikeRust pins Rust #61372's executor
// wiring: transparent TCP reaches the Linux helper only for managed launches.
func TestPrepareExecProcessTransparentTCPLikeRust(t *testing.T) {
	sandboxContext, err := json.Marshal(FileSystemSandboxContext{
		Permissions: json.RawMessage(`{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`),
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	managed := &ExecParams{
		Argv:                  []string{"true"},
		CWD:                   t.TempDir(),
		Env:                   map[string]string{},
		Sandbox:               sandboxContext,
		EnforceManagedNetwork: true,
		ManagedNetwork: &ManagedNetworkSandboxContext{
			LoopbackPorts:     []uint16{3128},
			AllowLocalBinding: true,
		},
	}
	command, _, _, err := prepareExecProcessWithTransparentTCP(managed, "", true)
	if err != nil {
		t.Fatalf("prepareExecProcessWithTransparentTCP() error = %v", err)
	}
	if !containsExecServerArg(command, "--transparent-tcp") {
		t.Fatalf("managed command is missing --transparent-tcp: %#v", command)
	}
	disabled, _, _, err := prepareExecProcessWithTransparentTCP(managed, "", false)
	if err != nil {
		t.Fatalf("prepareExecProcessWithTransparentTCP(false) error = %v", err)
	}
	if containsExecServerArg(disabled, "--transparent-tcp") {
		t.Fatalf("disabled command unexpectedly carries --transparent-tcp: %#v", disabled)
	}
	unmanaged := *managed
	unmanaged.EnforceManagedNetwork = false
	unmanaged.ManagedNetwork = nil
	plain, _, _, err := prepareExecProcessWithTransparentTCP(&unmanaged, "", true)
	if err != nil {
		t.Fatalf("prepareExecProcessWithTransparentTCP(unmanaged) error = %v", err)
	}
	if containsExecServerArg(plain, "--transparent-tcp") {
		t.Fatalf("unmanaged command unexpectedly carries --transparent-tcp: %#v", plain)
	}
}

func containsExecServerArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
