package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// TestPrepareApplyPatchReportsApprovalWithoutWritingLikeRust covers Rust
// #58899: preparing a patch verifies it and reports the live approval
// requirement while leaving the filesystem untouched.
func TestPrepareApplyPatchReportsApprovalWithoutWritingLikeRust(t *testing.T) {
	dir := t.TempDir()
	newInvocation := func() *Invocation {
		return &Invocation{
			CallID:   "patch-prepare",
			ToolName: PlainName(DefaultApplyPatchToolName),
			Payload:  Payload{Kind: PayloadCustom, Input: "*** Begin Patch\n*** Add File: prepared.txt\n+prepared\n*** End Patch"},
		}
	}
	target := filepath.Join(dir, "prepared.txt")

	disabled := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:               dir,
		PermissionProfile: &sandbox.PermissionProfile{Disabled: true},
	})
	prepared, err := disabled.PrepareApplyPatch(context.Background(), newInvocation())
	if err != nil {
		t.Fatalf("PrepareApplyPatch() error = %v", err)
	}
	if prepared.ApprovalRequired {
		t.Fatal("a disabled profile auto-approves patches")
	}
	if prepared.Request == nil || prepared.Action == nil {
		t.Fatalf("preparation = %#v, want the verified request", prepared)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("preparation wrote the patch target: %v", err)
	}

	workspace := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD: dir,
		PermissionProfile: &sandbox.PermissionProfile{SandboxPolicy: &sandbox.SandboxPolicy{
			Kind:          sandbox.SandboxWorkspaceWrite,
			WritableRoots: []string{dir},
		}},
	})
	prepared, err = workspace.PrepareApplyPatch(context.Background(), newInvocation())
	if err != nil {
		t.Fatalf("PrepareApplyPatch(workspace) error = %v", err)
	}
	if !prepared.ApprovalRequired {
		t.Fatal("a local workspace patch keeps the conservative approval path")
	}

	if _, err := disabled.Execute(context.Background(), newInvocation()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("live execution did not apply the prepared patch: %v", err)
	}
}
