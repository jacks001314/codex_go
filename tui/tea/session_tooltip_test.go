package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust's non-first-event session header shows a startup tip when
// `local_settings.tui.show_tooltips` is enabled, resolved against the current
// keybindings; a disabled preference leaves the header without a tip.
func TestModelStartupSessionHeaderTooltipHonorsShowTooltipsLikeRust(t *testing.T) {
	// Resumed sessions take the tooltip path; a fresh session's first event
	// renders the startup help block instead (Rust #58127).
	resumed := func() *codextui.State {
		state := codextui.NewState(nil)
		state.ThreadID = "thread-resumed"
		return state
	}
	off := false
	disabled := NewModel(resumed(), Options{
		Width: 100, Height: 24, ShowSessionHeader: true, ShowTooltips: &off,
	})
	if view := utils.StripANSI(disabled.View()); strings.Contains(view, "Tip: ") {
		t.Fatalf("disabled tooltips rendered a tip:\n%s", view)
	}

	on := true
	enabled := NewModel(resumed(), Options{
		Width: 100, Height: 24, ShowSessionHeader: true, ShowTooltips: &on,
	})
	view := utils.StripANSI(enabled.View())
	if strings.Contains(view, "Tip: ") {
		t.Fatalf("fullscreen transcript must hide the startup tip (Rust #48513):\n%s", view)
	}
	enabled.rawOutput = true
	if raw := utils.StripANSI(enabled.View()); !strings.Contains(raw, "Tip: ") {
		t.Fatalf("raw output must retain the startup tip:\n%s", raw)
	}
	enabled.rawOutput = false

	fullscreenOff := false
	inline := NewModel(resumed(), Options{
		Width: 100, Height: 24, ShowSessionHeader: true, ShowTooltips: &on, FullscreenTranscript: &fullscreenOff,
	})
	if inlineView := utils.StripANSI(inline.View()); !strings.Contains(inlineView, "Tip: ") {
		t.Fatalf("inline scrollback must retain the startup tip:\n%s", inlineView)
	}
	narrow := NewModel(resumed(), Options{
		Width: 32, Height: 24, ShowSessionHeader: true, ShowTooltips: &on,
	})
	narrowView := utils.StripANSI(narrow.View())
	if strings.Contains(narrowView, "Tip: ") {
		t.Fatalf("narrow fullscreen transcript must hide the startup tip:\n%s", narrowView)
	}
	if strings.Contains(narrowView, "**") || strings.Contains(narrowView, "](") {
		t.Fatalf("narrow transcript tip leaked Markdown source:\n%s", narrowView)
	}

	// A live settings update toggles the preference.
	enabled.Update(SettingsWriteResultMsg{RequestID: enabled.pendingSettingsRequestID, Result: SettingsWriteResult{ShowTooltips: &off}})
	if enabled.showTooltips {
		t.Fatal("live settings off did not disable tooltips")
	}
}

// The fast-status marker follows Rust's `ChatWidget::should_show_fast_status`:
// only a ChatGPT account with a model-supported fast tier shows it.
func TestModelSessionShowFastStatusLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width: 100, Height: 24, HasChatGPTAccount: true, ServiceTierCommands: nil,
	})
	model.State.ServiceTier = "priority"
	if model.sessionShowFastStatus() {
		t.Fatal("fast status shown without a fast service tier")
	}
	model.hasChatGPTAccount = false
	if model.sessionShowFastStatus() {
		t.Fatal("fast status shown without a ChatGPT account")
	}
}
