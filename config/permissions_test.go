package config

import (
	"path/filepath"
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
