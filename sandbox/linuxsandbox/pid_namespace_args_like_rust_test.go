package linuxsandbox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxSandboxPIDNamespaceArgumentLikeRust(t *testing.T) {
	base := LinuxSandboxCommandArgsOptions{
		SandboxExe:           "codex-linux-sandbox",
		Command:              []string{"echo", "hi"},
		CommandCWD:           "/repo",
		ProfileJSON:          `{"type":"read-only"}`,
		SandboxPolicyCWD:     "/repo",
		AllowNetworkForProxy: true,
		ManagedNetworkJSON:   `{"transparentTcp":false}`,
	}

	off, err := CreateCommandArgsForLinuxSandbox(base)
	if err != nil {
		t.Fatalf("CreateCommandArgsForLinuxSandbox() error = %v", err)
	}
	if off[1] != "--sandbox-policy-cwd" {
		t.Fatalf("default argv = %#v, want no PID namespace opt-in", off)
	}

	isolated := base
	isolated.PIDNamespace = LinuxSandboxPIDNamespaceIsolate
	explicit, err := CreateCommandArgsForLinuxSandbox(isolated)
	if err != nil {
		t.Fatalf("CreateCommandArgsForLinuxSandbox(isolate) error = %v", err)
	}
	if !reflect.DeepEqual(explicit, off) {
		t.Fatalf("isolate argv = %#v, want byte-identical default %#v", explicit, off)
	}

	inherited := base
	inherited.PIDNamespace = LinuxSandboxPIDNamespaceInherit
	on, err := CreateCommandArgsForLinuxSandbox(inherited)
	if err != nil {
		t.Fatalf("CreateCommandArgsForLinuxSandbox(inherit) error = %v", err)
	}
	if on[0] != off[0] {
		t.Fatalf("inherit argv helper = %q, want %q", on[0], off[0])
	}
	if on[1] != "--inherit-pid-namespace" {
		t.Fatalf("inherit argv = %#v, want the flag first after the helper", on)
	}
	if strings.Count(strings.Join(on, " "), "--inherit-pid-namespace") != 1 {
		t.Fatalf("inherit argv = %#v, want exactly one flag", on)
	}
	if len(on) != len(off)+1 {
		t.Fatalf("inherit argv = %#v, want exactly one extra argument", on)
	}
}

func TestLinuxSandboxPIDNamespaceRejectsUnknownModeLikeRust(t *testing.T) {
	_, err := CreateCommandArgsForLinuxSandbox(LinuxSandboxCommandArgsOptions{
		Command:          []string{"echo", "hi"},
		ProfileJSON:      `{"type":"read-only"}`,
		SandboxPolicyCWD: "/repo",
		PIDNamespace:     "host",
	})
	if !errors.Is(err, ErrInvalidCommandArgs) {
		t.Fatalf("CreateCommandArgsForLinuxSandbox(host) error = %v", err)
	}
	if _, err := ParseLinuxSandboxPIDNamespace("inherit"); err != nil {
		t.Fatalf("ParseLinuxSandboxPIDNamespace(inherit) error = %v", err)
	}
	if mode, err := ParseLinuxSandboxPIDNamespace(""); err != nil || mode != LinuxSandboxPIDNamespaceIsolate {
		t.Fatalf("ParseLinuxSandboxPIDNamespace(empty) = %v, %v", mode, err)
	}
}
