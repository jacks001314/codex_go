package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// TestWorkingHeaderPhaseRestartsOnChange covers Rust #43921's
// StatusIndicatorWidget::update_header: a changed header restarts the shimmer
// phase, while a repeated update keeps it.
func TestWorkingHeaderPhaseRestartsOnChange(t *testing.T) {
	base := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	current := base
	model := NewModel(nil, Options{Width: 100, Height: 24})
	model.now = func() time.Time { return current }

	model.setWorkingStatusHeader("Working")
	started := model.workingHeaderStartedAt
	if started.IsZero() {
		t.Fatal("setting the header must record its start time")
	}
	current = base.Add(time.Second)
	model.setWorkingStatusHeader("Working")
	if !model.workingHeaderStartedAt.Equal(started) {
		t.Fatal("a repeated header must keep the shimmer phase")
	}
	model.setWorkingStatusHeader("Mapping the app structure")
	if model.workingHeaderStartedAt.Equal(started) || !model.workingHeaderStartedAt.Equal(current) {
		t.Fatalf("changed header start = %v, want %v", model.workingHeaderStartedAt, current)
	}
}

// TestRenderWorkingHeaderShimmerCoversMotionAndFallback covers the status-row
// integration: reduced motion renders the header verbatim, while an animating
// row either emits truecolor band spans (known palette + truecolor) or the dim
// fallback Rust uses for unknown palettes, and always preserves the text.
func TestRenderWorkingHeaderShimmerCoversMotionAndFallback(t *testing.T) {
	disabled := false
	reduced := NewModel(nil, Options{Width: 100, Height: 24, AnimationsEnabled: &disabled})
	if got := reduced.renderWorkingHeader("Thinking hard"); got != "Thinking hard" {
		t.Fatalf("reduced-motion header = %q, want plain text", got)
	}

	restoreColors := codextui.SetDefaultTerminalColorsForTest(&codextui.DefaultColors{
		FG: codextui.RGBColor{R: 240, G: 240, B: 240},
		BG: codextui.RGBColor{R: 16, G: 16, B: 16},
	})
	defer restoreColors()
	animated := NewModel(nil, Options{Width: 100, Height: 24})
	rendered := animated.renderWorkingHeader("Working")
	if !strings.Contains(utils.StripANSI(rendered), "Working") {
		t.Fatalf("animated header lost its text: %q", rendered)
	}
	trueColor := codextui.DetectStdoutColorLevel() == codextui.ColorTrue
	hasTrueColorEscape := strings.Contains(rendered, "\x1b[38;2;")
	if trueColor && !hasTrueColorEscape {
		t.Fatalf("truecolor header missing band escapes: %q", rendered)
	}
	if !trueColor && !strings.Contains(rendered, "\x1b[2m") {
		t.Fatalf("unknown-palette header missing dim fallback: %q", rendered)
	}
}
