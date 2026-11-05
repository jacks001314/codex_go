package windowssandbox

import (
	"encoding/json"
	"path/filepath"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestResolveCapturePermissionProfileBindsWorkspaceRootsLikeRust(t *testing.T) {
	base := t.TempDir()
	denied := filepath.Join(base, "denied")
	raw, err := json.Marshal(map[string]any{
		"type":    "managed",
		"network": "restricted",
		"file_system": map[string]any{
			"type": "restricted",
			"entries": []map[string]any{
				{"path": map[string]any{"type": "special", "value": map[string]any{"kind": "project_roots"}}, "access": "write"},
				{"path": map[string]any{"type": "path", "path": denied}, "access": "deny"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	profile, err := coresandbox.ParseRuntimePermissionProfileJSON(string(raw))
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v", err)
	}

	resolved, err := ResolveCapturePermissionProfile(&CaptureRequest{PermissionProfile: profile, CWD: base, WorkspaceRoots: []string{}})
	if err != nil {
		t.Fatalf("ResolveCapturePermissionProfile(empty roots) error = %v", err)
	}
	entries := resolved.RuntimeFileSystemEntries()
	if len(entries) != 1 || entries[0].Access != coresandbox.FileSystemAccessDeny || entries[0].Path.Path != denied {
		t.Fatalf("empty-root capture entries = %#v, want only explicit deny %q", entries, denied)
	}

	resolved, err = ResolveCapturePermissionProfile(&CaptureRequest{PermissionProfile: profile, CWD: base, WorkspaceRoots: []string{base}})
	if err != nil {
		t.Fatalf("ResolveCapturePermissionProfile(root) error = %v", err)
	}
	entries = resolved.RuntimeFileSystemEntries()
	if len(entries) != 2 || entries[0].Access != coresandbox.FileSystemAccessWrite || entries[0].Path.Path != base {
		t.Fatalf("capture entries with root = %#v", entries)
	}

	unresolved, err := ResolveCapturePermissionProfile(&CaptureRequest{PermissionProfile: profile, CWD: base})
	if err != nil {
		t.Fatalf("ResolveCapturePermissionProfile(nil roots) error = %v", err)
	}
	if unresolved != profile {
		t.Fatal("nil workspace roots unexpectedly rewrote an already resolved profile")
	}
}
