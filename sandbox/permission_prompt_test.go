package sandbox

import (
	"fmt"
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

func TestFormatAllowPrefixesRendersRustPrefixList(t *testing.T) {
	got := FormatAllowPrefixes([][]string{{"echo", "amendment-ok"}, {"git", "pull"}})
	// Rust sorts by combined token byte length: "git pull" (7) before
	// "echo amendment-ok" (19).
	want := "- [\"git\", \"pull\"]\n- [\"echo\", \"amendment-ok\"]"
	if got != want {
		t.Fatalf("FormatAllowPrefixes() = %q, want %q", got, want)
	}
}

func TestFormatAllowPrefixesSortsLikeRust(t *testing.T) {
	// Rust sorts by token count, then combined token byte length, then
	// lexicographically.
	got := FormatAllowPrefixes([][]string{
		{"git", "pull"},
		{"go", "test"},
		{"git", "pull", "origin"},
		{"go"},
	})
	want := "- [\"go\"]\n- [\"go\", \"test\"]\n- [\"git\", \"pull\"]\n- [\"git\", \"pull\", \"origin\"]"
	if got != want {
		t.Fatalf("FormatAllowPrefixes() = %q, want %q", got, want)
	}
}

func TestFormatAllowPrefixesTruncatesLikeRust(t *testing.T) {
	many := make([][]string, 0, maxRenderedPrefixes+1)
	for index := 0; index < maxRenderedPrefixes+1; index++ {
		many = append(many, []string{"tool", fmt.Sprintf("token-%03d", index)})
	}
	got := FormatAllowPrefixes(many)
	if !strings.Contains(got, truncatedPrefixesMarker) {
		t.Fatalf("truncation marker missing from %d-prefix output", len(many))
	}
	if strings.Count(got, "- [") != maxRenderedPrefixes {
		t.Fatalf("rendered %d prefixes, want %d", strings.Count(got, "- ["), maxRenderedPrefixes)
	}

	long := FormatAllowPrefixes([][]string{{strings.Repeat("x", maxAllowPrefixTextChars)}})
	if !strings.Contains(long, truncatedPrefixesMarker) {
		t.Fatalf("long prefix did not truncate")
	}
}
