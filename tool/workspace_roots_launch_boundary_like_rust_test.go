package tool

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	"codex_go/utils"
)

func workspaceBoundaryShellProfile(t *testing.T, denied string) (*sandbox.PermissionProfile, string) {
	t.Helper()
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
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(string(raw))
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v", err)
	}
	return profile, string(raw)
}

func TestShellLaunchPreservesEmptyWorkspaceRootsLikeRust(t *testing.T) {
	base := t.TempDir()
	denied := filepath.Join(base, "denied")
	profile, profileJSON := workspaceBoundaryShellProfile(t, denied)
	request, err := BuildShellRequest(
		&ExecCommandArgs{Cmd: "pwd"},
		nil,
		ShellValidationOptions{
			CWD:                 base,
			PermissionProfileID: "workspace-boundary",
			PermissionProfile:   profile,
			WorkspaceRoots:      []string{},
			ApprovalPolicy:      sandbox.ApprovalNever,
			PathConvention:      utils.NativePathConvention(),
		},
	)
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	request.PermissionProfileJSON = profileJSON
	if request.WorkspaceRoots == nil || len(request.WorkspaceRoots) != 0 {
		t.Fatalf("request workspace roots = %#v, want non-nil empty", request.WorkspaceRoots)
	}
	prepared, err := prepareUnifiedExecShellRequest(request)
	if err != nil {
		t.Fatalf("prepareUnifiedExecShellRequest() error = %v", err)
	}
	if prepared.WorkspaceRoots == nil || len(prepared.WorkspaceRoots) != 0 {
		t.Fatalf("prepared workspace roots = %#v, want non-nil empty", prepared.WorkspaceRoots)
	}
	entries := prepared.PermissionProfile.RuntimeFileSystemEntries()
	if len(entries) != 1 || entries[0].Access != sandbox.FileSystemAccessDeny || entries[0].Path.Path != denied {
		t.Fatalf("prepared entries = %#v, want only explicit deny %q", entries, denied)
	}
}

func TestCloneUnifiedExecEnvironmentsPreservesEmptyWorkspaceRoots(t *testing.T) {
	cloned := cloneUnifiedExecEnvironments([]UnifiedExecEnvironment{{ID: "local", WorkspaceRoots: []string{}}})
	if len(cloned) != 1 || cloned[0].WorkspaceRoots == nil || len(cloned[0].WorkspaceRoots) != 0 {
		t.Fatalf("cloned workspace roots = %#v, want non-nil empty", cloned)
	}
}
