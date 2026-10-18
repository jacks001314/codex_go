package markdown

import (
	"strings"
	"testing"
)

// TestFollowupDirectivesRenderAsOrdinaryMarkdownLikeRust pins #49089: the
// visible label is rendered through the normal Markdown pipeline while the
// private prompt attribute never reaches the transcript.
func TestFollowupDirectivesRenderAsOrdinaryMarkdownLikeRust(t *testing.T) {
	source := strings.Join([]string{
		"Next actions:",
		"",
		`- :codex-followup[Add **revenue charts**]{prompt="Add a chart"}`,
		"- :codex-followup[Format `Sheet1`]{prompt=\"Format the spreadsheet\"}",
		"",
		"Or :codex-followup[explain café totals]{prompt=\"Explain $x$ and *all* totals\"}.",
		"",
	}, "\n")
	plain := strings.Join([]string{
		"Next actions:",
		"",
		"- Add **revenue charts**",
		"- Format `Sheet1`",
		"",
		"Or explain café totals.",
		"",
	}, "\n")
	rendered, err := RenderWithThemeCwd(source, 32, "", "")
	if err != nil {
		t.Fatalf("render follow-up markdown: %v", err)
	}
	want, err := RenderWithThemeCwd(plain, 32, "", "")
	if err != nil {
		t.Fatalf("render plain markdown: %v", err)
	}
	if rendered != want {
		t.Fatalf("follow-up render = %q, want %q", rendered, want)
	}
	if strings.Contains(rendered, "prompt=") || strings.Contains(rendered, "Add a chart") {
		t.Fatalf("private follow-up prompt leaked: %q", rendered)
	}
}

// TestFollowupDirectivesStayLiteralInsideCodeLikeRust keeps author examples
// visible when the directive is quoted as code.
func TestFollowupDirectivesStayLiteralInsideCodeLikeRust(t *testing.T) {
	directive := `:codex-followup[Add charts]{prompt="Add charts"}`
	rendered, err := RenderWithThemeCwd("```text\n"+directive+"\n```", 80, "", "")
	if err != nil {
		t.Fatalf("render code fence: %v", err)
	}
	if !strings.Contains(rendered, ":codex-followup[Add charts]") {
		t.Fatalf("code-fenced directive was rewritten: %q", rendered)
	}
}
