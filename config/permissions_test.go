package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"

	json "github.com/goccy/go-json"
)

func TestResolveSandboxPermissionProfileCompilesCustomRuntimeJSON(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	extra := filepath.Join(t.TempDir(), "shared")
	cfg := &Config{Values: map[string]any{
		"default_permissions": "dev",
		"permissions": map[string]any{
			"dev": map[string]any{
				"workspace_roots": map[string]any{
					extra: true,
				},
				"filesystem": map[string]any{
					"glob_scan_max_depth": int64(2),
					":minimal":            "read",
					":workspace_roots": map[string]any{
						".":        "write",
						"docs":     "read",
						"**/*.env": "deny",
					},
				},
				"network": map[string]any{"enabled": true},
			},
		},
	}}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.ID != "dev" || resolved.Profile == nil || !resolved.Profile.AllowsNetwork() {
		t.Fatalf("resolved = %+v", resolved)
	}
	wire := decodeRuntimeProfile(t, resolved.ProfileJSON)
	if wire.Network != string(sandbox.NetworkEnabled) || wire.FileSystem.GlobScanMaxDepth == nil || *wire.FileSystem.GlobScanMaxDepth != 2 {
		t.Fatalf("wire = %+v", wire)
	}
	assertRuntimeEntry(t, wire, "path", filepath.Clean(cwd), "", string(sandbox.FileSystemAccessWrite))
	assertRuntimeEntry(t, wire, "path", filepath.Join(cwd, "docs"), "", string(sandbox.FileSystemAccessRead))
	assertRuntimeEntry(t, wire, "path", filepath.Clean(extra), "", string(sandbox.FileSystemAccessWrite))
	assertRuntimeEntry(t, wire, "glob_pattern", "", filepath.Join(cwd, "**", "*.env"), string(sandbox.FileSystemAccessDeny))
	assertRuntimeEntry(t, wire, "glob_pattern", "", filepath.Join(extra, "**", "*.env"), string(sandbox.FileSystemAccessDeny))
}

// Rust protects the project-root metadata names (`.git`/`.agents`/`.codex`/`.aws`)
// for every writable root, not only the built-in workspace profile. Go derives
// the same protection from the resolved policy's writable roots, so a custom
// profile that writes `:workspace_roots` without extending `:workspace` still
// keeps those metadata names read-only.
func TestCustomPermissionProfileProtectsProjectMetadataRootsLikeRust(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	cfg := &Config{Values: map[string]any{
		"default_permissions": "dev",
		"permissions": map[string]any{
			"dev": map[string]any{
				"filesystem": map[string]any{
					":workspace_roots": map[string]any{".": "write"},
				},
			},
		},
	}}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.Profile == nil || resolved.Profile.SandboxPolicy == nil {
		t.Fatalf("resolved = %+v", resolved)
	}
	roots := resolved.Profile.SandboxPolicy.GetWritableRootsWithCWD(cwd)
	if len(roots) == 0 {
		t.Fatal("custom profile produced no writable roots")
	}
	for _, root := range roots {
		for _, name := range []string{".git", ".agents", ".gcode", ".aws"} {
			protected := filepath.Join(root.Root, name)
			if root.IsPathWritable(filepath.Join(protected, "probe.txt")) {
				t.Fatalf("metadata path %s is writable under root %s", protected, root.Root)
			}
		}
	}
}

