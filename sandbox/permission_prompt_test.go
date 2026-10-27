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

func TestPermissionPromptApprovedPrefixTemplatesLikeRust(t *testing.T) {
	prefixes := [][]string{{"git", "status"}}
	empty := ""
	custom := "## Custom prefixes\n{{ approved_prefixes }}"
	catalogApproval := "Catalog approval instructions."

	defaultText := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:          PermissionPromptApprovalOnRequest,
		ApprovedCommandPrefixes: prefixes,
	})
	if !strings.Contains(defaultText, "## Approved command prefixes") {
		t.Fatalf("default prefix template missing:\n%s", defaultText)
	}
	suppressed := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:          PermissionPromptApprovalOnRequest,
		ApprovedCommandPrefixes: prefixes,
		PermissionMessages:      &PermissionPromptPermissionMessages{ApprovedPrefixes: &empty},
	})
	if strings.Contains(suppressed, "Approved command prefixes") || strings.Contains(suppressed, "- [\"git\", \"status\"]") {
		t.Fatalf("empty prefix template did not suppress the section:\n%s", suppressed)
	}
	customText := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:          PermissionPromptApprovalOnRequest,
		ApprovedCommandPrefixes: prefixes,
		PermissionMessages:      &PermissionPromptPermissionMessages{ApprovedPrefixes: &custom},
	})
	if !strings.Contains(customText, "## Custom prefixes") || !strings.Contains(customText, "- [\"git\", \"status\"]") {
		t.Fatalf("custom prefix template = \n%s", customText)
	}
	withCatalogApproval := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:          PermissionPromptApprovalOnRequest,
		ApprovedCommandPrefixes: prefixes,
		ApprovalMessages:        &PermissionPromptApprovalMessages{OnRequest: &catalogApproval},
		PermissionMessages:      &PermissionPromptPermissionMessages{ApprovedPrefixes: &custom},
	})
	if !strings.Contains(withCatalogApproval, catalogApproval) || !strings.Contains(withCatalogApproval, "## Custom prefixes") {
		t.Fatalf("catalog approval + prefix template = \n%s", withCatalogApproval)
	}
	granularText := BuildPermissionPrompt(&PermissionPromptConfig{
		ApprovalPolicy:          PermissionPromptApprovalGranular,
		ApprovedCommandPrefixes: prefixes,
		PermissionMessages:      &PermissionPromptPermissionMessages{ApprovedPrefixes: &custom},
	})
	if !strings.Contains(granularText, "## Custom prefixes") {
		t.Fatalf("granular custom prefix template = \n%s", granularText)
	}
}

// TestPermissionPromptCatalogWritableRootsLikeRust mirrors Rust #56309: the
// catalog singular/plural sentence is selected by the displayed root count and
// substitutes exactly one {{ roots }} placeholder, preserving path facts.
func TestPermissionPromptCatalogWritableRootsLikeRust(t *testing.T) {
	singular := "Catalog root: {{ roots }}."
	plural := "Catalog roots: {{ roots }}."
	messages := &PermissionPromptPermissionMessages{WritableRoot: &singular, WritableRoots: &plural}

	one := writableRootsText([]string{"/repo"}, messages)
	if one != "Catalog root: `/repo`." {
		t.Fatalf("singular catalog roots = %q", one)
	}
	two := writableRootsText([]string{"/b", "/a"}, messages)
	if two != "Catalog roots: `/a`, `/b`." {
		t.Fatalf("plural catalog roots = %q", two)
	}
	if got := writableRootsText([]string{"/repo"}, nil); got != " The writable root is `/repo`." {
		t.Fatalf("bundled singular roots = %q", got)
	}

	for _, invalid := range []string{
		"",
		"   ",
		"Catalog root without a placeholder.",
		"Catalog {{ roots }} {{ roots }}.",
		"Catalog {{ other }}.",
		"Catalog {{ roots }}{ literal }.",
		"Catalog\nroot {{ roots }}.",
		"Catalog " + PermissionInstructionsOpenTag + " {{ roots }}.",
		strings.Repeat("x", maxWritableRootsTemplateBytes+1) + " {{ roots }}",
	} {
		value := invalid
		got := writableRootsText([]string{"/repo"}, &PermissionPromptPermissionMessages{WritableRoot: &value})
		if got != " The writable root is `/repo`." {
			t.Fatalf("invalid writable_root %q rendered %q", invalid, got)
		}
	}

	// Rendering beyond the shared path+template budget is rejected even when
	// the template itself is within its byte limit.
	if _, ok := renderWritableRootsOverride("Roots: {{ roots }}", strings.Repeat("z", maxPermissionPromptPathBytes+maxWritableRootsTemplateBytes)); ok {
		t.Fatal("an oversized rendered writable-root sentence was accepted")
	}

	// Truncation happens first: one surviving root selects the singular text.
	roots := []string{strings.Repeat("x", maxPermissionPromptPathBytes), "/short"}
	text := BuildPermissionPrompt(&PermissionPromptConfig{
		SandboxMode:        PermissionPromptWorkspaceWrite,
		NetworkAccess:      PermissionPromptNetworkRestricted,
		ApprovalPolicy:     PermissionPromptApprovalNever,
		PermissionMessages: messages,
		WritableRoots:      roots,
	})
	if !strings.Contains(text, "Catalog root: `/short`.") || strings.Contains(text, "Catalog roots:") {
		t.Fatalf("truncated writable roots = %q", text)
	}
	if !strings.Contains(text, permissionPromptOmittedPathsText) {
		t.Fatalf("truncated writable roots lost the omission warning: %q", text)
	}
}

