package tea

import (
	"regexp"
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

var statusLinePreviewANSIPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripStatusLinePreviewANSI(value string) string {
	return statusLinePreviewANSIPattern.ReplaceAllString(value, "")
}

// TestStatusLinePreviewMatchesFooterThreadColors covers Rust #44857: the status
// line setup preview must use the same spans and the same per-thread identity
// color as the live footer, for both thread name and thread title.
func TestStatusLinePreviewMatchesFooterThreadColors(t *testing.T) {
	enabled := true
	const threadID = "00000000-0000-0000-0000-000000000001"
	state := codextui.NewState(nil)
	state.ThreadID = threadID
	state.SetThreadName("Named task")
	model := NewModel(state, Options{
		Width:               80,
		Height:              24,
		TUITheme:            "catppuccin-mocha",
		StatusLineItems:     []string{"thread-name", "thread-title"},
		StatusLineUseColors: &enabled,
	})
	model.statusLineConfiguredByUser = true
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()

	items := []bottompane.StatusLineItem{bottompane.StatusLineThreadName, bottompane.StatusLineThreadTitle}
	identity := codextui.ThreadColorSGR(codextui.ThreadColorForTheme(threadID, model.tuiTheme))
	if identity == "" {
		t.Fatal("expected a thread identity color for the test theme")
	}

	preview := model.statusLinePreviewStyled(model.statusControls.StatusSurfacePreviewData(), items, true)
	if !strings.Contains(preview, identity) {
		t.Fatalf("preview is missing the thread identity color %q: %q", identity, preview)
	}
	if strings.Contains(preview, "\x1b[35m") {
		t.Fatalf("thread spans fell back to the generic thread accent: %q", preview)
	}
	if stripStatusLinePreviewANSI(preview) != "Named task \u00b7 Named task" {
		t.Fatalf("preview text = %q", stripStatusLinePreviewANSI(preview))
	}
	if footer := model.renderStatusHeader(); footer != preview {
		t.Fatalf("preview and footer diverged:\npreview %q\nfooter  %q", preview, footer)
	}

	// tui.status_line_use_colors=false suppresses every color, in the preview
	// and the footer alike.
	offPreview := model.statusLinePreviewStyled(model.statusControls.StatusSurfacePreviewData(), items, false)
	if strings.Contains(offPreview, "\x1b[38;2;") {
		t.Fatalf("disabled preview still has truecolor spans: %q", offPreview)
	}
	if stripStatusLinePreviewANSI(offPreview) != "Named task \u00b7 Named task" {
		t.Fatalf("disabled preview text = %q", stripStatusLinePreviewANSI(offPreview))
	}
}

// TestStatusLinePreviewFallsBackToTerminalDefault covers a thread with no
// identity color: the preview keeps the plain text instead of inventing one.
func TestStatusLinePreviewFallsBackToTerminalDefault(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-without-accent"
	state.SetThreadName("Named task")
	model := NewModel(state, Options{
		Width:           80,
		Height:          24,
		TUITheme:        "catppuccin-mocha",
		StatusLineItems: []string{"thread-name"},
	})
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	preview := model.statusLinePreviewStyled(model.statusControls.StatusSurfacePreviewData(), []bottompane.StatusLineItem{bottompane.StatusLineThreadName}, true)
	if got := stripStatusLinePreviewANSI(preview); got != "Named task" {
		t.Fatalf("preview text = %q", got)
	}
}
