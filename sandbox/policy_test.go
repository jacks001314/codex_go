package sandbox

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseSandboxMode(t *testing.T) {
	mode, err := ParseSandboxMode("workspace-write")
	if err != nil {
		t.Fatalf("ParseSandboxMode returned error: %v", err)
	}
	if mode != SandboxWorkspaceWrite {
		t.Fatalf("mode = %q", mode)
	}
	if _, err := ParseSandboxMode("unknown"); err == nil {
		t.Fatal("ParseSandboxMode returned nil error, want failure")
	}
}

func TestSandboxPolicyAccessFlags(t *testing.T) {
	readOnly := NewReadOnlyPolicy()
	if !readOnly.HasFullDiskReadAccess() || readOnly.HasFullDiskWriteAccess() || readOnly.HasFullNetworkAccess() {
		t.Fatalf("read-only flags are wrong")
	}

	workspace := NewWorkspaceWritePolicy()
	if workspace.HasFullDiskWriteAccess() || workspace.HasFullNetworkAccess() {
		t.Fatalf("workspace flags are wrong")
	}

	full := NewDangerFullAccessPolicy()
	if !full.HasFullDiskWriteAccess() || !full.HasFullNetworkAccess() {
		t.Fatalf("full access flags are wrong")
	}

	external := NewExternalSandboxPolicy(NetworkEnabled)
	if !external.HasFullDiskWriteAccess() || !external.HasFullNetworkAccess() {
		t.Fatalf("external sandbox flags are wrong")
	}
}

func TestWorkspaceWritableRootsIncludeCWDAndProtectMetadata(t *testing.T) {
	cwd := t.TempDir()
	policy := NewWorkspaceWritePolicy()
	roots := policy.GetWritableRootsWithCWD(cwd)
	if len(roots) == 0 {
		t.Fatal("roots is empty")
	}
	var workspace *WritableRoot
	for i := range roots {
		if roots[i].Root == cleanAbs(cwd) {
			workspace = &roots[i]
			break
		}
	}
	if workspace == nil {
		t.Fatalf("workspace root missing from %#v", roots)
	}
	if !workspace.IsPathWritable(filepath.Join(cwd, "src", "main.go")) {
		t.Fatal("workspace file should be writable")
	}
	for _, directory := range []string{".git", ".agents", ".codex"} {
		if workspace.IsPathWritable(filepath.Join(cwd, directory, "protected.txt")) {
			t.Fatalf("%s should be read-only under workspace root", directory)
		}
	}
}

func TestWorkspaceWritableRootsHonorExclusions(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cwd := t.TempDir()
	policy := NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	roots := policy.GetWritableRootsWithCWD(cwd)
	if len(roots) != 1 || roots[0].Root != cleanAbs(cwd) {
		t.Fatalf("roots = %#v", roots)
	}
}

func TestWindowsWorkspaceWritableRootsDoNotTreatSlashTmpAsTempDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific symbolic slash_tmp semantics")
	}
	t.Setenv("TMPDIR", "")
	cwd := t.TempDir()
	policy := NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	roots := policy.GetWritableRootsWithCWD(cwd)
	if len(roots) != 1 || roots[0].Root != cleanAbs(cwd) {
		t.Fatalf("roots = %#v, want only cwd", roots)
	}

	policy.WritableRoots = []string{"/tmp"}
	roots = policy.GetWritableRootsWithCWD("")
	if len(roots) != 1 || roots[0].Root != cleanAbs("/tmp") {
		t.Fatalf("literal /tmp roots = %#v", roots)
	}
}

func TestSandboxPolicyUnmarshalRustV2Shape(t *testing.T) {
	var full SandboxPolicy
	if err := json.Unmarshal([]byte(`{"type":"dangerFullAccess"}`), &full); err != nil {
		t.Fatalf("Unmarshal(full) error = %v", err)
	}
	if full.Kind != SandboxDangerFullAccess || !full.HasFullNetworkAccess() {
		t.Fatalf("full = %#v", full)
	}

	var workspace SandboxPolicy
	if err := json.Unmarshal([]byte(`{"type":"workspaceWrite","writableRoots":["/workspace"],"networkAccess":true,"excludeTmpdirEnvVar":true,"excludeSlashTmp":true}`), &workspace); err != nil {
		t.Fatalf("Unmarshal(workspace) error = %v", err)
	}
	if workspace.Kind != SandboxWorkspaceWrite || !workspace.NetworkAccess || len(workspace.WritableRoots) != 1 || !workspace.ExcludeTmpdirEnvVar || !workspace.ExcludeSlashTmp {
		t.Fatalf("workspace = %#v", workspace)
	}

	var external SandboxPolicy
	if err := json.Unmarshal([]byte(`{"type":"externalSandbox","networkAccess":"enabled"}`), &external); err != nil {
		t.Fatalf("Unmarshal(external) error = %v", err)
	}
	if external.Kind != "external-sandbox" || external.ExternalNetwork != NetworkEnabled {
		t.Fatalf("external = %#v", external)
	}
}

func TestSandboxPolicyUnmarshalRejectsLegacyRestrictedReadAccess(t *testing.T) {
	var readOnly SandboxPolicy
	if err := json.Unmarshal([]byte(`{"type":"readOnly","access":{"type":"restricted"}}`), &readOnly); err == nil {
		t.Fatal("Unmarshal(readOnly restricted) returned nil error")
	}
	var workspace SandboxPolicy
	if err := json.Unmarshal([]byte(`{"type":"workspaceWrite","readOnlyAccess":{"type":"restricted"}}`), &workspace); err == nil {
		t.Fatal("Unmarshal(workspace restricted) returned nil error")
	}
}

func TestGranularApprovalConfig(t *testing.T) {
	config := &GranularApprovalConfig{
		SandboxApproval:    true,
		Rules:              true,
		SkillApproval:      false,
		RequestPermissions: true,
		MCPElicitations:    false,
	}
	if !config.AllowsSandboxApproval() || !config.AllowsRulesApproval() || !config.AllowsRequestPermissions() {
		t.Fatal("expected enabled granular approvals")
	}
	if config.AllowsSkillApproval() || config.AllowsMCPElicitations() {
		t.Fatal("expected disabled granular approvals")
	}
}
