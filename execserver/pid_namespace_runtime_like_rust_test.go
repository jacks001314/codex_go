package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #47989 keeps the Linux PID namespace policy at executor startup: it is
// never read from a request, repository config or a command environment.
func TestExecServerLinuxSandboxPIDNamespaceRuntimeOptionsLikeRust(t *testing.T) {
	parent := NewServerWithHTTPClient(nil)
	parent.SetLinuxSandboxPIDNamespace(" inherit ")
	if got := parent.linuxSandboxPIDNamespaceValue(); got != "inherit" {
		t.Fatalf("startup PID namespace = %q", got)
	}

	child := newSessionServerWithRuntimeOptions(nil, parent)
	if got := child.linuxSandboxPIDNamespaceValue(); got != "inherit" {
		t.Fatalf("session server lost linux_sandbox_pid_namespace: %q", got)
	}

	context := &FileSystemSandboxContext{}
	parent.applyFSSandboxRuntimeOptions(context)
	if context.linuxSandboxPIDNamespace != "inherit" {
		t.Fatalf("filesystem sandbox context PID namespace = %q", context.linuxSandboxPIDNamespace)
	}

	options := parent.execProcessRuntimeOptions()
	if options.LinuxSandboxPIDNamespace != "inherit" {
		t.Fatalf("process launch PID namespace = %q", options.LinuxSandboxPIDNamespace)
	}
	if plain := NewServerWithHTTPClient(nil); plain.linuxSandboxPIDNamespaceValue() != "" {
		t.Fatalf("default PID namespace = %q, want empty", plain.linuxSandboxPIDNamespaceValue())
	}
}

// The filesystem sandbox context that a request carries exposes no PID
// namespace choice, so a remote executor cannot be told to inherit one.
func TestFileSystemSandboxContextCarriesNoPIDNamespaceChoiceLikeRust(t *testing.T) {
	context := &FileSystemSandboxContext{}
	context.SetLinuxSandboxPIDNamespace("inherit")
	payload, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("marshal filesystem sandbox context: %v", err)
	}
	for _, field := range []string{"linuxSandboxPIDNamespace", "linux_sandbox_pid_namespace", "inherit"} {
		if strings.Contains(string(payload), field) {
			t.Fatalf("filesystem sandbox context serialized the PID namespace policy: %s", payload)
		}
	}
}
