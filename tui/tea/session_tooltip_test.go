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
	off := false
	disabled := NewModel(codextui.NewState(nil), Options{
		Width: 100, Height: 24, ShowSessionHeader: true, ShowTooltips: &off,
	})
	if view := utils.StripANSI(disabled.View()); strings.Contains(view, "Tip: ") {
		t.Fatalf("disabled tooltips rendered a tip:\n%s", view)
	}

	on := true
	enabled := NewModel(codextui.NewState(nil), Options{
		Width: 100, Height: 24, ShowSessionHeader: true, ShowTooltips: &on,
	})
	if view := utils.StripANSI(enabled.View()); !strings.Contains(view, "Tip: ") {
		t.Fatalf("enabled tooltips missing the session tip:\n%s", view)
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
