package tea

import (
	"strings"
	"testing"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Mirrors Rust #58927's presentation-only half: reflowing live stream content
// at a new width must not report unseen activity, because no new output
// arrived. Go keys unseen activity to the message revision, so a width change
// re-renders without changing that revision.
func TestTranscriptResizeReflowsWithoutUnseenActivityLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	model.View()
	model.transcriptUnseenActivity = false

	long := strings.Repeat("streamed answer words ", 8)
	model.State.AddMessage(codextui.RoleAssistant, long)
	model.View()
	if !model.transcriptUnseenActivity {
		t.Fatal("new output while paused did not report unseen activity")
	}
	// Returning to the bottom clears the notice; the resize below is the only
	// change after this point.
	model.transcriptUnseenActivity = false

	model.Update(bubbletea.WindowSizeMsg{Width: 40, Height: 10})
	view := utils.StripANSI(model.View())
	if model.transcriptUnseenActivity {
		t.Fatalf("a resize-only reflow reported unseen activity:\n%s", view)
	}
	rendered := strings.Join(model.transcript.lines, "\n")
	if !strings.Contains(rendered, "streamed") {
		t.Fatalf("live stream content disappeared after the resize:\n%s", view)
	}
	for _, line := range model.transcript.lines {
		if width := utf8.RuneCountInString(line); width > 40 {
			t.Fatalf("line width %d exceeds the resized transcript width 40:\n%s", width, view)
		}
	}
}
