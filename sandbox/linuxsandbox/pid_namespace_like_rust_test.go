//go:build linux

package linuxsandbox

import (
	"io"
	"strings"
	"testing"
)

const pidNamespaceProfileJSON = `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`

func TestLinuxSandboxHelperInheritNamespaceLikeRust(t *testing.T) {
	command, err := parseLinuxSandboxCommand([]string{
		"--sandbox-policy-cwd", "/tmp",
		"--permission-profile", pidNamespaceProfileJSON,
		"--inherit-pid-namespace",
		"--", "true",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseLinuxSandboxCommand() error = %v", err)
	}
	if !command.InheritPIDNamespace {
		t.Fatal("InheritPIDNamespace = false")
	}
	// Rust #47989 inserts the option right after the helper executable so the
	// inner stage reuses the inherited namespace instead of recreating it.
	inner, err := buildInnerLinuxSandboxCommand(command)
	if err != nil {
		t.Fatalf("buildInnerLinuxSandboxCommand() error = %v", err)
	}
	if inner[1] != "--inherit-pid-namespace" {
		t.Fatalf("inner argv = %#v, want --inherit-pid-namespace second", inner)
	}

	plain, err := parseLinuxSandboxCommand([]string{
		"--sandbox-policy-cwd", "/tmp",
		"--permission-profile", pidNamespaceProfileJSON,
		"--", "true",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseLinuxSandboxCommand() error = %v", err)
	}
	plainInner, err := buildInnerLinuxSandboxCommand(plain)
	if err != nil {
		t.Fatalf("buildInnerLinuxSandboxCommand() error = %v", err)
	}
	if strings.Contains(strings.Join(plainInner, " "), "--inherit-pid-namespace") {
		t.Fatalf("inner argv = %#v, want no PID namespace opt-in", plainInner)
	}
}

func TestTransparentTCPAllowsInheritedPIDNamespaceLikeRust(t *testing.T) {
	command := func() *linuxSandboxCommand {
		return &linuxSandboxCommand{
			SandboxPolicyCWD:     "/tmp",
			ApplySeccompThenExec: true,
			AllowNetworkForProxy: true,
			ManagedNetwork:       &linuxManagedNetworkContext{},
			TransparentTCP:       true,
			TransparentTCPFDs:    []int{7, 8, 9},
		}
	}

	isolated := command()
	if err := validateLinuxSandboxCommand(isolated); err != nil {
		t.Fatalf("validateLinuxSandboxCommand(isolate) error = %v", err)
	}
	inherited := command()
	inherited.InheritPIDNamespace = true
	if err := validateLinuxSandboxCommand(inherited); err != nil {
		t.Fatalf("validateLinuxSandboxCommand(inherit) error = %v", err)
	}

	noProc := command()
	noProc.NoProc = true
	err := validateLinuxSandboxCommand(noProc)
	if err == nil || !strings.Contains(err.Error(), "transparent TCP requires managed networking, bubblewrap, and procfs") {
		t.Fatalf("validateLinuxSandboxCommand(no-proc) error = %v", err)
	}
}
