package tui

import (
	"errors"
	"testing"
	"time"
)

func TestFrameRateLimiterAndRequesterMatchRustCore(t *testing.T) {
	t0 := time.Unix(100, 0)
	limiter := FrameRateLimiter{}
	if got := limiter.ClampDeadline(t0); !got.Equal(t0) {
		t.Fatalf("default clamp = %v", got)
	}
	limiter.MarkEmitted(t0)
	if got := limiter.ClampDeadline(t0.Add(time.Millisecond)); !got.Equal(t0.Add(MinFrameInterval)) {
		t.Fatalf("clamped deadline = %v", got.Sub(t0))
	}

	requester := NewFrameRequester()
	requester.ScheduleFrameIn(t0, 100*time.Millisecond)
	requester.ScheduleFrame(t0)
	if requester.NextDeadline == nil || !requester.NextDeadline.Equal(t0) {
		t.Fatalf("next deadline = %#v", requester.NextDeadline)
	}
	if !requester.Advance(t0) || requester.DrawsEmitted != 1 {
		t.Fatalf("advance did not emit: %#v", requester)
	}
	requester.ScheduleFrame(t0.Add(time.Millisecond))
	if requester.NextDeadline == nil || requester.NextDeadline.Before(t0.Add(MinFrameInterval)) {
		t.Fatalf("rate-limited next deadline = %#v", requester.NextDeadline)
	}
}

func TestEventBrokerStreamMappingPauseResumeAndFairness(t *testing.T) {
	broker := NewEventBroker()
	stream := NewEventStreamState(broker)
	stream.PushDraw()
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventFocusLost})
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventKey, Key: "a"})

	event, ok := stream.NextEvent()
	if !ok || event.Kind != TuiEventKey || event.Key != "a" || stream.TerminalFocused {
		t.Fatalf("first event = %#v ok=%v focused=%v", event, ok, stream.TerminalFocused)
	}
	event, ok = stream.NextEvent()
	if !ok || event.Kind != TuiEventDraw {
		t.Fatalf("second event = %#v ok=%v", event, ok)
	}
	broker.PauseEvents()
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventKey, Key: "b"})
	if event, ok := stream.NextEvent(); ok {
		t.Fatalf("paused stream returned %#v", event)
	}
	broker.ResumeEvents()
	event, ok = stream.NextEvent()
	if !ok || event.Kind != TuiEventKey || event.Key != "b" || broker.ResumeCount != 1 {
		t.Fatalf("resumed event = %#v ok=%v broker=%#v", event, ok, broker)
	}
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventFocusGained})
	event, ok = stream.NextEvent()
	if !ok || event.Kind != TuiEventDraw || !stream.TerminalFocused {
		t.Fatalf("focus gained = %#v ok=%v focused=%v", event, ok, stream.TerminalFocused)
	}
}

func TestFocusGainedPreservesAlreadyQueuedKey(t *testing.T) {
	stream := NewEventStreamState(NewEventBroker())
	stream.TerminalFocused = false
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventFocusGained})
	stream.PushTerminalEvent(TerminalEvent{Kind: TerminalEventKey, Key: "f"})

	event, ok := stream.NextEvent()
	if !ok || event.Kind != TuiEventDraw || !stream.TerminalFocused {
		t.Fatalf("focus event = %#v ok=%v focused=%v", event, ok, stream.TerminalFocused)
	}
	event, ok = stream.NextEvent()
	if !ok || event.Kind != TuiEventKey || event.Key != "f" {
		t.Fatalf("queued key = %#v ok=%v", event, ok)
	}
}

func TestKeyboardModeDetectionAndAnsiMatchRust(t *testing.T) {
	yes := "YES"
	no := "0"
	bad := "maybe"
	if value, ok := ParseBoolEnv(&yes); !ok || !value {
		t.Fatalf("parse yes = %v %v", value, ok)
	}
	if value, ok := ParseBoolEnv(&no); !ok || value {
		t.Fatalf("parse no = %v %v", value, ok)
	}
	if _, ok := ParseBoolEnv(&bad); ok {
		t.Fatalf("unexpected bool parse")
	}
	if !KeyboardEnhancementDisabledFor(nil, true, true) || KeyboardEnhancementDisabledFor(&no, true, true) || !KeyboardEnhancementDisabledFor(&yes, false, false) {
		t.Fatalf("keyboard enhancement disable logic mismatch")
	}
	vscode := "vscode"
	windowsTerminal := "WindowsTerminal"
	if !VscodeTerminalDetected(nil, &vscode) || VscodeTerminalDetected(nil, &windowsTerminal) {
		t.Fatalf("vscode detection mismatch")
	}
	tmux := "session"
	csi := "csi-u"
	xterm := "xterm"
	if !TmuxSessionDetected(&tmux, nil) || !TmuxShouldEnableModifyOtherKeysFor(true, &csi) || TmuxShouldEnableModifyOtherKeysFor(true, &xterm) || TmuxShouldEnableModifyOtherKeysFor(false, &csi) {
		t.Fatalf("tmux detection mismatch")
	}
	if ResetKeyboardEnhancementFlagsANSI() != "\x1b[<u" || EnableModifyOtherKeysANSI() != "\x1b[>4;2m" || DisableModifyOtherKeysANSI() != "\x1b[>4;0m" {
		t.Fatalf("keyboard ANSI mismatch")
	}
}

func TestSuspendContextAndTerminalStderrStateMatchRustCore(t *testing.T) {
	ctx := NewSuspendContext()
	ctx.SetCursorY(12)
	ctx.CaptureSuspend(false)
	action, ok := ctx.PrepareResumeAction(nil)
	if !ok || action.Kind != PreparedResumeRealignViewport || action.Viewport.Y != 12 {
		t.Fatalf("inline resume = %#v ok=%v", action, ok)
	}
	ctx.CaptureSuspend(true)
	saved := Rect{Y: 1, Width: 80, Height: 24}
	action, ok = ctx.PrepareResumeAction(&saved)
	if !ok || action.Kind != PreparedResumeRestoreAlt || saved.Y != 12 {
		t.Fatalf("alt resume = %#v saved=%#v ok=%v", action, saved, ok)
	}
	if _, ok := ctx.PrepareResumeAction(nil); ok {
		t.Fatalf("resume action should be consumed")
	}

	state := &TerminalStderrState{}
	guard, err := InstallTerminalStderrGuard(state, true)
	if err != nil || !guard.Active || !state.OwnerActive || !state.Suppressed {
		t.Fatalf("install guard = %#v state=%#v err=%v", guard, state, err)
	}
	if _, err := InstallTerminalStderrGuard(state, true); !errors.Is(err, ErrTerminalStderrAlreadyActive) {
		t.Fatalf("second install err = %v", err)
	}
	PauseTerminalStderr(state)
	if state.Suppressed {
		t.Fatalf("pause should restore stderr")
	}
	ResumeTerminalStderr(state)
	if !state.Suppressed {
		t.Fatalf("resume should suppress stderr")
	}
	guard.Close()
	if guard.Active || state.OwnerActive || state.Suppressed {
		t.Fatalf("closed guard = %#v state=%#v", guard, state)
	}
}
