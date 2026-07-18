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
