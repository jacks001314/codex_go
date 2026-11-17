package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

func extensionSurfaceModel(t *testing.T, width int, height int, owned bool) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-extension")
	state.SetStatus("idle")
	state.Model = "gpt-5"
	model := NewModel(state, Options{Width: width, Height: height})
	model.width, model.height, model.ownedScreen = width, height, owned
	return model
}

func installExtensionSurface(t *testing.T, model *Model, tree string) bottompane.StatusLine {
	t.Helper()
	line, err := bottompane.StatusLineFromUITreeJSON([]byte(tree))
	if err != nil {
		t.Fatalf("StatusLineFromUITreeJSON() error = %v", err)
	}
	model.SetExtensionStatusLine(&line)
	return line
}

// TestExtensionStatusUsesFooterRowBudgetLikeRust covers #60032: extension
// content reserves multiple status rows up to the host budget, and short
// terminals make extra rows yield to the draft with a visible ellipsis.
func TestExtensionStatusUsesFooterRowBudgetLikeRust(t *testing.T) {
	model := extensionSurfaceModel(t, 40, 40, true)
	installExtensionSurface(t, model, `"one\ntwo\nthree\nfour"`)
	if got := model.statusLineSectionRows(); got != 4 {
		t.Fatalf("status rows = %d, want 4", got)
	}
	section := model.renderStatusLineSection("")
	rows := strings.Split(ansi.Strip(section), "\n")
	if len(rows) != 4 {
		t.Fatalf("section rows = %d, want 4:\n%s", len(rows), section)
	}
	for index, want := range []string{"one", "two", "three", "four"} {
		if got := strings.TrimSpace(rows[index]); got != want {
			t.Fatalf("row %d = %q, want %q", index, got, want)
		}
	}

	short := extensionSurfaceModel(t, 40, 8, true)
	installExtensionSurface(t, short, `"one\ntwo\nthree\nfour"`)
	if got := short.statusLineSectionRows(); got != 1 {
		t.Fatalf("short status rows = %d, want 1", got)
	}
	clipped := short.renderStatusLineSection("")
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(clipped)), "\u2026") {
		t.Fatalf("short section lacks ellipsis: %q", clipped)
	}
}

// TestExtensionStatusKeepsLinksAndMeasuredWidthLikeRust covers #60031: the
// live footer runs extension content through the prepared UI-tree renderer, so
// links survive at wide widths and clip together with the visible cells.
func TestExtensionStatusKeepsLinksAndMeasuredWidthLikeRust(t *testing.T) {
	url := "https://example.com/pr/42"
	model := extensionSurfaceModel(t, 40, 40, true)
	installExtensionSurface(t, model, `{"type":"row","props":{"gap":1},"children":["ready",{"type":"link","props":{"href":"`+url+`"},"children":["PR #42"]}]}`)
	section := model.renderStatusLineSection("")
	if !strings.Contains(section, url) {
		t.Fatalf("section lost OSC-8 destination: %q", section)
	}
	plainSection := ansi.Strip(codextui.StripOSC8(section))
	if !strings.Contains(plainSection, "ready") || !strings.Contains(plainSection, "PR #42") {
		t.Fatalf("section lost extension text: %q", plainSection)
	}

	narrow := extensionSurfaceModel(t, 10, 40, true)
	installExtensionSurface(t, narrow, `{"type":"link","props":{"href":"`+url+`"},"children":["ABCDEFGHIJ"]}`)
	section = narrow.renderStatusLineSection("")
	plain := ansi.Strip(codextui.StripOSC8(section))
	if !strings.HasSuffix(strings.TrimSpace(plain), "\u2026") {
		t.Fatalf("narrow section lacks ellipsis: %q", plain)
	}
	links := codextui.OSC8LinksInLine(section)
	if len(links) != 1 || links[0].End > codextui.DisplayWidth(plain)-1 {
		t.Fatalf("narrow link range = %#v (%q)", links, plain)
	}
}

// TestExtensionStatusKeepsPlanIndicatorLikeRust covers the shared-layout rule
// from #60031: the mode indicator shares the status viewport with extension
// content instead of replacing it, and the content reflows to the remaining
// width.
func TestExtensionStatusKeepsPlanIndicatorLikeRust(t *testing.T) {
	model := extensionSurfaceModel(t, 100, 40, true)
	model.State.PlanMode = true
	model.featureSettings = map[string]bool{"collaboration_modes": true}
	installExtensionSurface(t, model, `"extension status"`)
	section := model.renderStatusLineSection("")
	plain := ansi.Strip(codextui.StripOSC8(section))
	if !strings.Contains(plain, "extension status") {
		t.Fatalf("section lost extension content: %q", plain)
	}
	if !strings.Contains(plain, bottompane.CollaborationModeLabel(bottompane.CollaborationModePlan, true)) {
		t.Fatalf("section lost the full plan indicator: %q", plain)
	}

	narrow := extensionSurfaceModel(t, 34, 40, true)
	narrow.State.PlanMode = true
	narrow.featureSettings = map[string]bool{"collaboration_modes": true}
	installExtensionSurface(t, narrow, `"extension status"`)
	plain = ansi.Strip(codextui.StripOSC8(narrow.renderStatusLineSection("")))
	if !strings.Contains(plain, "Plan mode") || strings.Contains(plain, "shift+tab") {
		t.Fatalf("narrow plan indicator = %q", plain)
	}
}

// TestExtensionStatusHonorsReadOnlyAndClearLikeRust keeps the #61219 rule:
// a saved-history/read-only thread hides every status surface, and clearing
// extension content restores the native configured line.
func TestExtensionStatusHonorsReadOnlyAndClearLikeRust(t *testing.T) {
	model := extensionSurfaceModel(t, 80, 40, true)
	installExtensionSurface(t, model, `"extension status"`)
	if got := model.statusLineContent(); !strings.Contains(got, "extension status") {
		t.Fatalf("extension content = %q", got)
	}
	model.readOnlyThread = true
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("read-only extension content = %q, want empty", got)
	}
	model.readOnlyThread = false
	model.ClearExtensionStatusLine()
	if model.statusLineExtension != nil {
		t.Fatal("ClearExtensionStatusLine() kept the extension surface")
	}
	if got := model.statusLineContent(); strings.Contains(got, "extension status") {
		t.Fatalf("native status kept extension content: %q", got)
	}
}
