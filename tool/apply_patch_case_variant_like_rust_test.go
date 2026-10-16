package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestApplyPatchCaseVariantMetadataCannotWriteWithoutApprovalLikeRust mirrors
// the freeform half of Rust #54250
// (`case_variant_metadata_patch_cannot_write_without_approval` in
// core/tests/suite/apply_patch_system_aliases.rs): with approvals disabled and a
// workspace-write permission profile, a patch that updates `.git/config`
// through its case alias `.GIT/config` is refused, while the very same patch
// against an ordinary workspace file applies.
//
// Rust's fixture returns early when the alias does not exist (a
// case-sensitive filesystem), so this test skips there as well.
func TestApplyPatchCaseVariantMetadataCannotWriteWithoutApprovalLikeRust(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git) error = %v", err)
	}
	for _, path := range []string{filepath.Join(cwd, ".git", "config"), filepath.Join(cwd, "notes.txt")} {
		if err := os.WriteFile(path, []byte("# original\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, ".GIT", "config")); err != nil {
		t.Skip("the reported alias requires a case-insensitive filesystem")
	}

	policy := sandbox.NewWorkspaceWritePolicy()
	policy.ExcludeTmpdirEnvVar = true
	policy.ExcludeSlashTmp = true
	// No approval callback is wired, which is the disabled-approval path the
	// Rust fixture selects with `AskForApproval::Never`.
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: cwd, SandboxPolicy: policy})

	run := func(t *testing.T, callID string, patch string) *Output {
		t.Helper()
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID:   callID,
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadCustom, Input: patch},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		if output == nil {
			t.Fatalf("Execute(%s) = nil output", callID)
		}
		return output
	}

	control := "*** Begin Patch\n*** Update File: notes.txt\n@@\n-# original\n+# changed\n*** End Patch"
	if output := run(t, "case-variant-control", control); !output.Success {
		t.Fatalf("control patch output = %#v, want the ordinary workspace file to stay writable", output)
	}

	protected := "*** Begin Patch\n*** Update File: .GIT/config\n@@\n-# original\n+# changed\n*** End Patch"
	output := run(t, "case-variant-protected", protected)
	if output.Success || !strings.Contains(output.Body, "outside of the project workspace roots") {
		t.Fatalf("protected patch output = %#v, want the case alias of .git to be refused", output)
	}

	assertApplyPatchFile(t, filepath.Join(cwd, "notes.txt"), "# changed\n")
	assertApplyPatchFile(t, filepath.Join(cwd, ".git", "config"), "# original\n")
}
