package tui

import (
	"strings"
	"testing"
)

func TestFollowupLabelsRenderMarksAndHidePrompts(t *testing.T) {
	source := strings.Join([]string{
		"Next actions:",
		"",
		`- :codex-followup[Add **revenue charts**]{prompt="Add a \"Q3\" chart with } labels"}`,
		"- :codex-followup[Format `Sheet1`]{prompt=\"Format the spreadsheet\"}",
		"",
		"Or :codex-followup[explain café totals]{prompt=\"Explain $x$ and *all* totals\"}.",
		"",
	}, "\n")
	want := strings.Join([]string{
		"Next actions:",
		"",
		"- Add **revenue charts**",
		"- Format `Sheet1`",
		"",
		"Or explain café totals.",
		"",
	}, "\n")
	if got := FollowupLabels(source); got != want {
		t.Fatalf("FollowupLabels() = %q, want %q", got, want)
	}
	if strings.Contains(FollowupLabels(source), "prompt=") {
		t.Fatalf("follow-up prompt leaked: %q", FollowupLabels(source))
	}
}

func TestFollowupLabelsPreserveLiteralAndMalformedSources(t *testing.T) {
	directive := `:codex-followup[Add charts]{prompt="Add charts"}`
	for _, source := range []string{
		"`" + directive + "`",
		"```text\n" + directive + "\n```",
		`\` + directive,
		"<!-- " + directive + " -->",
		"[" + directive + "](https://example.com)",
		`:codex-followup[]{prompt=action}`,
		`:codex-followup[Add charts]{prompt="unfinished`,
		`:codex-followup[Add charts`,
		`:codex-followup[Add charts]{prompt=one prompt=two}`,
	} {
		if got := FollowupLabels(source); got != source {
			t.Fatalf("FollowupLabels(%q) = %q, want unchanged", source, got)
		}
	}
}

func TestFollowupLabelsDoNotConsumeNeighborsOrNestedDirectives(t *testing.T) {
	source := `&amp; :codex-followup[**First**]{prompt="Use :codex-file-citation{path=\"/tmp/private\"}"} ` +
		`:codex-followup[*Second*]{prompt="Then continue"} &copy;` + "\n"
	want := "&amp; **First** *Second* &copy;\n"
	if got := FollowupLabels(source); got != want {
		t.Fatalf("FollowupLabels() = %q, want %q", got, want)
	}
}

func TestNormalizeCompletedAssistantMarkdownUsesFollowupLabels(t *testing.T) {
	source := "- :codex-followup[Inspect **items**]{prompt=\"private\"}\n\n"
	want := "- Inspect **items**"
	if got := NormalizeCompletedAssistantMarkdown(source); got != want {
		t.Fatalf("NormalizeCompletedAssistantMarkdown() = %q, want %q", got, want)
	}
}

func TestHideUnfinishedFollowupDirectiveForStreamingPreview(t *testing.T) {
	completed := `Before :codex-followup[Add charts]{prompt="private"}`
	if got := HideUnfinishedFollowupDirective(completed); got != completed {
		t.Fatalf("completed directive was removed: %q", got)
	}
	for _, source := range []string{
		`Before :codex-followup[Add charts]{prompt="private"`,
		"Before :codex-followup[Add **charts**",
	} {
		if got := HideUnfinishedFollowupDirective(source); got != "Before" {
			t.Fatalf("HideUnfinishedFollowupDirective(%q) = %q, want Before", source, got)
		}
	}
	literal := "```text\n:codex-followup[Add charts"
	if got := HideUnfinishedFollowupDirective(literal); got != literal {
		t.Fatalf("newline-terminated literal was removed: %q", got)
	}
}
