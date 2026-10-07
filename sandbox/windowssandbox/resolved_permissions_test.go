package windowssandbox

import (
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestPermissionProfileWorkspaceWriteUsesWindowsTempEnvVars(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	mode, err := TokenModeForPermissionProfile(&profile, nil, `C:\repo`, map[string]string{
		"TEMP": `C:\tmp`,
		"TMP":  `C:\tmp`,
	})
	if err != nil {
		t.Fatalf("TokenModeForPermissionProfile() error = %v", err)
	}
	if mode != WindowsSandboxTokenModeWritableRootsCapability {
		t.Fatalf("mode = %s", mode)
	}
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	roots := permissions.WritableRootsForCWD(`C:\repo`, map[string]string{"TEMP": `C:\tmp`, "TMP": `C:\tmp`})
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want cwd and temp", roots)
	}
}

func TestTokenModeForReadOnlyProfileUsesReadOnlyCapability(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	mode, err := TokenModeForPermissionProfile(&profile, nil, `C:\repo`, nil)
	if err != nil {
		t.Fatalf("TokenModeForPermissionProfile() error = %v", err)
	}
	if mode != WindowsSandboxTokenModeReadOnlyCapability {
		t.Fatalf("mode = %s", mode)
	}
}

func TestResolvePermissionsRejectsDisabledProfile(t *testing.T) {
	profile := coresandbox.FullAccessPermissionProfile()
	if _, err := ResolvePermissions(&profile, nil); err == nil {
		t.Fatalf("ResolvePermissions(full access) error = nil, want failure")
	}
}
func TestHasSymbolicRootReadAccessMirrorsReadOnly(t *testing.T) {
	readOnly := &ResolvedWindowsSandboxPermissions{FileSystem: &coresandbox.SandboxPolicy{Kind: coresandbox.SandboxReadOnly}}
	if !readOnly.HasSymbolicRootReadAccess("C:\\work") {
		t.Fatal("read-only policy should expose a symbolic root read for cwd")
	}
	if readOnly.HasSymbolicRootReadAccess("") {
		t.Fatal("read-only policy with empty cwd should not expose a symbolic root read")
	}
	writeOnly := &ResolvedWindowsSandboxPermissions{FileSystem: &coresandbox.SandboxPolicy{Kind: coresandbox.SandboxWorkspaceWrite}}
	if writeOnly.HasSymbolicRootReadAccess("C:\\work") {
		t.Fatal("workspace-write policy should not expose a symbolic root read")
	}
}

// Mirrors Rust #51512: writable temp roots come from the workload environment
// only. Absolute TEMP/TMP values are honored, mixed-case and duplicate
// spellings collapse case-insensitively, and the host TEMP/TMP is never used as
// a fallback.
func TestWritableTempRootsMatchTheWorkloadEnvironmentLikeRust(t *testing.T) {
	// The host temp must never leak into the sandbox grants.
	t.Setenv("TEMP", `C:\host-temp`)
	t.Setenv("TMP", `C:\host-temp`)

	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"workload TEMP", map[string]string{"TEMP": `C:\tmp`}, []string{`C:\tmp`}},
		{"mixed-case key", map[string]string{"tmp": `C:\workload-tmp`}, []string{`C:\workload-tmp`}},
		// Duplicates keep the first spelling in the deterministic order the
		// child environment block uses ("Temp" before "temp").
		{"duplicate spellings", map[string]string{"Temp": `C:\first`, "temp": `C:\second`}, []string{`C:\first`}},
		{"TEMP and TMP agree", map[string]string{"TEMP": `C:\tmp`, "TMP": `C:\tmp`}, []string{`C:\tmp`}},
		{"TEMP and TMP differ", map[string]string{"TMP": `C:\tmp-b`, "TEMP": `C:\tmp-a`}, []string{`C:\tmp-a`, `C:\tmp-b`}},
		// Relative values are not absolute temp paths, and no host fallback applies.
		{"relative value", map[string]string{"TEMP": `relative\tmp`}, nil},
		{"missing value", map[string]string{"TEMP": ``}, nil},
		{"no temp keys", map[string]string{"PATH": `C:\bin`}, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := windowsTempEnvRoots(testCase.env)
			if len(got) != len(testCase.want) {
				t.Fatalf("windowsTempEnvRoots() = %#v, want %#v", got, testCase.want)
			}
			for i := range got {
				// Off Windows the absolute-path cleaning resolves a DOS path
				// against the process working directory, so compare the tail.
				if !strings.HasSuffix(got[i], testCase.want[i]) {
					t.Fatalf("windowsTempEnvRoots() = %#v, want %#v", got, testCase.want)
				}
			}
		})
	}

	// The same policy wires the resolved roots into the sandbox grants.
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	if roots := permissions.WritableRootsForCWD(`C:\repo`, map[string]string{"TEMP": `C:\tmp`}); len(roots) != 2 {
		t.Fatalf("roots = %#v, want cwd and the workload temp", roots)
	}
	if roots := permissions.WritableRootsForCWD(`C:\repo`, map[string]string{}); len(roots) != 1 {
		t.Fatalf("roots = %#v, want cwd only without a workload temp", roots)
	}
}
