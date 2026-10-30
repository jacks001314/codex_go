package tui

import (
	"reflect"
	"strings"
	"testing"
)

// Mirrors Rust #42640 git_action_directives::strips_and_parses_git_action_directives.
func TestStripAndParseGitActionDirectivesLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(
		`Done`+"\n\n"+`::git-stage{cwd="/repo"} ::git-push{cwd="/repo" branch="feat/x"} ::git-stage{cwd="C:\repo\"} ::git-create-branch{cwd="/tmp/repo\" branch="feat"}`,
		"/repo",
	)
	if parsed.VisibleMarkdown != "Done" {
		t.Fatalf("visible markdown = %q, want %q", parsed.VisibleMarkdown, "Done")
	}
	want := []GitAction{
		{Kind: GitActionStage, CWD: "/repo"},
		{Kind: GitActionPush, CWD: "/repo", Branch: "feat/x"},
		{Kind: GitActionStage, CWD: `C:\repo\`},
		{Kind: GitActionCreateBranch, CWD: `/tmp/repo\`, Branch: "feat"},
	}
	if !reflect.DeepEqual(parsed.GitActions, want) {
		t.Fatalf("git actions = %#v, want %#v", parsed.GitActions, want)
	}
}

// Mirrors Rust #42640 hides_malformed_directives_without_materializing_rows.
func TestHidesMalformedGitDirectivesLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(`Done ::git-push{cwd="/repo"}`, "/repo")
	if parsed.VisibleMarkdown != "Done" {
		t.Fatalf("visible markdown = %q, want %q", parsed.VisibleMarkdown, "Done")
	}
	if len(parsed.GitActions) != 0 {
		t.Fatalf("git actions = %#v, want none", parsed.GitActions)
	}
}

func TestSuppressesMalformedGapDirectiveDirectly(t *testing.T) {
	parsed := ParseAssistantMarkdown(`Mention ::git-push} inline.`, "/repo")
	// Rust hides a malformed `::git-` candidate only when an attribute block
	// exists; `::git-push}` has no `{`, so the text stays visible.
	if parsed.VisibleMarkdown != `Mention ::git-push} inline.` {
		t.Fatalf("visible markdown = %q, want the original text", parsed.VisibleMarkdown)
	}
}

// Mirrors Rust #42640 parses_unquoted_git_flags_and_quoted_closing_braces.
func TestParsesUnquotedGitFlagsAndQuotedClosingBracesLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(
		`Done ::git-create-pr{cwd="C:\repo" branch="feature/{rollout}" isDraft=true}`,
		"/repo",
	)
	if parsed.VisibleMarkdown != "Done" {
		t.Fatalf("visible markdown = %q, want %q", parsed.VisibleMarkdown, "Done")
	}
	want := []GitAction{{
		Kind:    GitActionCreatePR,
		CWD:     `C:\repo`,
		Branch:  "feature/{rollout}",
		IsDraft: true,
	}}
	if !reflect.DeepEqual(parsed.GitActions, want) {
		t.Fatalf("git actions = %#v, want %#v", parsed.GitActions, want)
	}
}

// Mirrors Rust #42640 renders_code_comment_directives_as_markdown and its
// `code_comment_directive_fallback` snapshot.
func TestRendersCodeCommentDirectivesAsMarkdownLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(
		"Found two issues.\n\n"+
			`::code-comment{title="Fix body= parsing" body="C:\temp says \"foo\" here; keep role=\"tab\", ::git-stage{cwd=/tmp}, file=, and \n literal." file="/repo/src/app.ts" start=10 end=12 priority="P2"} ::git-stage{cwd="/repo"}`+
			"\n\n"+
			`:::code-comment{title='[P1] Clamp the range' body='The line range should match the App.' file='codex/src/range.ts' start=8 end=2 priority=3}`,
		"/repo",
	)
	want := strings.Join([]string{
		"Found two issues.",
		"",
		"- [P2] Fix body= parsing — src/app.ts:10-12",
		`  C:\temp says "foo" here; keep role="tab", ::git-stage{cwd=/tmp}, file=, and \n literal.`,
		"",
		"- [P1] Clamp the range — codex/src/range.ts:8",
		"  The line range should match the App.",
	}, "\n")
	if parsed.VisibleMarkdown != want {
		t.Fatalf("visible markdown =\n%q\nwant\n%q", parsed.VisibleMarkdown, want)
	}
	wantActions := []GitAction{{Kind: GitActionStage, CWD: "/repo"}}
	if !reflect.DeepEqual(parsed.GitActions, wantActions) {
		t.Fatalf("git actions = %#v, want %#v", parsed.GitActions, wantActions)
	}
}

// Mirrors Rust #42640 preserves_non_directive_and_malformed_code_comment_text.
func TestPreservesNonDirectiveAndMalformedCodeCommentTextLikeRust(t *testing.T) {
	markdown := "Mention `::code-comment{title=\"Example\"}` and `::git-push}` inline.\n" +
		`::code-comment{title="Missing body" file="/repo/src/app.ts"}`
	if parsed := ParseAssistantMarkdown(markdown, "/repo"); parsed.VisibleMarkdown != markdown {
		t.Fatalf("visible markdown = %q, want the original text", parsed.VisibleMarkdown)
	}
}

// Mirrors Rust #42640 last_created_branch_cwd_uses_the_last_matching_directive.
func TestLastCreatedBranchCWDLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(
		`::git-create-branch{cwd="/first" branch="first"}`+"\n"+
			`::git-push{cwd="/repo" branch="first"}`+"\n"+
			`::git-create-branch{cwd="/second" branch="second"}`,
		"/repo",
	)
	cwd, ok := parsed.LastCreatedBranchCWD()
	if !ok || cwd != "/second" {
		t.Fatalf("last created branch cwd = %q (%v), want /second", cwd, ok)
	}
	if parsed.VisibleMarkdown != "" {
		t.Fatalf("visible markdown = %q, want the directives stripped", parsed.VisibleMarkdown)
	}
}

// Mirrors Rust #42640 using the shared parser for a Git action that follows a
// rendered code comment on the same line, and the deduplicated receipt list.
func TestCodeCommentKeepsFollowingGitActionLikeRust(t *testing.T) {
	parsed := ParseAssistantMarkdown(
		`::code-comment{title="Note" body="Body" file="src/a.ts" start=2} ::git-commit{cwd="/repo"} ::git-commit{cwd="/repo"}`,
		"/repo",
	)
	if !strings.Contains(parsed.VisibleMarkdown, "- Note — src/a.ts:2\n  Body") {
		t.Fatalf("visible markdown = %q, want the rendered code comment", parsed.VisibleMarkdown)
	}
	wantActions := []GitAction{{Kind: GitActionCommitKind, CWD: "/repo"}}
	if !reflect.DeepEqual(parsed.GitActions, wantActions) {
		t.Fatalf("git actions = %#v, want one deduplicated commit", parsed.GitActions)
	}
}

// The completed-response normalization strips directives as Rust's
// parse_assistant_markdown does (#42640).
func TestNormalizeCompletedAssistantMarkdownStripsDirectivesLikeRust(t *testing.T) {
	source := "Done.\n\n::git-stage{cwd=\"/repo\"} ::git-commit{cwd=\"/repo\"}\n"
	if got := NormalizeCompletedAssistantMarkdown(source); got != "Done." {
		t.Fatalf("normalized markdown = %q, want %q", got, "Done.")
	}
}
