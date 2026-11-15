package sandbox

import (
	"testing"
)

// TestLinuxSandboxTransparentTCPArgsLikeRust pins Rust #61372's manager rule:
// the helper gets --transparent-tcp only when the command also has a managed
// network proxy.
func TestLinuxSandboxTransparentTCPArgsLikeRust(t *testing.T) {
	profileJSON := `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`
	managedNetworkJSON := `{"loopbackPorts":[3128],"allowLocalBinding":true}`
	command := []string{"true"}
	withProxy, err := CreateLinuxSandboxCommandArgsForPermissionProfileJSONWithSandboxExeManagedNetworkAndTransparentTCP(
		"",
		command,
		t.TempDir(),
		profileJSON,
		t.TempDir(),
		false,
		true,
		managedNetworkJSON,
		true,
	)
	if err != nil {
		t.Fatalf("managed transparent argv error = %v", err)
	}
	if !sandboxArgsContain(withProxy, "--transparent-tcp") {
		t.Fatalf("managed argv is missing --transparent-tcp: %#v", withProxy)
	}
	withoutProxy, err := CreateLinuxSandboxCommandArgsForPermissionProfileJSONWithSandboxExeManagedNetworkAndTransparentTCP(
		"",
		command,
		t.TempDir(),
		profileJSON,
		t.TempDir(),
		false,
		false,
		"",
		true,
	)
	if err != nil {
		t.Fatalf("unmanaged argv error = %v", err)
	}
	if sandboxArgsContain(withoutProxy, "--transparent-tcp") {
		t.Fatalf("unmanaged argv unexpectedly carries --transparent-tcp: %#v", withoutProxy)
	}
}

func sandboxArgsContain(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
