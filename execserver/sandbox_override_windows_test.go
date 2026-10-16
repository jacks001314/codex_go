//go:build windows

package execserver

import (
	"encoding/json"
	"testing"
)

// Mirrors the Rust exec-server process path (#51786 process_sandbox.rs): an
// unparsable sandboxOverride is invalid params before any launch, exactly like
// the sibling Windows sandbox level gate.
func TestExecServerRejectsUnknownSandboxOverrideLikeRust(t *testing.T) {
	payload, err := json.Marshal(FileSystemSandboxContext{
		Permissions:         json.RawMessage(managedRestrictedPermissions),
		SandboxOverride:     SandboxOverride("escalated"),
		WindowsSandboxLevel: windowsSandboxLevelRestrictedToken,
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	_, supported, err := startExecServerSandboxProcess(&ExecParams{
		ProcessID: "override-1",
		Argv:      []string{"echo", "hi"},
		CWD:       t.TempDir(),
		Env:       map[string]string{},
		Sandbox:   payload,
	})
	want := "invalid sandbox context: unknown variant `escalated`, expected one of `noOverride`, `escalatedSandboxWithRestrictions`, `bypassSandboxFirstAttempt`"
	if !supported || err == nil || err.Error() != want {
		t.Fatalf("supported = %v, err = %v, want %q", supported, err, want)
	}

	// The acceptance half of the same seam: every shippable spelling decodes and
	// validates on the Windows launch path without a launch.
	for _, override := range []SandboxOverride{SandboxOverrideNone, SandboxOverrideEscalatedWithRestrictions, SandboxOverrideBypassFirstAttempt} {
		encoded, err := json.Marshal(FileSystemSandboxContext{
			Permissions:     json.RawMessage(managedRestrictedPermissions),
			SandboxOverride: override,
		})
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", override, err)
		}
		var context FileSystemSandboxContext
		if err := json.Unmarshal(encoded, &context); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", override, err)
		}
		if err := context.SandboxOverride.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", override, err)
		}
		if context.SandboxOverride != override {
			t.Fatalf("decoded override = %q, want %q", context.SandboxOverride, override)
		}
	}
}
