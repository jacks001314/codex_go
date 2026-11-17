package linuxsandbox

import "testing"

// TestManagedNetworkContextRequestsTransparentTCPLikeRust pins Rust #61851
// SandboxManager: a prepared managed-network context can request capture
// without the executor startup switch.
func TestManagedNetworkContextRequestsTransparentTCPLikeRust(t *testing.T) {
	profileJSON := `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`
	cwd := t.TempDir()
	args, err := CreateCommandArgsWithSandboxExeAndManagedNetworkAndTransparentTCP(
		"",
		[]string{"true"},
		cwd,
		profileJSON,
		cwd,
		false,
		true,
		`{"transparentTcp":true,"loopbackPorts":[3128],"allowLocalBinding":true}`,
		false,
	)
	if err != nil {
		t.Fatalf("argv error = %v", err)
	}
	found := false
	for _, arg := range args {
		if arg == "--transparent-tcp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("argv is missing --transparent-tcp: %#v", args)
	}

	off, err := CreateCommandArgsWithSandboxExeAndManagedNetworkAndTransparentTCP(
		"",
		[]string{"true"},
		cwd,
		profileJSON,
		cwd,
		false,
		true,
		`{"loopbackPorts":[3128],"allowLocalBinding":true}`,
		false,
	)
	if err != nil {
		t.Fatalf("argv error = %v", err)
	}
	for _, arg := range off {
		if arg == "--transparent-tcp" {
			t.Fatalf("context without capture emitted --transparent-tcp: %#v", off)
		}
	}
}
