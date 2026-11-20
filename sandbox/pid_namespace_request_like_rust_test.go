package sandbox

import (
	"errors"
	"testing"
)

// Rust #47989 settles the Linux PID namespace policy at executor startup, so a
// request may only carry a mode the helper understands.
func TestBuildCommandRunPlanPIDNamespaceLikeRust(t *testing.T) {
	plan, err := BuildCommandRunPlan(&CommandRunRequest{
		PermissionProfile:        ":danger-full-access",
		Command:                  []string{"echo", "hello"},
		LinuxSandboxPIDNamespace: LinuxSandboxPIDNamespaceInherit,
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan(inherit) error = %v", err)
	}
	if plan.LinuxSandboxPIDNamespace != LinuxSandboxPIDNamespaceInherit {
		t.Fatalf("plan PID namespace = %q", plan.LinuxSandboxPIDNamespace)
	}

	plain, err := BuildCommandRunPlan(&CommandRunRequest{
		PermissionProfile: ":danger-full-access",
		Command:           []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("BuildCommandRunPlan(default) error = %v", err)
	}
	if plain.LinuxSandboxPIDNamespace != LinuxSandboxPIDNamespaceIsolate {
		t.Fatalf("default plan PID namespace = %q, want isolate", plain.LinuxSandboxPIDNamespace)
	}

	if _, err := BuildCommandRunPlan(&CommandRunRequest{
		PermissionProfile:        ":danger-full-access",
		Command:                  []string{"echo", "hello"},
		LinuxSandboxPIDNamespace: "host",
	}); !errors.Is(err, ErrInvalidSandboxRunRequest) {
		t.Fatalf("BuildCommandRunPlan(host) error = %v", err)
	}
}