// Mirrors Rust's PermissionProfile::materialize_project_roots_with_path_uris
// (#46568): supplied workspace roots anchor the profile's project roots instead
// of the thread cwd.
func TestResolveSandboxPermissionProfileWithWorkspaceRootsAnchorsProjectRootsLikeRust(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	environmentRoot := filepath.Join(t.TempDir(), "remote")
	cfg := &Config{Values: map[string]any{
		"default_permissions": "dev",
		"permissions": map[string]any{
			"dev": map[string]any{
				"filesystem": map[string]any{
					":minimal": "read",
					":workspace_roots": map[string]any{
						".":    "write",
						"docs": "read",
					},
				},
			},
		},
	}}
	byCWD, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if byCWD == nil || len(byCWD.WorkspaceRoots) == 0 || !strings.EqualFold(filepath.Clean(byCWD.WorkspaceRoots[0]), filepath.Clean(cwd)) {
		t.Fatalf("cwd-anchored roots = %#v, want %q", byCWD, cwd)
	}
	assertRuntimeEntry(t, decodeRuntimeProfile(t, byCWD.ProfileJSON), "path", filepath.Clean(cwd), "", string(sandbox.FileSystemAccessWrite))

	byEnvironment, err := cfg.ResolveSandboxPermissionProfileWithWorkspaceRoots("", cwd, []string{environmentRoot})
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfileWithWorkspaceRoots() error = %v", err)
	}
	if byEnvironment == nil || len(byEnvironment.WorkspaceRoots) == 0 || !strings.EqualFold(filepath.Clean(byEnvironment.WorkspaceRoots[0]), filepath.Clean(environmentRoot)) {
		t.Fatalf("environment-anchored roots = %#v, want %q", byEnvironment, environmentRoot)
	}
	environmentWire := decodeRuntimeProfile(t, byEnvironment.ProfileJSON)
	assertRuntimeEntry(t, environmentWire, "path", filepath.Clean(environmentRoot), "", string(sandbox.FileSystemAccessWrite))
	assertRuntimeEntry(t, environmentWire, "path", filepath.Join(environmentRoot, "docs"), "", string(sandbox.FileSystemAccessRead))
	for _, entry := range environmentWire.FileSystem.Entries {
		if entry.Path.Type == "path" && strings.EqualFold(filepath.Clean(entry.Path.Path), filepath.Clean(cwd)) {
			t.Fatalf("environment-anchored profile still carried the thread cwd: %s", byEnvironment.ProfileJSON)
		}
	}
}

// Rust #48176: `.aws` joins `.git`/`.agents`/`.codex` as a default read-only
// project-root metadata subpath, so a writable workspace root cannot modify the
// AWS credential helpers an AWS profile may select.
func TestWorkspaceProfileProtectsAWSMetadataPathLikeRust(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", cwd, err)
	}
	cfg := &Config{Values: map[string]any{"default_permissions": ":workspace"}}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	wire := decodeRuntimeProfile(t, resolved.ProfileJSON)
	assertSpecialRuntimeEntry(t, wire, "project_roots", "", string(sandbox.FileSystemAccessWrite))
	for _, name := range []string{".git", ".agents", ".gcode", ".aws"} {
		assertSpecialRuntimeEntry(t, wire, "project_roots", name, string(sandbox.FileSystemAccessRead))
	}
}

func TestResolveSandboxPermissionProfileCustomWorkspaceWinsOverAlias(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"default_permissions": "workspace",
		"permissions": map[string]any{
			"workspace": map[string]any{
				"filesystem": map[string]any{
					":minimal": "read",
				},
			},
		},
	}}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	wire := decodeRuntimeProfile(t, resolved.ProfileJSON)
	for _, entry := range wire.FileSystem.Entries {
		if entry.Access == string(sandbox.FileSystemAccessWrite) {
			t.Fatalf("custom workspace profile was treated as builtin workspace: %s", resolved.ProfileJSON)
		}
	}
}

func TestResolveSandboxPermissionProfileLegacySandboxMode(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "cache")
	cfg := &Config{Values: map[string]any{
		"sandbox_mode": "workspace-write",
		"sandbox_workspace_write": map[string]any{
			"writable_roots":         []any{extra},
			"network_access":         true,
			"exclude_tmpdir_env_var": true,
			"exclude_slash_tmp":      true,
		},
	}}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.ID != "workspace-write" || !resolved.Profile.AllowsNetwork() {
		t.Fatalf("resolved = %+v", resolved)
	}
	wire := decodeRuntimeProfile(t, resolved.ProfileJSON)
	assertRuntimeEntry(t, wire, "path", filepath.Clean(extra), "", string(sandbox.FileSystemAccessWrite))
}