// TestPermissionPromptCatalogDeniedReadsLikeRust mirrors Rust #57263: the
// catalog template must contain exactly one {{ denied_reads }} placeholder and
// keeps the harness-owned policy, facts, and path list.
func TestPermissionPromptCatalogDeniedReadsLikeRust(t *testing.T) {
	template := "Blocked reads:\n{{ denied_reads }}"
	text := deniedReadsText([]string{"/private", "/seed"}, []string{"/root/**"}, &PermissionPromptPermissionMessages{DeniedReads: &template})
	want := "Blocked reads:\n" + permissionPromptDeniedReadsPolicy + "\n- path `/private`\n- path `/seed`\n- glob `/root/**`"
	if text != want {
		t.Fatalf("denied reads = %q, want %q", text, want)
	}

	for _, invalid := range []string{
		"",
		"   ",
		"Blocked reads without a placeholder",
		"{{ denied_reads }} and {{ denied_reads }}",
		"Blocked {{ other }}: {{ denied_reads }}",
		"Blocked {literal}: {{ denied_reads }}",
		"Blocked\x01: {{ denied_reads }}",
		"Blocked\u2028: {{ denied_reads }}",
		strings.Repeat("x", maxPermissionPresentationBytes+1) + "{{ denied_reads }}",
		"Blocked: {{ denied_reads }} " + strings.Repeat("z", maxPermissionPresentationBytes),
		"Blocked: {{ denied_reads }} " + PermissionInstructionsCloseTag,
	} {
		value := invalid
		got := deniedReadsText([]string{"/private"}, nil, &PermissionPromptPermissionMessages{DeniedReads: &value})
		if got != "## Denied filesystem reads\n"+permissionPromptDeniedReadsPolicy+"\n- path `/private`" {
			t.Fatalf("invalid denied_reads %q rendered %q", invalid, got)
		}
	}

	// A bare placeholder is valid and keeps the catalog's exact framing.
	bare := "{{ denied_reads }}"
	if got, want := deniedReadsText([]string{"/private"}, nil, &PermissionPromptPermissionMessages{DeniedReads: &bare}), permissionPromptDeniedReadsPolicy+"\n- path `/private`"; got != want {
		t.Fatalf("bare denied_reads = %q, want %q", got, want)
	}
}

// TestPermissionPromptCatalogOmittedPathsLikeRust mirrors Rust #57263: catalog
// presentation only precedes the mandatory omission/no-bypass warning, and
// invalid overrides fall back independently.
func TestPermissionPromptCatalogOmittedPathsLikeRust(t *testing.T) {
	presentation := "Some extra permission paths are omitted from this prompt."
	got := omittedPathsText(&PermissionPromptPermissionMessages{OmittedPaths: &presentation})
	want := presentation + "\n" + permissionPromptOmittedPathsText
	if got != want {
		t.Fatalf("omitted paths = %q, want %q", got, want)
	}
	for _, invalid := range []string{
		"",
		"   ",
		"Has {literal} braces",
		"Has " + PermissionInstructionsOpenTag,
		"Line\nbreak is allowed",
	} {
		value := invalid
		got := omittedPathsText(&PermissionPromptPermissionMessages{OmittedPaths: &value})
		if invalid == "Line\nbreak is allowed" {
			if got != invalid+"\n"+permissionPromptOmittedPathsText {
				t.Fatalf("newline presentation = %q", got)
			}
			continue
		}
		if got != permissionPromptOmittedPathsText {
			t.Fatalf("invalid omitted_paths %q rendered %q", invalid, got)
		}
	}
	oversized := strings.Repeat("x", maxPermissionPresentationBytes+1)
	if got := omittedPathsText(&PermissionPromptPermissionMessages{OmittedPaths: &oversized}); got != permissionPromptOmittedPathsText {
		t.Fatalf("oversized omitted_paths rendered %q", got)
	}
	if got := omittedPathsText(&PermissionPromptPermissionMessages{OmittedPaths: &presentation}); !strings.HasSuffix(got, permissionPromptOmittedPathsText) {
		t.Fatalf("omitted_paths lost the mandatory warning: %q", got)
	}
}
