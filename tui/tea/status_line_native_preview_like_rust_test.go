package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
)

// Rust #60027 keeps configured status lines and their picker previews on the
// native renderer even when the extensions feature is enabled. The preview must
// keep theme colour spans and the pull-request hyperlink; routing it through the
// plain UI-tree renderer dropped both.
func TestStatusLinePreviewStaysNativeWithExtensionsFlagLikeRust(t *testing.T) {
	const url = "https://example.com/pr/42"
	model := NewModel(codextui.NewState(nil), Options{
		Width:           80,
		Height:          24,
		TUITheme:        "catppuccin-mocha",
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
	})
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	model.statusControls.SetStatusLineHyperlink(url, true)

	data := chatwidget.NewStatusSurfacePreviewData(map[chatwidget.StatusSurfacePreviewItem]string{
		chatwidget.StatusPreviewModel:             "gpt-5",
		chatwidget.StatusPreviewPullRequestNumber: "PR #42",
	})
	items := []bottompane.StatusLineItem{bottompane.StatusLineModelName, bottompane.StatusLinePullRequestNumber}
	preview := model.statusLinePreviewStyled(data, items, true)

	if !strings.Contains(preview, "\x1b]8;;"+url+"\x07") {
		t.Fatalf("preview lost the native pull-request hyperlink: %q", preview)
	}
	visible := codextui.StripOSC8(stripStatusLinePreviewANSI(preview))
	if visible != "gpt-5 \u00b7 PR #42" {
		t.Fatalf("preview text = %q", visible)
	}
	if plain := model.statusLinePreviewStyled(data, items, false); preview == plain {
		t.Fatal("preview did not keep native theme colours")
	}
}

// An unsafe pull-request URL is never wrapped in an OSC-8 link, matching the
// native HyperlinkLine validation.
func TestStatusLinePreviewSkipsUnsafeHyperlinkLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, TUITheme: "catppuccin-mocha"})
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	model.statusControls.SetStatusLineHyperlink("file:///tmp/pr", true)

	data := chatwidget.NewStatusSurfacePreviewData(map[chatwidget.StatusSurfacePreviewItem]string{
		chatwidget.StatusPreviewPullRequestNumber: "PR #42",
	})
	preview := model.statusLinePreviewStyled(data, []bottompane.StatusLineItem{bottompane.StatusLinePullRequestNumber}, true)
	if strings.Contains(preview, "\x1b]8;;") {
		t.Fatalf("unsafe URL produced an OSC-8 link: %q", preview)
	}
}
