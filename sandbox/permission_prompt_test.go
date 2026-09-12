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
	// Mirrors Rust: the sandbox template (trimmed) then the approval template
	// (which keeps its trailing newline), preceded by the section separator.
	want := "\nFilesystem sandboxing defines which files can be read or written. " +
		"`sandbox_mode` is `read-only`: The sandbox only permits reading files. Network access is restricted." +
		"\nApproval policy is currently never. Do not provide the `sandbox_permissions` for any reason, commands will be rejected.\n"
	if text != want {
		t.Fatalf("BuildPermissionPrompt() = %q, want %q", text, want)
	}
}

// TestSandboxPromptTextMatchesRustTemplates mirrors Rust's
// renders_sandbox_mode_text vectors.
func TestSandboxPromptTextMatchesRustTemplates(t *testing.T) {
	tests := []struct {
		mode    PermissionPromptSandboxMode
		network PermissionPromptNetworkAccess
		want    string
	}{
		{
			mode:    PermissionPromptWorkspaceWrite,
			network: PermissionPromptNetworkRestricted,
			want: "Filesystem sandboxing defines which files can be read or written. `sandbox_mode` is `workspace-write`: " +
				"The sandbox permits reading files, and editing files in `cwd` and `writable_roots`. " +
				"Editing files in other directories requires approval. Network access is restricted.",
		},
		{
			mode:    PermissionPromptReadOnly,
			network: PermissionPromptNetworkRestricted,
			want: "Filesystem sandboxing defines which files can be read or written. `sandbox_mode` is `read-only`: " +
				"The sandbox only permits reading files. Network access is restricted.",
		},
		{
			mode:    PermissionPromptDangerFullAccess,
			network: PermissionPromptNetworkEnabled,
			want: "Filesystem sandboxing defines which files can be read or written. `sandbox_mode` is `danger-full-access`: " +
				"No filesystem sandboxing - all commands are permitted. Network access is enabled.",
		},
	}
	for _, testCase := range tests {
		t.Run(string(testCase.mode), func(t *testing.T) {
			if got := sandboxPromptText(testCase.mode, testCase.network, nil); got != testCase.want {
				t.Fatalf("sandboxPromptText() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestSandboxPromptTextCatalogMessagesLikeRust mirrors Rust
// catalog_permission_messages_select_sandbox_mode_and_render_network_access,
// missing_catalog_permission_message_uses_legacy_sandbox_text,
// invalid_catalog_permission_message_is_preserved_verbatim and
// catalog_permission_message_renders_network_access_and_preserves_other_placeholders.
func TestSandboxPromptTextCatalogMessagesLikeRust(t *testing.T) {
	danger := "catalog danger"
	workspace := "catalog workspace {{ network_access }}"
	readOnly := "catalog read only {{ network_access }}"
	messages := &PermissionPromptPermissionMessages{
		DangerFullAccess: &danger,
		WorkspaceWrite:   &workspace,
		ReadOnly:         &readOnly,
	}
	for _, testCase := range []struct {
		mode PermissionPromptSandboxMode
		want string
	}{
		{PermissionPromptDangerFullAccess, "catalog danger"},
		{PermissionPromptWorkspaceWrite, "catalog workspace enabled"},
		{PermissionPromptReadOnly, "catalog read only enabled"},
	} {
		if got := sandboxPromptText(testCase.mode, PermissionPromptNetworkEnabled, messages); got != testCase.want {
			t.Fatalf("sandboxPromptText(%s) = %q, want %q", testCase.mode, got, testCase.want)
		}
	}

	// A missing catalog message falls back to the built-in template.
	legacy := sandboxPromptText(PermissionPromptWorkspaceWrite, PermissionPromptNetworkRestricted, nil)
	partial := &PermissionPromptPermissionMessages{ReadOnly: &readOnly}
	if got := sandboxPromptText(PermissionPromptWorkspaceWrite, PermissionPromptNetworkRestricted, partial); got != legacy {
		t.Fatalf("missing catalog message = %q, want the legacy template %q", got, legacy)
	}

	// Invalid placeholders are preserved verbatim.
	for _, invalid := range []string{"{{ unterminated", "{{ unsupported }}"} {
		value := invalid
		got := sandboxPromptText(
			PermissionPromptWorkspaceWrite,
			PermissionPromptNetworkRestricted,
			&PermissionPromptPermissionMessages{WorkspaceWrite: &value},
		)
		if got != invalid {
			t.Fatalf("invalid catalog message = %q, want %q", got, invalid)
		}
	}

	// Only the exact placeholder is replaced.
	source := "network={{ network_access }} compact={{network_access}} other={{ other }}"
	got := sandboxPromptText(
		PermissionPromptWorkspaceWrite,
		PermissionPromptNetworkRestricted,
		&PermissionPromptPermissionMessages{WorkspaceWrite: &source},
	)
	if got != "network=restricted compact={{network_access}} other={{ other }}" {
		t.Fatalf("placeholder rendering = %q", got)
	}

	// An empty catalog message drops the sandbox section but keeps the rest.
	empty := ""
	text := BuildPermissionPrompt(&PermissionPromptConfig{
		SandboxMode:        PermissionPromptWorkspaceWrite,
		NetworkAccess:      PermissionPromptNetworkRestricted,
		ApprovalPolicy:     PermissionPromptApprovalNever,
		PermissionMessages: &PermissionPromptPermissionMessages{WorkspaceWrite: &empty},
		WritableRoots:      []string{"/tmp/repo"},
	})
	if strings.Contains(text, "Filesystem sandboxing defines") {
		t.Fatalf("empty catalog message kept the sandbox section: %q", text)
	}
	if !strings.Contains(text, "Approval policy is currently never") || !strings.Contains(text, "/tmp/repo") {
		t.Fatalf("empty catalog message dropped other sections: %q", text)
	}
}

// TestApprovalPromptTextCatalogAndReviewerLikeRust mirrors Rust's approval
// selection: catalog messages win per policy (and per reviewer for on-request),
// Granular has no catalog variant, and the auto-review suffix only applies when
// approvals are possible.
func TestApprovalPromptTextCatalogAndReviewerLikeRust(t *testing.T) {
	onRequest := "catalog on request"
	onRequestAuto := "catalog on request auto review"
	never := "catalog never"
	unlessTrusted := "catalog unless trusted"
	messages := &PermissionPromptApprovalMessages{
		OnRequest:           &onRequest,
		OnRequestAutoReview: &onRequestAuto,
		Never:               &never,
		UnlessTrusted:       &unlessTrusted,
	}
	tests := []struct {
		name   string
		config PermissionPromptConfig
		want   string
	}{
		{
			name:   "on request user",
			config: PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalOnRequest, ApprovalMessages: messages},
			want:   "catalog on request",
		},
		{
			name:   "on request auto review",
			config: PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalOnRequest, ApprovalsReviewer: PermissionPromptAutoReview, ApprovalMessages: messages},
			want:   "catalog on request auto review",
		},
		{
			name:   "never",
			config: PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalNever, ApprovalsReviewer: PermissionPromptAutoReview, ApprovalMessages: messages},
			want:   "catalog never",
		},
		{
			name:   "unless trusted",
			config: PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalUnlessTrusted, ApprovalMessages: messages},
			want:   "catalog unless trusted",
		},
		{
			name:   "granular ignores catalog messages",
			config: PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalGranular, ApprovalMessages: messages},
			want:   "# Approval Requests\n\nApproval policy is `granular`. Categories set to `false` are automatically rejected instead of prompting the user.",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			config := testCase.config
			if got := approvalPromptText(&config); got != testCase.want {
				t.Fatalf("approvalPromptText() = %q, want %q", got, testCase.want)
			}
		})
	}

	// The built-in on-request text gains the auto-review suffix.
	config := PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalOnRequest, ApprovalsReviewer: PermissionPromptAutoReview}
	text := approvalPromptText(&config)
	if !strings.Contains(text, "`approvals_reviewer` is `auto_review`") {
		t.Fatalf("auto-review suffix missing: %q", text)
	}
	if !strings.Contains(text, "How to request escalation") {
		t.Fatalf("on-request guidance missing: %q", text)
	}
	// Rust does not add the suffix for the never policy.
	neverConfig := PermissionPromptConfig{ApprovalPolicy: PermissionPromptApprovalNever, ApprovalsReviewer: PermissionPromptAutoReview}
	if got := approvalPromptText(&neverConfig); strings.Contains(got, "auto_review") {
		t.Fatalf("auto-review suffix applied to never: %q", got)
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