// Rust parity: permissions_tests.rs legacy_project_roots_restrictions_do_not_fail_open
// (#38916). Profiles written before the rename to :workspace_roots may still use
// :project_roots; the alias must keep deny rules and read-only subpath carveouts
// enforced instead of silently dropping the entries (fail-open).
func TestLegacyProjectRootsRestrictionsDoNotFailOpenLikeRust(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	docs := filepath.Join(cwd, "docs")
	cfg := &Config{Values: map[string]any{
		"permissions": map[string]any{
			"read_deny": map[string]any{
				"filesystem": map[string]any{
					":root":          "read",
					":project_roots": "none",
				},
			},
			"write_deny": map[string]any{
				"filesystem": map[string]any{
					":root":          "write",
					":project_roots": "none",
				},
			},
			"write_read": map[string]any{
				"filesystem": map[string]any{
					":root": "write",
					":project_roots": map[string]any{
						"docs": "read",
					},
				},
			},
		},
	}}

	readDeny, err := cfg.ResolveSandboxPermissionProfile("read_deny", cwd)
	if err != nil {
		t.Fatalf("read_deny ResolveSandboxPermissionProfile() error = %v", err)
	}
	if readDeny.Profile == nil || !readDeny.Profile.DeniesReadPath(cwd) {
		t.Fatalf("read_deny must deny the project root, profile = %+v", readDeny.Profile)
	}
	readDenyWire := decodeRuntimeProfile(t, readDeny.ProfileJSON)
	assertRuntimeEntry(t, readDenyWire, "path", filepath.Clean(cwd), "", string(sandbox.FileSystemAccessDeny))
	assertSpecialRuntimeEntry(t, readDenyWire, "root", "", string(sandbox.FileSystemAccessRead))

	writeDeny, err := cfg.ResolveSandboxPermissionProfile("write_deny", cwd)
	if err != nil {
		t.Fatalf("write_deny ResolveSandboxPermissionProfile() error = %v", err)
	}
	if writeDeny.Profile == nil || !writeDeny.Profile.DeniesReadPath(cwd) {
		t.Fatalf("write_deny must deny the project root, profile = %+v", writeDeny.Profile)
	}
	writeDenyWire := decodeRuntimeProfile(t, writeDeny.ProfileJSON)
	assertRuntimeEntry(t, writeDenyWire, "path", filepath.Clean(cwd), "", string(sandbox.FileSystemAccessDeny))
	assertSpecialRuntimeEntry(t, writeDenyWire, "root", "", string(sandbox.FileSystemAccessWrite))

	writeRead, err := cfg.ResolveSandboxPermissionProfile("write_read", cwd)
	if err != nil {
		t.Fatalf("write_read ResolveSandboxPermissionProfile() error = %v", err)
	}
	writeReadWire := decodeRuntimeProfile(t, writeRead.ProfileJSON)
	assertSpecialRuntimeEntry(t, writeReadWire, "root", "", string(sandbox.FileSystemAccessWrite))
	assertRuntimeEntry(t, writeReadWire, "path", filepath.Clean(docs), "", string(sandbox.FileSystemAccessRead))
}

type runtimeProfileForTest struct {
	Type       string `json:"type"`
	FileSystem struct {
		Type             string `json:"type"`
		GlobScanMaxDepth *int   `json:"glob_scan_max_depth"`
		Entries          []struct {
			Path struct {
				Type    string `json:"type"`
				Path    string `json:"path"`
				Pattern string `json:"pattern"`
				Value   *struct {
					Kind    string  `json:"kind"`
					Subpath *string `json:"subpath,omitempty"`
				} `json:"value,omitempty"`
			} `json:"path"`
			Access string `json:"access"`
		} `json:"entries"`
	} `json:"file_system"`
	Network string `json:"network"`
}

