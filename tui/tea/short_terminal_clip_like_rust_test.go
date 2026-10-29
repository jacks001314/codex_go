package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust owned_transcript packs the frame into the terminal: the transcript and
// the flexible activity row yield rows before the composer loses any, and the
// composer keeps its shaded block, status line, and passive footer hint.
func TestShortFullscreenClipsTranscriptBeforeActivityLikeRust(t *testing.T) {
	fullscreen := true
	newModel := func(height int) *Model {
		state := codextui.NewState(nil)
		state.Status = "running"
		state.AddMessage(codextui.RoleAssistant, "hello")
		return NewModel(state, Options{
			Width:                80,
			Height:               height,
			FullscreenTranscript: &fullscreen,
			Placeholder:          "COMPOSER-MARKER",
		})
	}

	for _, height := range []int{5, 6, 7, 8, 12} {
		view := utils.StripANSI(newModel(height).View())
		if !strings.Contains(view, "COMPOSER-MARKER") {
			t.Fatalf("height %d clipped the composer:\n%s", height, view)
		}
		if !strings.Contains(view, footerHelpText) {
			t.Fatalf("height %d dropped the passive footer hint:\n%s", height, view)
		}
		if lines := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; lines > height {
			t.Fatalf("height %d rendered %d rows:\n%s", height, lines, view)
		}
	}

	// The activity row is flexible: it survives while the transcript yields.
	if view := utils.StripANSI(newModel(7).View()); !strings.Contains(view, "Working (") {
		t.Fatalf("height 7 dropped activity before the transcript:\n%s", view)
	}
	if view := utils.StripANSI(newModel(6).View()); strings.Contains(view, "Working (") {
		t.Fatalf("height 6 kept activity after the transcript was clipped:\n%s", view)
	}

	inline := false
	inlineModel := NewModel(codextui.NewState(nil), Options{
		Width:                80,
		Height:               8,
		FullscreenTranscript: &inline,
		Placeholder:          "COMPOSER-MARKER",
	})
	inlineView := utils.StripANSI(inlineModel.View())
	if !strings.Contains(inlineView, "COMPOSER-MARKER") || !strings.Contains(inlineView, footerHelpText) {
		t.Fatalf("inline layout should keep its footer:\n%s", inlineView)
	}
}
