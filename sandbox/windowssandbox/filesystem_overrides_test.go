package windowssandbox

import (
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

// TestResolveWindowsSandboxFilesystemOverridesSelectsBackendLikeRust locks the
// resolution order Rust #60042 centralized: a disabled sandbox resolves no
// overrides, an elevated level always takes the elevated resolver, and every
// other level keeps the restricted-token resolver.
func TestResolveWindowsSandboxFilesystemOverridesSelectsBackendLikeRust(t *testing.T) {
	permissions := workspaceWritePermissionsForTest(t)
	requested := WindowsSandboxFilesystemOverrides{
		ReadRoots:                        []string{`C:\read`},
		ReadRootsSet:                     true,
		ReadRootsIncludePlatformDefaults: true,
		WriteRoots:                       []string{`C:\write`},
		WriteRootsSet:                    true,
		AdditionalDenyWritePaths:         []string{`C:\repo\readonly`},
	}

	disabled, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelDisabled, permissions, requested)
	if err != nil {
		t.Fatalf("disabled level error = %v", err)
	}
	if disabled != nil {
		t.Fatalf("disabled level overrides = %#v, want nil", disabled)
	}

	for _, level := range []WindowsSandboxLevel{"", WindowsSandboxLevelRestrictedToken} {
		resolved, err := ResolveWindowsSandboxFilesystemOverrides(level, permissions, requested)
		if err != nil {
			t.Fatalf("%q level error = %v", string(level), err)
		}
		if resolved == nil {
			t.Fatalf("%q level resolved nil overrides", string(level))
		}
		// The restricted-token backend only consumes deny-write carveouts.
		if resolved.ReadRootsSet || len(resolved.ReadRoots) != 0 || resolved.ReadRootsIncludePlatformDefaults {
			t.Fatalf("%q level kept read roots: %#v", string(level), resolved)
		}
		if resolved.WriteRootsSet || len(resolved.WriteRoots) != 0 {
			t.Fatalf("%q level kept write roots: %#v", string(level), resolved)
		}
		if len(resolved.AdditionalDenyWritePaths) != 1 || resolved.AdditionalDenyWritePaths[0] != `C:\repo\readonly` {
			t.Fatalf("%q level deny-write = %#v", string(level), resolved.AdditionalDenyWritePaths)
		}
	}

	elevated, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelElevated, permissions, requested)
	if err != nil {
		t.Fatalf("elevated level error = %v", err)
	}
	if elevated == nil || !elevated.ReadRootsSet || !elevated.WriteRootsSet || !elevated.ReadRootsIncludePlatformDefaults {
		t.Fatalf("elevated level overrides = %#v", elevated)
	}
	if len(elevated.ReadRoots) != 1 || elevated.ReadRoots[0] != `C:\read` || len(elevated.WriteRoots) != 1 || elevated.WriteRoots[0] != `C:\write` {
		t.Fatalf("elevated level roots = %#v / %#v", elevated.ReadRoots, elevated.WriteRoots)
	}

	if _, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevel("mxc"), permissions, requested); err == nil ||
		!strings.Contains(err.Error(), "invalid windows sandbox level: mxc") {
		t.Fatalf("unknown level error = %v", err)
	}
}

// TestResolveRestrictedTokenRejectsDenyReadLikeRust locks that deny-read
// overrides are refused on the restricted-token backend and accepted on the
// elevated one, i.e. the backend choice happens before validation.
func TestResolveRestrictedTokenRejectsDenyReadLikeRust(t *testing.T) {
	permissions := workspaceWritePermissionsForTest(t)
	requested := WindowsSandboxFilesystemOverrides{
		AdditionalDenyReadPaths: []string{`C:\repo\.env`},
	}

	_, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelRestrictedToken, permissions, requested)
	if err == nil || !strings.Contains(err.Error(), "deny-read overrides require the elevated Windows sandbox backend") {
		t.Fatalf("restricted-token deny-read error = %v", err)
	}

	elevated, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelElevated, permissions, requested)
	if err != nil {
		t.Fatalf("elevated deny-read error = %v", err)
	}
	if len(elevated.AdditionalDenyReadPaths) != 1 || elevated.AdditionalDenyReadPaths[0] != `C:\repo\.env` {
		t.Fatalf("elevated deny-read = %#v", elevated.AdditionalDenyReadPaths)
	}
}