func decodeRuntimeProfile(t *testing.T, raw string) runtimeProfileForTest {
	t.Helper()
	var wire runtimeProfileForTest
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("Unmarshal runtime profile error = %v: %s", err, raw)
	}
	return wire
}

func assertRuntimeEntry(t *testing.T, wire runtimeProfileForTest, typ string, path string, pattern string, access string) {
	t.Helper()
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type == typ && entry.Path.Path == path && entry.Path.Pattern == pattern && entry.Access == access {
			return
		}
	}
	t.Fatalf("entry type=%s path=%q pattern=%q access=%q not found in %+v", typ, path, pattern, access, wire.FileSystem.Entries)
}

func assertSpecialRuntimeEntry(t *testing.T, wire runtimeProfileForTest, kind string, subpath string, access string) {
	t.Helper()
	for _, entry := range wire.FileSystem.Entries {
		if entry.Path.Type != "special" || entry.Access != access || entry.Path.Value == nil {
			continue
		}
		if entry.Path.Value.Kind != kind {
			continue
		}
		entrySubpath := ""
		if entry.Path.Value.Subpath != nil {
			entrySubpath = *entry.Path.Value.Subpath
		}
		if entrySubpath == subpath {
			return
		}
	}
	t.Fatalf("special entry kind=%q subpath=%q access=%q not found in %+v", kind, subpath, access, wire.FileSystem.Entries)
}

func TestResolvePermissionProfileSelectionManagedDefaultWinsAndMergesCatalogLikeRust(t *testing.T) {
	// Rust #39752: a managed default overrides the configured default while
	// preserving Windows-style paths, and the merged configured + managed
	// catalog is returned without compiling executor paths.
	cfg := &Config{Values: map[string]any{
		"default_permissions": "configured-windows",
		"permissions": map[string]any{
			"configured-windows": map[string]any{
				"workspace_roots": map[string]any{`C:\Users\agent\workspace`: true},
				"filesystem":      map[string]any{`C:\Users\agent\workspace`: "write"},
			},
		},
	}}
	cfg.Requirements = &ConfigRequirements{
		DefaultPermissions: stringPtrConfig(`managed-windows`),
		AllowedPermissionProfiles: map[string]bool{
			"configured-windows": false,
			"managed-windows":    true,
		},
		Permissions: map[string]any{
			"managed-windows": map[string]any{
				"workspace_roots": map[string]any{`D:\Managed\workspace`: true},
				"filesystem":      map[string]any{`D:\Managed\workspace`: "read"},
			},
		},
	}
	selection, err := cfg.ResolvePermissionProfileSelection()
	if err != nil {
		t.Fatalf("ResolvePermissionProfileSelection() error = %v", err)
	}
	if selection.ProfileID != "managed-windows" {
		t.Fatalf("profile id = %q, want managed-windows", selection.ProfileID)
	}
	if len(selection.Profiles) != 2 {
		t.Fatalf("merged catalog = %#v, want configured + managed", selection.Profiles)
	}
	managed, ok := selection.Profiles["managed-windows"].(map[string]any)
	if !ok {
		t.Fatalf("managed profile missing from catalog: %#v", selection.Profiles)
	}
	roots, ok := managed["workspace_roots"].(map[string]any)
	if !ok || !roots[`D:\Managed\workspace`].(bool) {
		t.Fatalf("managed Windows-style workspace roots lost: %#v", managed)
	}
}

func TestResolvePermissionProfileSelectionRejectsUndefinedAllowlistedProfileLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{}}
	cfg.Requirements = &ConfigRequirements{
		DefaultPermissions: stringPtrConfig("missing-profile"),
		AllowedPermissionProfiles: map[string]bool{
			"missing-profile": true,
		},
	}
	_, err := cfg.ResolvePermissionProfileSelection()
	if err == nil || !strings.Contains(err.Error(), "allowed_permission_profiles refers to undefined profile `missing-profile`") {
		t.Fatalf("error = %v, want undefined allowlisted profile rejection", err)
	}
}

func stringPtrConfig(value string) *string {
	return &value
}
