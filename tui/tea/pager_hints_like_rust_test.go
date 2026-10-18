package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func TestPagerHintsUseTwoCompactRowsLikeRust(t *testing.T) {
	overlay := chatwidget.NewTranscriptOverlay(80, 12, "alpha\nbeta")
	view := utils.StripANSI(overlay.View())
	for _, want := range []string{
		"↑/↓ to scroll · pgup/pgdn to page · home/end to jump",
		"q close · f3 find · esc browse prompts",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("pager hint missing %q:\n%s", want, view)
		}
	}
}

func TestPagerHintsUseConfiguredBindingsLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	if err := config.Set("pager", "close", []string{"f4"}); err != nil {
		t.Fatalf("set pager.close: %v", err)
	}
	if err := config.Set("pager", "find", []string{"f6"}); err != nil {
		t.Fatalf("set pager.find: %v", err)
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, KeymapConfig: config})
	model.openTranscriptOverlay()
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "f4 close · f6 find · esc browse prompts") {
		t.Fatalf("pager hints did not use configured bindings:\n%s", view)
	}
}
