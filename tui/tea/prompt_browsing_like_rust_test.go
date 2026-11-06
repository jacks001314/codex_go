package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// browsingTestModel builds a scrollable transcript with several prompts.
func browsingTestModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-browse")
	for index := 0; index < 8; index++ {
		state.AddMessage(codextui.RoleUser, fmt.Sprintf("prompt-%d", index))
		state.AddMessage(codextui.RoleAssistant, strings.Repeat(fmt.Sprintf("answer-%d row\n", index), 4))
	}
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.refreshTranscript()
	return model
}

func startOwnedPromptBrowsing(t *testing.T, model *Model) {
	t.Helper()
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEsc))
	if model.overlay == nil || !model.backtrack.OverlayPreviewActive {
		t.Fatal("double Escape did not open prompt browsing")
	}
}

// TestPromptBrowsingCompactDetailsAndCancelLikeRust covers #46739's compact
// browser: Escape restores the owned reading position, Left/Right and h/l move
// prompts, and the configured open_transcript key toggles details in place.
func TestPromptBrowsingCompactDetailsAndCancelLikeRust(t *testing.T) {
	model := browsingTestModel(t)
	_ = model.viewString()
	model.scrollTranscriptRows(-8)
	offset := model.transcript.YOffset

	startOwnedPromptBrowsing(t, model)
	if model.browsingDetails {
		t.Fatal("browsing must start in compact mode")
	}
	view := utils.StripANSI(model.viewString())
	for _, want := range []string{"Browsing", "ctrl+t details", "rewind", "esc back"} {
		if !strings.Contains(view, want) {
			t.Fatalf("browsing footer missing %q:\n%s", want, view)
		}
	}

	model.Update(key(keymsg.KeyCtrlT))
	if !model.browsingDetails || model.overlay == nil || !model.backtrack.OverlayPreviewActive {
		t.Fatal("ctrl+t did not toggle detailed browsing in place")
	}
	model.Update(key(keymsg.KeyCtrlT))
	if model.browsingDetails {
		t.Fatal("second ctrl+t did not restore compact browsing")
	}

	model.Update(key(keymsg.KeyLeft))
	if got := model.backtrack.NthUserMessage; got != 6 {
		t.Fatalf("Left prompt = %d, want 6", got)
	}
	model.Update(runes("l"))
	if got := model.backtrack.NthUserMessage; got != 7 {
		t.Fatalf("l prompt = %d, want 7", got)
	}
	model.Update(runes("h"))
	if got := model.backtrack.NthUserMessage; got != 6 {
		t.Fatalf("h prompt = %d, want 6", got)
	}

	model.Update(key(keymsg.KeyEsc))
	if model.overlay != nil || model.backtrack.OverlayPreviewActive || model.browsingOrigin != nil {
		t.Fatal("Escape did not cancel owned browsing")
	}
	if got := model.transcript.YOffset; got != offset {
		t.Fatalf("owned transcript offset = %d, want restored %d", got, offset)
	}
}

// TestInlineBrowsingCancelRestoresOverlayBookmarkLikeRust keeps an overlay
// opened before browsing at its original reading offset and detailed mode.
func TestInlineBrowsingCancelRestoresOverlayBookmarkLikeRust(t *testing.T) {
	model := browsingTestModel(t)
	model.openTranscriptOverlay()
	if model.overlay == nil {
		t.Fatal("transcript overlay did not open")
	}
	model.overlay.SetYOffset(5)
	before := model.overlay.YOffset()

	model.Update(key(keymsg.KeyEsc))
	if !model.backtrack.OverlayPreviewActive || model.browsingDetails {
		t.Fatalf("inline browsing = active:%v details:%v, want compact browsing", model.backtrack.OverlayPreviewActive, model.browsingDetails)
	}
	model.Update(key(keymsg.KeyCtrlT))
	if !model.browsingDetails || model.overlay == nil {
		t.Fatal("ctrl+t closed or failed to expand the inline browser")
	}
	model.Update(key(keymsg.KeyEsc))
	if model.overlay == nil || model.backtrack.OverlayPreviewActive {
		t.Fatal("Escape must keep an existing overlay after cancelling browsing")
	}
	if !model.browsingDetails {
		t.Fatal("cancel did not restore the overlay's detailed presentation")
	}
	if got := model.overlay.YOffset(); got != before {
		t.Fatalf("overlay offset = %d, want restored %d", got, before)
	}
	if _, _, ok := model.overlay.HighlightRange(); ok {
		t.Fatal("cancel left the browsing highlight active")
	}
}

// TestBrowsingHonorsRemappedOpenTranscriptLikeRust shares the footer and the
// in-place details action with the configured keybinding, including a chord.
func TestBrowsingHonorsRemappedOpenTranscriptLikeRust(t *testing.T) {
	model := browsingTestModel(t)
	config := codextui.NewKeymapConfig()
	if err := config.Set("global", "open_transcript", []string{"ctrl-x ctrl-t"}); err != nil {
		t.Fatalf("bind open_transcript: %v", err)
	}
	model.keymapConfig = config
	startOwnedPromptBrowsing(t, model)
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "ctrl+x ctrl+t details") {
		t.Fatalf("browsing footer did not use the remapped chord:\n%s", view)
	}
	model.Update(key(keymsg.KeyCtrlX))
	model.Update(key(keymsg.KeyCtrlT))
	if !model.browsingDetails || model.overlay == nil {
		t.Fatal("remapped chord did not toggle details")
	}
	if !model.backtrack.OverlayPreviewActive {
		t.Fatal("remapped chord cancelled browsing")
	}
}

// TestBrowsingFooterFitsWidthLikeRust keeps the footer within every tested
// terminal width while preserving rewind/cancel when they fit.
func TestBrowsingFooterFitsWidthLikeRust(t *testing.T) {
	model := browsingTestModel(t)
	startOwnedPromptBrowsing(t, model)
	for _, width := range []int{100, 80, 60, 40, 20, 8} {
		hint := model.browsingActionHint(width)
		if codextui.DisplayWidth(hint) > width {
			t.Fatalf("width=%d hint %q exceeds the viewport", width, hint)
		}
		if !strings.Contains(hint, "esc") {
			t.Fatalf("width=%d hint %q lost the cancel action", width, hint)
		}
	}
}

// TestUnsafeRemotePromptRestoreLeavesComposerCleanLikeRust checks the tea half
// of #46739's remote guard: the error is shown without restoring rejected text.
func TestUnsafeRemotePromptRestoreLeavesComposerCleanLikeRust(t *testing.T) {
	model := backtrackTestModel(t, func(tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, UnsafePromptRestoreError{
			Message: "This remote prompt contains local image paths or command syntax that cannot be restored safely. Write a new message and reattach any images.",
		}
	}, backtrackTestMessages()...)
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEsc))
	model.Update(key(keymsg.KeyEnter))
	if model.overlay != nil || model.backtrack.OverlayPreviewActive {
		t.Fatal("unsafe restore left browsing active")
	}
	if model.composer.Value() != "" || len(model.attachments) != 0 {
		t.Fatalf("unsafe restore repopulated the composer: %q %#v", model.composer.Value(), model.attachments)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "local image paths") {
		t.Fatalf("unsafe restore error missing:\n%s", view)
	}
}
