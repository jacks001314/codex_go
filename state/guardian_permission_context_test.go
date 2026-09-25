package state

import (
	"errors"
	"strings"
	"testing"

	"codex_go/context"
)

// Mirrors Rust's `PermissionContext::body`: paths precede globs, the scope names
// the environment when the host resolved one, and an empty context still renders
// its explicit "no denied-read paths/globs" sentence.
func TestPermissionContextBodyMatchesRust(t *testing.T) {
	environment := "remote-1"
	cases := []struct {
		name    string
		context PermissionContext
		want    string
	}{
		{
			name:    "no evidence",
			context: PermissionContext{},
			want:    "The parent turn's active permission profile has no explicit denied-read paths/globs.\n",
		},
		{
			name:    "environment only",
			context: PermissionContext{EnvironmentID: &environment},
			want:    `The active permission profile for environment "remote-1" has no explicit denied-read paths/globs.` + "\n",
		},
		{
			name: "paths before globs",
			context: PermissionContext{
				DeniedGlobs: []string{"/repo/**/*.pem"},
				DeniedPaths: []string{"/repo/secret", "/repo/keys"},
			},
			want: "The parent turn's active permission profile denies reading these paths/globs. " +
				"These are policy restrictions; do not approve escalation whose purpose is to read them.\n" +
				"- path `/repo/secret`\n- path `/repo/keys`\n- glob `/repo/**/*.pem`\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.context.RenderPermissionContextBody(); got != testCase.want {
				t.Fatalf("body = %q, want %q", got, testCase.want)
			}
		})
	}
	if got := (*PermissionContext)(nil).RenderPermissionContextBody(); got != "" {
		t.Fatalf("nil body = %q, want empty", got)
	}
}

// Mirrors `PermissionContextSection::contribute`: nothing for an empty context,
// and the marked section otherwise. An environment id alone is evidence.
func TestPermissionContextSectionItemsMatchRust(t *testing.T) {
	if items := PermissionContextSectionItems(nil); items != nil {
		t.Fatalf("nil items = %#v, want none", items)
	}
	if items := PermissionContextSectionItems(&PermissionContext{}); items != nil {
		t.Fatalf("empty items = %#v, want none", items)
	}
	environment := "remote-1"
	items := PermissionContextSectionItems(&PermissionContext{EnvironmentID: &environment})
	if len(items) != 3 || items[0] != permissionContextStart || items[2] != permissionContextEnd {
		t.Fatalf("section items = %#v", items)
	}
	if !strings.Contains(items[1], "environment \"remote-1\"") {
		t.Fatalf("section body = %q", items[1])
	}

	denied := &PermissionContext{DeniedPaths: []string{"/repo/secret"}}
	items = PermissionContextSectionItems(denied)
	if len(items) != 3 || items[0] != permissionContextStart ||
		items[1] != denied.RenderPermissionContextBody() || items[2] != permissionContextEnd {
		t.Fatalf("denied section items = %#v", items)
	}
}

// Mirrors the asynchronous rule: evidence that would exceed one thousand
// estimated tokens is an error, so the scorer falls back to synchronous review
// rather than dropping restrictions.
func TestAsyncPermissionContextEvidenceLimitMatchesRust(t *testing.T) {
	fixed := len((&PermissionContext{DeniedPaths: []string{""}}).RenderPermissionContextBody())
	path := strings.Repeat("a", maxAsyncPermissionBytes-fixed)
	atLimit := &PermissionContext{DeniedPaths: []string{path}}
	if got := len(atLimit.RenderPermissionContextBody()); got != maxAsyncPermissionBytes {
		t.Fatalf("probe body = %d bytes, want %d", got, maxAsyncPermissionBytes)
	}
	if items, err := AsyncPermissionContextSectionItems(atLimit); err != nil || len(items) != 3 {
		t.Fatalf("at-limit async items = %#v / %v", items, err)
	}
	tooLarge := &PermissionContext{DeniedPaths: []string{path + "a"}}
	if _, err := AsyncPermissionContextSectionItems(tooLarge); !errors.Is(err, ErrPermissionContextTooLarge) {
		t.Fatalf("over-limit async error = %v, want the evidence limit", err)
	}
	if _, err := AsyncPermissionContextSectionItems(nil); err != nil {
		t.Fatalf("nil async error = %v", err)
	}
}

// The permission context is part of the review prompt and sits after the
// node-repl evidence and before the planned action, matching Rust's registry
// order.
func TestGuardianPromptIncludesPermissionContextLikeRust(t *testing.T) {
	evidence := &context.NodeReplReviewEvidence{}
	evidence.Record("js", "cell-1", "call-1", []string{"evidence-text"})
	fragment := evidence.SnapshotSince(0)
	if fragment == nil {
		t.Fatal("expected evidence fragment")
	}
	action := Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"}
	prompt, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{
		NodeReplEvidence:  fragment,
		PermissionContext: &PermissionContext{DeniedPaths: []string{"/repo/secret"}},
	})
	if err != nil {
		t.Fatalf("BuildPromptWithOptions() error = %v", err)
	}
	evidenceAt := strings.Index(prompt, "<node_repl_review_evidence>")
	permissionAt := strings.Index(prompt, permissionContextStart)
	actionAt := strings.Index(prompt, ">>> APPROVAL REQUEST START")
	if evidenceAt < 0 || permissionAt < 0 || actionAt < 0 {
		t.Fatalf("prompt is missing a section:\n%s", prompt)
	}
	if !(evidenceAt < permissionAt && permissionAt < actionAt) {
		t.Fatalf("section order = evidence:%d permission:%d action:%d\n%s", evidenceAt, permissionAt, actionAt, prompt)
	}
	if !strings.Contains(prompt, "- path `/repo/secret`") || !strings.Contains(prompt, permissionContextEnd) {
		t.Fatalf("permission evidence missing from prompt:\n%s", prompt)
	}

	without, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{})
	if err != nil {
		t.Fatalf("BuildPromptWithOptions() error = %v", err)
	}
	if strings.Contains(without, "PARENT TURN PERMISSION CONTEXT") {
		t.Fatalf("empty permission context rendered a section:\n%s", without)
	}
}
