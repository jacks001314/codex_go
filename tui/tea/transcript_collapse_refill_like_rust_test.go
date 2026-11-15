package tea

import (
	"strings"
	"testing"

	keymsg "codex_go/tui/keymsg"
	"codex_go/utils"
)

// Rust #61424: collapsing a tool group while reading its expanded details can
// leave the scroll anchor below the new bottom limit. Go's viewport clamps the
// painted window when the rendered rows shrink, so the transcript must refill
// from the new bottom limit and keep that position across renders.
func TestCollapsingScrolledToolGroupRefillsViewportLikeRust(t *testing.T) {
	model := disclosureFixture(t, 40)
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF4})
	if !model.activityFocus.active {
		t.Fatal("F4 did not focus the activity")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRight})
	if model.transcript.maxYOffset() <= 0 {
		t.Fatalf("expanded transcript did not grow past the viewport: height=%d", model.transcript.Height)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "detail line 00") {
		t.Fatalf("Right did not expand the activity:\n%s", view)
	}

	model.transcript.SetYOffset(model.transcript.maxYOffset())
	expandedOffset := model.transcript.YOffset
	if expandedOffset == 0 {
		t.Fatalf("expanded transcript did not scroll: max=%d height=%d", model.transcript.maxYOffset(), model.transcript.Height)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyLeft})
	collapsed := utils.StripANSI(model.viewString())
	if strings.Contains(collapsed, "detail line 39") {
		t.Fatalf("Left did not collapse the activity:\n%s", collapsed)
	}
	if model.transcript.YOffset >= expandedOffset {
		t.Fatalf("collapsed viewport kept the scrolled offset %d (expanded %d, max %d)", model.transcript.YOffset, expandedOffset, model.transcript.maxYOffset())
	}
	if !model.transcript.AtBottom() || model.transcript.YOffset != model.transcript.maxYOffset() {
		t.Fatalf("collapsed viewport did not refill to the bottom limit: offset=%d max=%d", model.transcript.YOffset, model.transcript.maxYOffset())
	}
	lines := strings.Split(strings.TrimRight(model.transcriptContent(), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("collapsed transcript is empty")
	}
	if last := strings.TrimSpace(utils.StripANSI(lines[len(lines)-1])); last != "" && !strings.Contains(collapsed, last) {
		t.Fatalf("last transcript row %q is not painted after collapse:\n%s", last, collapsed)
	}

	refilled := model.transcript.YOffset
	_ = model.viewString()
	_ = model.viewString()
	if model.transcript.YOffset != refilled {
		t.Fatalf("viewport position moved across renders: %d -> %d", refilled, model.transcript.YOffset)
	}
}
