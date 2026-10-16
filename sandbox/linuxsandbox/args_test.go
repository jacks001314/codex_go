package linuxsandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateCommandArgsWithSandboxExeOverride(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "custom-codex-linux-sandbox")
	cwd := filepath.Join(t.TempDir(), "repo")
	args, err := CreateCommandArgsWithSandboxExe(helper, []string{"echo", "hi"}, cwd, `{"type":"disabled"}`, "", false, false)
	if err != nil {
		t.Fatalf("CreateCommandArgsWithSandboxExe() error = %v", err)
	}
	if len(args) == 0 || args[0] != filepath.Clean(helper) {
		t.Fatalf("args[0] = %q, want helper %q in %#v", args[0], filepath.Clean(helper), args)
	}
	if args[2] != filepath.Clean(cwd) || args[4] != filepath.Clean(cwd) {
		t.Fatalf("cwd args = %#v, want %q", args, filepath.Clean(cwd))
	}
}

func TestCreateCommandArgsUsesDefaultSandboxExe(t *testing.T) {
	args, err := CreateCommandArgs([]string{"echo"}, "", `{"type":"disabled"}`, "", false, false)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if len(args) == 0 || args[0] != "codex-linux-sandbox" {
		t.Fatalf("args[0] = %q in %#v", args[0], args)
	}
}

func TestCreateCommandArgsAllowsNetworkForProxy(t *testing.T) {
	args, err := CreateCommandArgs([]string{"echo"}, "", `{"type":"managed"}`, "", true, true)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	if !containsArg(args, "--allow-network-for-proxy") {
		t.Fatalf("args missing managed proxy flag: %#v", args)
	}
	if containsArg(args, "--use-legacy-landlock") {
		t.Fatalf("args should not combine legacy landlock with managed proxy routing: %#v", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

// TestCreateCommandArgsWithoutManagedNetworkIsUnchanged pins the zero-change
// requirement: leaving the managed-network context empty must produce exactly
// the argv the legacy builders produced, byte for byte.
func TestCreateCommandArgsWithoutManagedNetworkIsUnchanged(t *testing.T) {
	legacy, err := CreateCommandArgsWithSandboxExe("/usr/bin/helper", []string{"echo", "hi"}, "/repo", `{"type":"managed"}`, "/policy", false, true)
	if err != nil {
		t.Fatalf("CreateCommandArgsWithSandboxExe() error = %v", err)
	}
	same, err := CreateCommandArgsWithSandboxExeAndManagedNetwork("/usr/bin/helper", []string{"echo", "hi"}, "/repo", `{"type":"managed"}`, "/policy", false, true, "")
	if err != nil {
		t.Fatalf("CreateCommandArgsWithSandboxExeAndManagedNetwork() error = %v", err)
	}
	if strings.Join(legacy, "\x00") != strings.Join(same, "\x00") {
		t.Fatalf("argv changed without a managed-network context:\nlegacy=%#v\nnew=%#v", legacy, same)
	}
	if containsArg(same, "--managed-network") {
		t.Fatalf("argv must not carry --managed-network when the context is empty: %#v", same)
	}
	legacyOff, err := CreateCommandArgs([]string{"echo"}, "", `{"type":"disabled"}`, "", false, false)
	if err != nil {
		t.Fatalf("CreateCommandArgs() error = %v", err)
	}
	sameOff, err := CreateCommandArgsWithManagedNetwork([]string{"echo"}, "", `{"type":"disabled"}`, "", false, false, "")
	if err != nil {
		t.Fatalf("CreateCommandArgsWithManagedNetwork() error = %v", err)
	}
	if strings.Join(legacyOff, "\x00") != strings.Join(sameOff, "\x00") {
		t.Fatalf("argv changed for a plain request:\nlegacy=%#v\nnew=%#v", legacyOff, sameOff)
	}
}

// TestCreateCommandArgsManagedNetworkContextEnablesProxyMode mirrors the Rust
// helper, where a managed-network context on its own selects proxy mode.
func TestCreateCommandArgsManagedNetworkContextEnablesProxyMode(t *testing.T) {
	context := `{"loopbackPorts":[3128],"allowLocalBinding":false,"allowUnixSockets":[],"dangerouslyAllowAllUnixSockets":true}`
	args, err := CreateCommandArgsWithManagedNetwork([]string{"echo"}, "", `{"type":"managed"}`, "", true, false, context)
	if err != nil {
		t.Fatalf("CreateCommandArgsWithManagedNetwork() error = %v", err)
	}
	if !containsArg(args, "--allow-network-for-proxy") {
		t.Fatalf("a managed-network context must imply proxy mode: %#v", args)
	}
	if !containsArgWindowStr(args, []string{"--managed-network", context}) {
		t.Fatalf("args missing the managed-network context: %#v", args)
	}
	// The legacy flag must be dropped for managed proxy routing, exactly like
	// the Rust builder.
	if containsArg(args, "--use-legacy-landlock") {
		t.Fatalf("args should not combine legacy landlock with managed proxy routing: %#v", args)
	}
}

func containsArgWindowStr(args []string, window []string) bool {
	for i := 0; i+len(window) <= len(args); i++ {
		matched := true
		for j := range window {
			if args[i+j] != window[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
