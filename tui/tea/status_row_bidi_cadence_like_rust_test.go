package tea

import (
	"testing"
	"time"

	codextui "codex_go/tui"
)

// Rust #61731: bidi-sensitive headers skip the shimmer sweep, so the status row
// only schedules one-second clock ticks unless progress still animates.
func TestStatusRowCadenceForBidiHeadersLikeRust(t *testing.T) {
	now := time.Unix(3_000, 0)
	animationsOn := true
	shimmerOnlyEffects := DefaultEffectsSettings()
	shimmerOnlyEffects.Progress = false
	model, state := statusRowTestModel(&now, 100)
	model.animationsEnabled = animationsOn
	model.effects = shimmerOnlyEffects
	state.SetStatus("running")
	model.syncTaskRunningTimer()

	model.setWorkingStatusHeader("Working")
	if got := model.statusRowFrameInterval(); got != statusRowAnimatedFrame {
		t.Fatalf("left-to-right header interval = %v, want %v", got, statusRowAnimatedFrame)
	}

	model.setWorkingStatusHeader("שלום עולם")
	if got := model.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("bidi header interval = %v, want %v", got, statusRowStaticFrame)
	}

	model.setWorkingStatusHeader("Checking lines ١٢-٣٤")
	if got := model.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("Arabic-number header interval = %v, want %v", got, statusRowStaticFrame)
	}

	// Progress keeps the fast cadence even for bidi headers (Rust's
	// `progress || (shimmer && !contains_rtl_or_arabic_number)`).
	progressingEffects := DefaultEffectsSettings()
	progressing := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animationsOn, Effects: &progressingEffects})
	progressing.now = func() time.Time { return now }
	progressing.State.SetStatus("running")
	progressing.syncTaskRunningTimer()
	progressing.setWorkingStatusHeader("مرحبا")
	if got := progressing.statusRowFrameInterval(); got != statusRowAnimatedFrame {
		t.Fatalf("progress bidi interval = %v, want %v", got, statusRowAnimatedFrame)
	}

	quietEffects := DefaultEffectsSettings()
	quietEffects.Progress = false
	quietEffects.Shimmer = false
	quiet := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, AnimationsEnabled: &animationsOn, Effects: &quietEffects})
	quiet.now = func() time.Time { return now }
	quiet.State.SetStatus("running")
	quiet.syncTaskRunningTimer()
	quiet.setWorkingStatusHeader("Working")
	if got := quiet.statusRowFrameInterval(); got != statusRowStaticFrame {
		t.Fatalf("effects-off interval = %v, want %v", got, statusRowStaticFrame)
	}
}