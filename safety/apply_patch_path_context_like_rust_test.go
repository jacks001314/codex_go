package safety

import (
	"testing"

	"codex_go/utils"
)

func mustPathURI(t *testing.T, value string) *utils.PathURI {
	t.Helper()
	uri, ok := utils.InferredPathURI(value)
	if !ok || uri == nil {
		t.Fatalf("InferredPathURI(%q) failed", value)
	}
	return uri
}

// TestPatchWritabilityUsesExecutorPathConventionLikeRust mirrors the Windows
// executor URI half of Rust #42391: a patch target spelled with the executor's
// convention is classified against the executor workspace even when the Codex
// host uses a different convention.
func TestPatchWritabilityUsesExecutorPathConventionLikeRust(t *testing.T) {
	context := FileSystemPolicyContext{
		CWD:                    mustPathURI(t, "file:///C:/workspace"),
		ProtectedMetadataNames: []string{".git", ".agents"},
	}
	policy := FileSystemPolicy{WritableRoots: []string{"."}}

	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "relative target", target: "notes.txt", want: true},
		{name: "absolute target", target: `C:\workspace\notes.txt`, want: true},
		{name: "parent traversal stays inside", target: `C:\workspace\sub\..\notes.txt`, want: true},
		{name: "case-insensitive drive and root", target: `c:\WORKSPACE\notes.txt`, want: true},
		{name: "outside workspace", target: `C:\outside\notes.txt`, want: false},
		{name: "other drive", target: `D:\workspace\notes.txt`, want: false},
		{name: "protected metadata", target: `C:\workspace\.git\config`, want: false},
		{name: "protected metadata case alias", target: `C:\workspace\.GIT\config`, want: false},
		{name: "metadata name below subdirectory is writable", target: `C:\workspace\src\.git\config`, want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target, ok := context.ResolvePath(testCase.target)
			if !ok {
				t.Fatalf("ResolvePath(%q) failed", testCase.target)
			}
			if got := CanWritePathInContext(&policy, context, target); got != testCase.want {
				t.Fatalf("CanWritePathInContext(%q) = %v, want %v", testCase.target, got, testCase.want)
			}
		})
	}
}

// TestWritePatchConstrainedToWritablePathsInContextLikeRust covers the
// PathUri-based containment check: every change (including a move target) must
// be writable under the executor convention.
func TestWritePatchConstrainedToWritablePathsInContextLikeRust(t *testing.T) {
	context := FileSystemPolicyContext{CWD: mustPathURI(t, "file:///C:/workspace")}
	policy := FileSystemPolicy{WritableRoots: []string{"."}}

	inside := &PatchAction{Changes: []FileChange{{Kind: FileUpdate, Path: `C:\workspace\notes.txt`}}}
	if !WritePatchConstrainedToWritablePathsInContext(inside, &policy, context) {
		t.Fatal("inside workspace patch reported as outside")
	}
	outside := &PatchAction{Changes: []FileChange{{Kind: FileAdd, Path: `C:\outside\notes.txt`}}}
	if WritePatchConstrainedToWritablePathsInContext(outside, &policy, context) {
		t.Fatal("outside workspace patch reported as writable")
	}
	moveOutside := &PatchAction{Changes: []FileChange{{
		Kind:     FileUpdate,
		Path:     `C:\workspace\notes.txt`,
		MovePath: `C:\outside\notes.txt`,
	}}}
	if WritePatchConstrainedToWritablePathsInContext(moveOutside, &policy, context) {
		t.Fatal("patch moving outside the workspace reported as writable")
	}
	fullDisk := FileSystemPolicy{FullDiskWrite: true}
	outsideWithFullDisk := &PatchAction{Changes: []FileChange{{Kind: FileAdd, Path: `C:\outside\notes.txt`}}}
	if !WritePatchConstrainedToWritablePathsInContext(outsideWithFullDisk, &fullDisk, context) {
		t.Fatal("full-disk policy rejected an outside target")
	}
}

// TestPatchRejectionReasonUsesConfiguredWritableRootsInContextLikeRust covers
// the read-only versus outside-project rejection reasons Rust #42391 derives
// from `has_configured_writable_roots(context)`.
func TestPatchRejectionReasonUsesConfiguredWritableRootsInContextLikeRust(t *testing.T) {
	context := FileSystemPolicyContext{CWD: mustPathURI(t, "file:///C:/workspace")}
	readOnly := FileSystemPolicy{}
	check := AssessPatchSafetyWithContext(
		&PatchAction{Changes: []FileChange{{Kind: FileAdd, Path: `C:\outside\notes.txt`}}},
		ApprovalPolicy{Mode: ApprovalNever},
		PermissionManaged,
		readOnly,
		context,
		"",
	)
	if check.Kind != SafetyReject || check.Reason != PatchRejectedReadOnlyReason {
		t.Fatalf("read-only check = %+v, want rejection with the read-only reason", check)
	}
	workspace := FileSystemPolicy{WritableRoots: []string{"."}}
	check = AssessPatchSafetyWithContext(
		&PatchAction{Changes: []FileChange{{Kind: FileAdd, Path: `C:\outside\notes.txt`}}},
		ApprovalPolicy{Mode: ApprovalNever},
		PermissionManaged,
		workspace,
		context,
		"",
	)
	if check.Kind != SafetyReject || check.Reason != PatchRejectedOutsideProjectReason {
		t.Fatalf("workspace check = %+v, want rejection with the outside-project reason", check)
	}
	inside := AssessPatchSafetyWithContext(
		&PatchAction{Changes: []FileChange{{Kind: FileAdd, Path: `C:\workspace\notes.txt`}}},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		PermissionManaged,
		workspace,
		context,
		"landlock",
	)
	if inside.Kind != SafetyAutoApprove || inside.SandboxType != "landlock" {
		t.Fatalf("inside check = %+v, want auto approval with the platform sandbox", inside)
	}
}
