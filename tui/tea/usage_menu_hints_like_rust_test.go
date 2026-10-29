package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// TestUsageMenuHintsUseListKeymapLikeRust covers Rust #46862: the usage menu
// renders action-specific open/back hints from the active list bindings.
func TestUsageMenuHintsUseListKeymapLikeRust(t *testing.T) {
	keymapConfig := codextui.NewKeymapConfig()
	if err := keymapConfig.Set("list", "accept", []string{"ctrl-a"}); err != nil {
		t.Fatalf("configure list accept: %v", err)
	}
	if err := keymapConfig.Set("list", "cancel", []string{"ctrl-b"}); err != nil {
		t.Fatalf("configure list cancel: %v", err)
	}
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		KeymapConfig:      keymapConfig,
		HasChatGPTAccount: true,
	})
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil {
		t.Fatal("usage menu did not open")
	}
	if model.modal.footerHint != "ctrl+a open \u00b7 ctrl+b back" {
		t.Fatalf("footer hint = %q", model.modal.footerHint)
	}
}

// TestUsageMenuNarrowHidesDescriptionsLikeRust covers Rust #46862's shared
// picker layout: descriptions collapse instead of crowding a narrow terminal.
func TestUsageMenuNarrowHidesDescriptionsLikeRust(t *testing.T) {
	credits := int64(2)
	model := NewModel(codextui.NewState(nil), Options{
		Width:                          32,
		Height:                         24,
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &credits,
	})
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	if !strings.Contains(view, "Show usage") || !strings.Contains(view, "Redeem reset") {
		t.Fatalf("narrow usage menu missing rows:\n%s", view)
	}
	if strings.Contains(view, "Usage history.") || strings.Contains(view, "2 available.") {
		t.Fatalf("narrow usage menu should hide descriptions:\n%s", view)
	}
}

// TestRateLimitResetConfirmationUsesConfirmHintLikeRust covers Rust #46862's
// action-specific confirm hint.
func TestRateLimitResetConfirmationUsesConfirmHintLikeRust(t *testing.T) {
	credits := int64(2)
	model := NewModel(codextui.NewState(nil), Options{
		Width:                          80,
		Height:                         24,
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &credits,
	})
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	// Rust #45764 places "Explore analytics" between token activity and the
	// reset entry.
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil {
		t.Fatal("reset confirmation did not open")
	}
	if model.modal.footerHint != "enter confirm \u00b7 esc back" {
		t.Fatalf("confirmation hint = %q", model.modal.footerHint)
	}
}
