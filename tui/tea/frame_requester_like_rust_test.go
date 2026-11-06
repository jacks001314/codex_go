package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// Rust frame_requester.rs: an idle UI schedules no draws at all.
func TestIdleModelRequestsNoAnimationFrames(t *testing.T) {
	animations := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		AnimationsEnabled: &animations,
	})
	frames := make(chan bubbletea.Msg, 4)
	model.frames.attach(func(msg bubbletea.Msg) { frames <- msg })

	if model.frameDemandActive() {
		t.Fatal("idle model reported animation frame demand")
	}
	model.syncFrameDemand()
	if !model.animEngine.Paused() {
		t.Fatal("idle model left the animation engine running")
	}
	select {
	case msg := <-frames:
		t.Fatalf("idle model requested a frame: %#v", msg)
	default:
	}
}

// A live animated surface drives the shared chain through the requester, and
// consuming a frame both advances the tick and asks for the next one.
func TestAnimatedSurfaceDrivesFramesThroughRequester(t *testing.T) {
	model, _ := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	frames := make(chan bubbletea.Msg, 8)
	model.frames.attach(func(msg bubbletea.Msg) {
		select {
		case frames <- msg:
		default:
		}
	})
	if !model.frameDemandActive() {
		t.Fatal("visible starfield did not report frame demand")
	}

	model.syncFrameDemand()
	if model.animEngine.Paused() {
		t.Fatal("visible starfield left the animation engine paused")
	}
	waitForFrame(t, frames)

	before := model.animEngine.CurrentTick()
	model.Update(frameRequestMsg{})
	if got := model.animEngine.CurrentTick(); got != before+1 {
		t.Fatalf("tick after frame request = %d, want %d", got, before+1)
	}
	waitForFrame(t, frames)
}

// The chain stops as soon as the last animated surface ends.
func TestFrameDemandStopsWhenAnimationEnds(t *testing.T) {
	model, now := sparkleTestModel(t, "gpt-6-astra", true, DefaultEffectsSettings())
	if view := model.viewString(); view == "" {
		t.Fatal("empty view")
	}
	model.frames.attach(func(bubbletea.Msg) {})
	model.syncFrameDemand()
	if model.animEngine.Paused() {
		t.Fatal("starfield did not start the animation chain")
	}

	*now = (*now).Add(20 * time.Second)
	_ = model.viewString()
	if model.sparkle.phase != sparkleFinished {
		t.Fatalf("sparkle phase after deadline = %v, want finished", model.sparkle.phase)
	}
	if model.frameDemandActive() {
		t.Fatal("expired starfield still reported frame demand")
	}
	model.syncFrameDemand()
	if !model.animEngine.Paused() {
		t.Fatal("animation chain kept running after the starfield ended")
	}
}

func waitForFrame(t *testing.T, frames <-chan bubbletea.Msg) {
	t.Helper()
	select {
	case msg := <-frames:
		if _, ok := msg.(frameRequestMsg); !ok {
			t.Fatalf("unexpected frame message %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame request")
	}
}
