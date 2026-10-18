package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust #46899: inserted completion rows must not shift the reader's top line.
func TestTranscriptScrollAnchorFollowsVisibleLineLikeRust(t *testing.T) {
	old := "header\nalpha\nbeta\ngamma\ndelta"
	anchor := captureTranscriptScrollAnchor(old, 2, 2)
	if !anchor.hasAnchor || anchor.line != "beta" || anchor.offset != 0 {
		t.Fatalf("anchor = %#v", anchor)
	}
	updated := "header\ninserted one\ninserted two\nalpha\nbeta\ngamma\ndelta"
	if got := restoreTranscriptScrollAnchor(updated, anchor, 4); got != 4 {
		t.Fatalf("restored offset = %d, want 4", got)
	}

	// A blank top row anchors to the first non-blank visible row.
	anchor = captureTranscriptScrollAnchor("header\n\nbeta\ngamma", 1, 2)
	if !anchor.hasAnchor || anchor.line != "beta" || anchor.offset != 1 {
		t.Fatalf("blank-row anchor = %#v", anchor)
	}
	if got := restoreTranscriptScrollAnchor("header\ninserted\n\nbeta\ngamma", anchor, 4); got != 2 {
		t.Fatalf("blank-row restored offset = %d, want 2", got)
	}

	// A missing anchor falls back to the old row offset, clamped to content.
	if got := restoreTranscriptScrollAnchor("only\nrows", transcriptScrollAnchor{yOffset: 9}, 1); got != 1 {
		t.Fatalf("fallback offset = %d, want 1", got)
	}
}

func TestCompletedStreamKeepsReaderTopLineLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleUser, "header")
	var body strings.Builder
	for index := 0; index < 12; index++ {
		body.WriteString(fmt.Sprintf("line %02d\n\n", index))
	}
	body.WriteString("beta\n\ngamma\n\ndelta")
	state.AddMessage(codextui.RoleAssistant, body.String())
	model := NewModel(state, Options{Width: 80, Height: 14})
	model.View()
	model.activityFollow = false

	lines := strings.Split(model.lastTranscriptContent, "\n")
	anchorLine := -1
	for index, line := range lines {
		if strings.Contains(line, "line 11") {
			anchorLine = index
			break
		}
	}
	if anchorLine < 0 {
		t.Fatalf("line 11 row missing from transcript:\n%s", model.lastTranscriptContent)
	}
	model.transcript.SetYOffset(anchorLine)
	before := firstVisibleTranscriptLine(model.transcript.View())
	if !strings.Contains(before, "line 11") {
		t.Fatalf("top before = %q, want line 11 row", before)
	}

	state.Messages[0].Text = "header\ninserted one\ninserted two"
	model.refreshTranscript()
	after := firstVisibleTranscriptLine(model.transcript.View())
	if after != before {
		t.Fatalf("top line moved after insertion: before=%q after=%q", before, after)
	}
}

func firstVisibleTranscriptLine(view string) string {
	for _, line := range strings.Split(view, "\n") {
		text := strings.TrimRight(utils.StripANSI(line), " ")
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
