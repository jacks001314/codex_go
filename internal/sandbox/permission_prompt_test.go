package sandbox

import (
	"strings"
	"testing"
)

func TestBuildWorkspaceOnRequest(t *testing.T) {
	text := BuildPermissionPrompt(&PermissionPromptConfig{
		SandboxMode:                    PermissionPromptWorkspaceWrite,
		NetworkAccess:                  PermissionPromptNetworkRestricted,
		ApprovalPolicy:                 PermissionPromptApprovalOnRequest,
		RequestPermissionsToolEnabled:  true,
		ApprovedCommandPrefixes:        [][]string{{"go", "test"}},
		WritableRoots:                  []string{"/repo"},
		DeniedReadGlobs:                []string{"**/.env"},
		ExecPermissionApprovalsEnabled: true,
	})
	for _, want := range []string{"workspace-write", "request_permissions", "[\"go\", \"test\"]", "`/repo`", "glob `**/.env`"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
}

func TestBuildNever(t *testing.T) {
	text := BuildPermissionPrompt(&PermissionPromptConfig{SandboxMode: PermissionPromptReadOnly, ApprovalPolicy: PermissionPromptApprovalNever})
	if !strings.Contains(text, "read-only") || !strings.Contains(text, "`never`") {
		t.Fatalf("unexpected text: %s", text)
	}
}

func TestBuildGranular(t *testing.T) {
	text := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:                PermissionPromptApprovalGranular,
		GranularAllowedCategories:     []string{"rules"},
		GranularAutomaticallyRejected: []string{"skill_approval"},
		ApprovalsReviewer:             PermissionPromptAutoReview,
	})
	for _, want := range []string{"granular", "`rules`", "`skill_approval`", "auto_review"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
}
