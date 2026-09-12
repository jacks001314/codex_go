package chatwidget

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func TestTranscriptOverlayPagerActionsPreserveAndFollowBottom(t *testing.T) {
	overlay := NewTranscriptOverlay(48, 8, numberedTranscript(40))
	if !overlay.AtBottom() || overlay.YOffset() <= 0 {
		t.Fatalf("initial overlay offset=%d atBottom=%v, want scrollable bottom", overlay.YOffset(), overlay.AtBottom())
	}

	overlay.ApplyPagerAction(PagerJumpTop)
	if !overlay.AtTop() {
		t.Fatalf("jump top offset=%d", overlay.YOffset())
	}
	offset := overlay.YOffset()
	overlay.SetContent(numberedTranscript(45))
	if got := overlay.YOffset(); got != offset {
		t.Fatalf("SetContent while reading offset=%d, want %d", got, offset)
	}

	overlay.ApplyPagerAction(PagerJumpBottom)
	overlay.SetContent(numberedTranscript(50))
	if !overlay.AtBottom() {
		t.Fatalf("SetContent at bottom did not follow tail; offset=%d", overlay.YOffset())
	}
	if !strings.Contains(overlay.Content(), "line 49") {
		t.Fatalf("overlay content missing appended tail")
	}
}

func TestTranscriptOverlayViewFitsHeaderAndHelp(t *testing.T) {
	overlay := NewTranscriptOverlay(32, 6, numberedTranscript(20))
	view := overlay.View()
	if !strings.Contains(view, "T R A N S C R I P T") {
		t.Fatalf("view missing transcript title:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 32 {
			t.Fatalf("line over width: %q", line)
		}
	}
}

func TestTranscriptOverlayHighlightRangeRendersReverseVideo(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 8, "alpha\nbeta\ngamma")
	if _, _, ok := overlay.HighlightRange(); ok {
		t.Fatal("a fresh overlay should have no highlight")
	}

	overlay.SetHighlightRange(1, 2)
	start, end, ok := overlay.HighlightRange()
	if !ok || start != 1 || end != 2 {
		t.Fatalf("HighlightRange = %d,%d,%v want 1,2,true", start, end, ok)
	}
	// Content() reports the base transcript, not the highlighted rendering.
	if overlay.Content() != "alpha\nbeta\ngamma" {
		t.Fatalf("Content = %q", overlay.Content())
	}
	view := overlay.View()
	if !strings.Contains(view, reverseVideoOn+"beta"+reverseVideoOff) {
		t.Fatalf("highlighted line missing reverse video:\n%q", view)
	}
	if strings.Contains(view, reverseVideoOn+"alpha") || strings.Contains(view, reverseVideoOn+"gamma") {
		t.Fatalf("unselected lines must stay plain:\n%q", view)
	}

	overlay.ClearHighlightRange()
	if _, _, ok := overlay.HighlightRange(); ok {
		t.Fatal("ClearHighlightRange left a highlight")
	}
	if strings.Contains(overlay.View(), reverseVideoOn) {
		t.Fatalf("cleared overlay still renders reverse video:\n%q", overlay.View())
	}
}

func TestTranscriptOverlayHighlightReassertsReverseAfterReset(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 8, "plain\n\x1b[31mred\x1b[0m tail")
	overlay.SetHighlightRange(1, 2)
	view := overlay.View()
	if !strings.Contains(view, "\x1b[0m"+reverseVideoOn) {
		t.Fatalf("an inner SGR reset must re-assert the highlight:\n%q", view)
	}
}

func TestTranscriptOverlayHighlightScrollsIntoView(t *testing.T) {
	overlay := NewTranscriptOverlay(48, 8, numberedTranscript(40))
	if overlay.YOffset() == 0 {
		t.Fatal("expected the overlay to start at the bottom")
	}
	overlay.SetHighlightRange(0, 1)
	if offset := overlay.YOffset(); offset > 0 {
		t.Fatalf("highlight above the viewport should scroll up, offset=%d", offset)
	}

	overlay.ApplyPagerAction(PagerJumpTop)
	overlay.SetHighlightRange(38, 40)
	if offset := overlay.YOffset(); offset < 34 {
		t.Fatalf("highlight below the viewport should scroll down, offset=%d", offset)
	}
}

func TestTranscriptOverlayHighlightInvalidRangeClears(t *testing.T) {
	overlay := NewTranscriptOverlay(40, 8, "alpha\nbeta")
	overlay.SetHighlightRange(0, 1)
	overlay.SetHighlightRange(2, 1)
	if _, _, ok := overlay.HighlightRange(); ok {
		t.Fatal("an inverted range should clear the highlight")
	}
	overlay.SetHighlightRange(0, 1)
	overlay.SetHighlightRange(-1, 2)
	if _, _, ok := overlay.HighlightRange(); ok {
		t.Fatal("a negative start should clear the highlight")
	}
}

func TestLastAssistantMarkdown(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleAssistant, Text: " first "},
		{Role: codextui.RoleSystem, Text: "notice"},
		{Role: codextui.RoleAssistant, Text: "second"},
	}
	got, ok := LastAssistantMarkdown(messages)
	if !ok || got != "second" {
		t.Fatalf("LastAssistantMarkdown = %q ok=%v, want second", got, ok)
	}
}

func numberedTranscript(count int) string {
	var builder strings.Builder
	for i := 0; i < count; i++ {
		if i > 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "line %02d", i)
	}
	return builder.String()
}