// TestResolveRestrictedTokenChecksSplitReadBeforeDenyReadLikeRust locks the
// ordering inside the restricted-token resolver: a policy without root read
// access reports the restricted-read reason even when it also carries deny-read
// entries.
func TestResolveRestrictedTokenChecksSplitReadBeforeDenyReadLikeRust(t *testing.T) {
	permissions := &ResolvedWindowsSandboxPermissions{}
	requested := WindowsSandboxFilesystemOverrides{AdditionalDenyReadPaths: []string{`C:\repo\.env`}}
	_, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelRestrictedToken, permissions, requested)
	if err == nil || !strings.Contains(err.Error(), "restricted read-only access requires the elevated Windows sandbox backend") {
		t.Fatalf("restricted-token split-read error = %v", err)
	}

	if _, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelRestrictedToken, nil, requested); err == nil ||
		!strings.Contains(err.Error(), "restricted-token Windows sandbox requires an enforceable filesystem policy") {
		t.Fatalf("nil permissions error = %v", err)
	}
	if _, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelElevated, nil, requested); err == nil ||
		!strings.Contains(err.Error(), "elevated Windows sandbox requires an enforceable filesystem policy") {
		t.Fatalf("nil elevated permissions error = %v", err)
	}
}

// TestResolveElevatedRejectsRootReadDenialLikeRust keeps the elevated backend's
// root-read requirement on the shared resolver.
func TestResolveElevatedRejectsRootReadDenialLikeRust(t *testing.T) {
	permissions := workspaceWritePermissionsForTest(t)
	permissions.DeniedReadEntries = []coresandbox.FileSystemSandboxEntry{{
		Path:   coresandbox.FileSystemPath{Type: "special", Value: &coresandbox.FileSystemSpecialPath{Kind: "root"}},
		Access: coresandbox.FileSystemAccessDeny,
	}}
	_, err := ResolveWindowsSandboxFilesystemOverrides(WindowsSandboxLevelElevated, permissions, WindowsSandboxFilesystemOverrides{})
	if err == nil || !strings.Contains(err.Error(), "elevated Windows sandbox requires effective `:root` read access") {
		t.Fatalf("elevated root-read denial error = %v", err)
	}
}

// TestWindowsSandboxFilesystemOverridesProjectionKeepsDenyPaths locks the
// projection onto SetupRootOverrides so the elevated setup path keeps consuming
// the same deny-read/deny-write fields it did before the centralization.
func TestWindowsSandboxFilesystemOverridesProjectionKeepsDenyPaths(t *testing.T) {
	overrides := WindowsSandboxFilesystemOverrides{
		ReadRoots:                        []string{`C:\read`},
		ReadRootsSet:                     true,
		ReadRootsIncludePlatformDefaults: true,
		WriteRoots:                       []string{`C:\write`},
		WriteRootsSet:                    true,
		AdditionalDenyReadPaths:          []string{`C:\repo\.env`},
		AdditionalDenyWritePaths:         []string{`C:\repo\readonly`},
	}.SetupRootOverrides()
	if !overrides.ReadRootsSet || !overrides.WriteRootsSet || !overrides.ReadRootsIncludePlatformDefaults {
		t.Fatalf("projection flags = %#v", overrides)
	}
	if len(overrides.DenyReadPaths) != 1 || overrides.DenyReadPaths[0] != `C:\repo\.env` {
		t.Fatalf("projection deny-read = %#v", overrides.DenyReadPaths)
	}
	if len(overrides.DenyWritePaths) != 1 || overrides.DenyWritePaths[0] != `C:\repo\readonly` {
		t.Fatalf("projection deny-write = %#v", overrides.DenyWritePaths)
	}
}

func workspaceWritePermissionsForTest(t *testing.T) *ResolvedWindowsSandboxPermissions {
	t.Helper()
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{`C:\repo`})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	return permissions
}
