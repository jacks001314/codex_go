package tea

import (
	"fmt"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// wheelPress builds the press event Bubble Tea reports for wheel motion.
func wheelPress(button bubbletea.MouseButton) bubbletea.MouseMsg {
	return bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: button}
}

// scrollableTranscript builds a model whose transcript can scroll in both
// directions, like the existing transcript navigation tests.
func scrollableTranscript(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 60, Height: 10})
	if model.transcript.YOffset <= 0 {
		t.Fatalf("initial transcript offset = %d, want a scrollable bottom", model.transcript.YOffset)
	}
	return model
}

// applyMouseScrollSpeed stages a resolved `tui.mouse_scroll_speed` through the
// real settings-write path (Rust #50209 threads it from the local settings).
func applyMouseScrollSpeed(t *testing.T, model *Model, speed float64) {
	t.Helper()
	model.pendingSettingsRequestID = 41
	model.Update(SettingsWriteResultMsg{RequestID: 41, Result: SettingsWriteResult{MouseScrollSpeed: &speed}})
	if got := model.MouseScrollSpeed(); got != speed {
		t.Fatalf("live mouse scroll speed = %v, want %v", got, speed)
	}
}

// Rust #50209 (4dd51f4a5f, "Make transcript mouse scroll speed configurable"),
// `TranscriptView::handle_mouse`'s ScrollUp/ScrollDown arm: each wheel event
// moves `tui.mouse_scroll_speed` rows, and the default drops from the previous
// fixed three rows to one.
func TestTranscriptWheelUsesConfiguredMouseScrollSpeedLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		speed float64
		rows  int
	}{
		{name: "default_is_one_row", speed: 1.0, rows: 1},
		{name: "double", speed: 2.0, rows: 2},
		{name: "triple_restores_the_old_speed", speed: 3.0, rows: 3},
		{name: "one_and_a_half", speed: 1.5, rows: 1},
		{name: "half_waits_for_a_whole_row", speed: 0.5, rows: 0},
	} {
		model := scrollableTranscript(t)
		applyMouseScrollSpeed(t, model, tc.speed)
		before := model.transcript.YOffset
		model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
		if got, want := model.transcript.YOffset, before-tc.rows; got != want {
			t.Fatalf("%s: wheel offset = %d, want %d", tc.name, got, want)
		}
	}
}

// Rust #50209: a session that has not received a settings write still gets the
// new default of one row per wheel event (the pre-#50209 wheel moved three).
func TestTranscriptWheelDefaultsToOneRowPerEventLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	if got := model.MouseScrollSpeed(); got != codextui.MouseScrollSpeedDefault {
		t.Fatalf("initial mouse scroll speed = %v, want %v", got, codextui.MouseScrollSpeedDefault)
	}
	before := model.transcript.YOffset
	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got, want := model.transcript.YOffset, before-1; got != want {
		t.Fatalf("default wheel offset = %d, want %d", got, want)
	}
	model.Update(wheelPress(bubbletea.MouseButtonWheelDown))
	if got, want := model.transcript.YOffset, before; got != want {
		t.Fatalf("default wheel-down offset = %d, want %d", got, want)
	}
}

// Rust #50209: a fractional multiplier accumulates movement across events
// (Rust `mouse_scroll_speed_scales_rows_and_accumulates_fractional_movement`)
// and the remainder is discarded rather than cancelled when the direction
// reverses.
func TestTranscriptWheelAccumulatesFractionalScrollLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	applyMouseScrollSpeed(t, model, 0.5)
	before := model.transcript.YOffset

	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got := model.transcript.YOffset; got != before {
		t.Fatalf("first half-row event moved to %d, want %d", got, before)
	}
	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got, want := model.transcript.YOffset, before-1; got != want {
		t.Fatalf("second half-row event moved to %d, want %d", got, want)
	}

	// One more up event keeps half a row pending; the reversed event must start
	// a fresh (positive) remainder instead of cancelling the pending movement.
	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got, want := model.transcript.YOffset, before-1; got != want {
		t.Fatalf("half-row event after a whole row moved to %d, want %d", got, want)
	}
	model.Update(wheelPress(bubbletea.MouseButtonWheelDown))
	if got, want := model.transcript.YOffset, before-1; got != want {
		t.Fatalf("reversed half-row event moved to %d, want %d", got, want)
	}
	if got := model.mouseScroll.PendingRows(); got <= 0 {
		t.Fatalf("reversed wheel kept pending rows %v, want a fresh positive remainder", got)
	}
}

// Rust #50209: the fullscreen transcript pager consumes the same multiplier
// (Rust threads it into `TranscriptOverlay::new`). The Go pager reads the value
// both when it is created and when a reload arrives while it is open.
func TestTranscriptOverlayWheelUsesConfiguredMouseScrollSpeedLikeRust(t *testing.T) {
	openOverlay := func(t *testing.T, model *Model) *Model {
		t.Helper()
		updated, _ := model.Update(key(bubbletea.KeyCtrlT))
		model = updated.(*Model)
		if model.overlay == nil {
			t.Fatal("Ctrl+T did not open the transcript overlay")
		}
		if model.overlay.YOffset() <= 0 {
			t.Fatalf("overlay initial offset = %d, want a scrollable bottom", model.overlay.YOffset())
		}
		return model
	}

	// A speed resolved before the pager opens seeds the new overlay.
	model := scrollableTranscript(t)
	applyMouseScrollSpeed(t, model, 3.0)
	model = openOverlay(t, model)
	before := model.overlay.YOffset()
	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got, want := model.overlay.YOffset(), before-3; got != want {
		t.Fatalf("triple-speed overlay wheel moved to %d, want %d", got, want)
	}

	// A reload that lands while the pager is open retargets the live overlay.
	applyMouseScrollSpeed(t, model, 1.0)
	before = model.overlay.YOffset()
	model.Update(wheelPress(bubbletea.MouseButtonWheelUp))
	if got, want := model.overlay.YOffset(), before-1; got != want {
		t.Fatalf("reloaded overlay wheel moved to %d, want %d", got, want)
	}
}
