package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust #47096: owned transcripts clip the shared hint row before activity and
// previews, while the composer keeps its rows on short terminals.
func TestShortFullscreenClipsHintsBeforeActivityLikeRust(t *testing.T) {
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

	model := newModel(8)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "COMPOSER-MARKER") {
		t.Fatalf("short view clipped the composer:\n%s", view)
	}
	if strings.Contains(view, footerHelpText) {
		t.Fatalf("short view kept hints before activity:\n%s", view)
	}
	if !strings.Contains(view, "Working (") {
		t.Fatalf("short view clipped activity before hints:\n%s", view)
	}
	if lines := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; lines > 8 {
		t.Fatalf("short view rows = %d, want <= 8:\n%s", lines, view)
	}

	tighter := utils.StripANSI(newModel(7).View())
	if !strings.Contains(tighter, "COMPOSER-MARKER") {
		t.Fatalf("tighter view clipped the composer:\n%s", tighter)
	}
	if strings.Contains(tighter, "Working (") {
		t.Fatalf("tighter view kept activity after hints were clipped:\n%s", tighter)
	}
	if lines := strings.Count(strings.TrimRight(tighter, "\n"), "\n") + 1; lines > 7 {
		t.Fatalf("tighter view rows = %d, want <= 7:\n%s", lines, tighter)
	}

	roomy := utils.StripANSI(newModel(12).View())
	if !strings.Contains(roomy, footerHelpText) || !strings.Contains(roomy, "Working (") {
		t.Fatalf("roomy fullscreen view dropped hints/activity:\n%s", roomy)
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
