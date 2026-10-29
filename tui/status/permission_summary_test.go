package status

import (
	"testing"

	"codex_go/sandbox"
	"codex_go/utils"
)

// Mirrors Rust #45852 status card wiring: the status card derives its sandbox
// label and workspace-root suffix from the profile and executor paths.
func TestStatusPermissionSummaryAndWorkspaceRootSuffix(t *testing.T) {
	cwd := mustStatusPathURI(t, "file:///workspace/repo")
	extraRoot := mustStatusPathURI(t, "file:///workspace/shared%20files")
	roots := []*utils.PathURI{cwd, extraRoot}

	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.ExcludeTmpdirEnvVar = true
	profile.SandboxPolicy.ExcludeSlashTmp = true

	summary := StatusPermissionSummary(&profile, cwd, roots)
	if summary != "workspace" {
		t.Fatalf("StatusPermissionSummary() = %q, want workspace", summary)
	}
	suffix := WorkspaceRootSuffix(roots, cwd)
	if suffix != " [/workspace/shared files]" {
		t.Fatalf("WorkspaceRootSuffix() = %q", suffix)
	}
	if got, want := StatusPermissionsLabel(nil, &profile, sandbox.ApprovalOnRequest, summary, "Ask for approval", suffix),
		"Workspace [/workspace/shared files] (Ask for approval)"; got != want {
		t.Fatalf("StatusPermissionsLabel() = %q, want %q", got, want)
	}
	if got := WorkspaceRootSuffix([]*utils.PathURI{cwd}, cwd); got != "" {
		t.Fatalf("WorkspaceRootSuffix(cwd only) = %q, want empty", got)
	}
}

func TestStatusPermissionSummaryNetworkLabels(t *testing.T) {
	cwd := mustStatusPathURI(t, "file:///workspace/repo")

	readOnly := sandbox.ReadOnlyPermissionProfile()
	readOnly.NetworkEnabled = true
	if got := StatusPermissionSummary(&readOnly, cwd, []*utils.PathURI{cwd}); got != "read-only with network access" {
		t.Fatalf("read-only network summary = %q", got)
	}

	workspace := sandbox.WorkspaceWritePermissionProfile()
	workspace.SandboxPolicy.ExcludeTmpdirEnvVar = true
	workspace.SandboxPolicy.ExcludeSlashTmp = true
	workspace.NetworkEnabled = true
	if got := StatusPermissionSummary(&workspace, cwd, []*utils.PathURI{cwd}); got != "workspace with network access" {
		t.Fatalf("workspace network summary = %q", got)
	}

	custom, err := sandbox.ParseRuntimePermissionProfileJSON(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"tmpdir"}},"access":"write"}]},"network":"enabled"}`)
	if err != nil {
		t.Fatalf("parse custom runtime profile: %v", err)
	}
	if got := StatusPermissionSummary(custom, cwd, []*utils.PathURI{cwd}); got != "custom permissions with network access" {
		t.Fatalf("custom network summary = %q", got)
	}
}

func mustStatusPathURI(t *testing.T, raw string) *utils.PathURI {
	t.Helper()
	path, err := utils.Parse(raw)
	if err != nil {
		t.Fatalf("parse path URI %q: %v", raw, err)
	}
	return path
}
