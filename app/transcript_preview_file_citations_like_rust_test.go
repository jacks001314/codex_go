package app

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// Rust #42650 renders resume-picker transcript previews through
// render_streaming_markdown_lines_with_width_and_cwd, so an assistant file
// citation shows the citation's local path instead of the raw directive.
func TestTranscriptPreviewRendersAssistantFileCitationsLikeRust(t *testing.T) {
	turns := []appserver.Turn{{ID: "t1", Items: []appserver.ThreadItem{{
		ID:   "a1",
		Type: "agentMessage",
		Text: `See :codex-file-citation{path="src/report.md"} for details.`,
	}}}}
	got := transcriptPreviewFromTurns(turns, "/repo")
	if len(got) != 1 {
		t.Fatalf("lines = %#v", got)
	}
	if got[0].Speaker != codextui.TranscriptPreviewAssistant || got[0].Text != "See src/report.md for details." {
		t.Fatalf("preview line = %#v, want citation path", got[0])
	}
}

// Citations inside code spans stay literal, matching the rendering path the
// preview shares with streaming and finalized messages.
func TestTranscriptPreviewKeepsCodeSpanCitationsLiteralLikeRust(t *testing.T) {
	const source = "Run `:codex-file-citation{path=\"src/report.md\"}` now."
	turns := []appserver.Turn{{ID: "t1", Items: []appserver.ThreadItem{{
		ID:   "a1",
		Type: "agentMessage",
		Text: source,
	}}}}
	got := transcriptPreviewFromTurns(turns, "/repo")
	if len(got) != 1 || got[0].Text != source {
		t.Fatalf("lines = %#v, want literal directive %q", got, source)
	}
}

// The preview also drops completed-response directives (#42640), so a Git
// receipt line never shows up in the resume picker.
func TestTranscriptPreviewStripsGitDirectivesLikeRust(t *testing.T) {
	turns := []appserver.Turn{{ID: "t1", Items: []appserver.ThreadItem{{
		ID:   "a1",
		Type: "agentMessage",
		Text: "Pushed.\n::git-push{cwd=\"/repo\" branch=\"main\"}\n",
	}}}}
	got := transcriptPreviewFromTurns(turns, "/repo")
	if len(got) != 1 || got[0].Text != "Pushed." {
		t.Fatalf("lines = %#v, want only the visible text", got)
	}
}
