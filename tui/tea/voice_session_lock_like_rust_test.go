package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/sessionlock"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

type fakeLockDetector struct {
	locked bool
	err    error
}

func (d fakeLockDetector) Locked() (bool, error) { return d.locked, d.err }

// Mirrors Rust #59330: an already locked session never starts audio and reports
// why voice stopped.
func TestVoiceStartRefusesLockedSessionLikeRust(t *testing.T) {
	started := 0
	state := codextui.NewState(nil)
	state.SetThreadID("thread-voice")
	model := NewModel(state, Options{
		VoiceLockDetector: fakeLockDetector{locked: true},
		OnVoiceConversationStart: func(threadID string, attemptID uint64) bubbletea.Cmd {
			started++
			return nil
		},
	})
	model.applyVoiceCommand("")
	if started != 0 {
		t.Fatalf("voice helper started %d times on a locked session", started)
	}
	if !model.VoiceConversation.Inactive() {
		t.Fatalf("voice conversation phase = %q, want inactive", model.VoiceConversation.Phase)
	}
	if !historyCellContains(model, voiceSessionLockedMessage) {
		t.Fatalf("lock notice missing from the transcript")
	}
}

// Mirrors Rust #59330: a lock during a running call stops the conversation with
// the lock notice, and a stale notification from an earlier attempt is ignored.
func TestVoiceLockStopsRunningConversationAndIgnoresStaleAttemptLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-voice")
	model := NewModel(state, Options{})
	model.VoiceConversation.BeginVoiceConversation("thread-voice", 4)
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()

	if cmd := model.applyVoiceSessionLocked(VoiceSessionLockedMsg{AttemptID: 3}); cmd != nil {
		t.Fatal("stale lock notification produced a command")
	}
	if model.VoiceConversation.Inactive() {
		t.Fatal("stale lock notification stopped the live call")
	}

	model.applyVoiceSessionLocked(VoiceSessionLockedMsg{AttemptID: 4})
	if phase := model.VoiceConversation.Phase; phase != chatwidget.VoicePhaseStopping && !model.VoiceConversation.Inactive() {
		t.Fatalf("voice phase after the lock = %q, want stopping", phase)
	}
	if !historyCellContains(model, voiceSessionLockedMessage) {
		t.Fatalf("lock notice missing from the transcript")
	}
}

// Mirrors Rust #59330: a monitoring failure reports its own message.
func TestVoiceLockMonitoringFailureReportsDedicatedNotice(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-voice")
	model := NewModel(state, Options{})
	model.VoiceConversation.BeginVoiceConversation("thread-voice", 1)
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()

	model.applyVoiceSessionLocked(VoiceSessionLockedMsg{AttemptID: 1, Err: errors.New("boom")})
	if !historyCellContains(model, voiceSessionLockFailedMessage) {
		t.Fatalf("monitoring-failure notice missing from the transcript")
	}
}

// The observer resolves an already locked session without polling.
func TestSessionLockWaitResolvesImmediately(t *testing.T) {
	result := sessionlock.Wait(fakeLockDetector{locked: true}, time.Millisecond, nil)
	if !result.Locked {
		t.Fatalf("Wait() = %+v, want locked", result)
	}
}

func historyCellContains(model *Model, text string) bool {
	if model == nil {
		return false
	}
	if strings.Contains(model.notice, text) || strings.Contains(model.viewString(), text) {
		return true
	}
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, text) || strings.Contains(message.RawText, text) {
			return true
		}
	}
	return false
}
