package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Mirrors Rust's status-indicator effects test: the shimmer is gated by
// `animations && effects.shimmer`, and a settings write can change it.
func TestWorkingHeaderShimmerHonorsTheEffectLikeRust(t *testing.T) {
	enabled := NewModel(codextui.NewState(nil), Options{})
	if got := enabled.renderWorkingHeader("Working"); got == "Working" {
		t.Fatalf("shimmer must animate by default, got the plain header %q", got)
	}

	shimmerOff := NewModel(codextui.NewState(nil), Options{Effects: &EffectsSettings{Shimmer: false}})
	if got := shimmerOff.renderWorkingHeader("Working"); got != "Working" {
		t.Fatalf("shimmer effect off rendered %q, want the plain header", got)
	}

	animationsOff := false
	noAnimations := NewModel(codextui.NewState(nil), Options{AnimationsEnabled: &animationsOff})
	if got := noAnimations.renderWorkingHeader("Working"); got != "Working" {
		t.Fatalf("animations off rendered %q, want the plain header", got)
	}

	// A settings write refreshes the per-effect preferences.
	model := NewModel(codextui.NewState(nil), Options{})
	model.pendingSettingsRequestID = 3
	model.Update(SettingsWriteResultMsg{
		RequestID: 3,
		Result:    SettingsWriteResult{Effects: &EffectsSettings{Shimmer: false}},
	})
	if got := model.renderWorkingHeader("Working"); got != "Working" {
		t.Fatalf("refreshed shimmer effect rendered %q, want the plain header", got)
	}
}
