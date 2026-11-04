package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/sessionlock"
	"codex_go/tui/chatwidget"
)

// voiceSessionLockedMessage is the Rust #59330 notice shown when the OS session
// locks during a voice call.
const voiceSessionLockedMessage = "Voice stopped because your session locked."

// voiceSessionLockFailedMessage mirrors Rust's monitoring-failure notice.
const voiceSessionLockFailedMessage = "Voice conversation stopped because session lock monitoring failed."

// VoiceSessionLockedMsg reports that the desktop session locked (or that lock
// monitoring failed) while one voice attempt was running.
type VoiceSessionLockedMsg struct {
	AttemptID uint64
	Err       error
}

// voiceLockDetectorValue returns the detector the model observes. Tests inject
// one; production uses the platform detector.
func (m *Model) voiceLockDetectorValue() sessionlock.Detector {
	if m != nil && m.voiceLockDetector != nil {
		return m.voiceLockDetector
	}
	return sessionlock.SystemDetector()
}

// stopVoiceLockWatch cancels an outstanding lock observation.
func (m *Model) stopVoiceLockWatch() {
	if m == nil || m.voiceLockStop == nil {
		return
	}
	close(m.voiceLockStop)
	m.voiceLockStop = nil
}

// watchVoiceSessionLockCmd observes the session for one voice attempt. The
// observer starts before audio so an already locked session resolves at once
// (Rust #59330 checks the initial state before starting media).
func (m *Model) watchVoiceSessionLockCmd(attemptID uint64) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	detector := m.voiceLockDetectorValue()
	if detector == nil {
		return nil
	}
	m.stopVoiceLockWatch()
	stop := make(chan struct{})
	m.voiceLockStop = stop
	return func() bubbletea.Msg {
		result := sessionlock.Wait(detector, sessionlock.DefaultPollInterval, stop)
		if result.Stopped {
			return nil
		}
		return VoiceSessionLockedMsg{AttemptID: attemptID, Err: result.Err}
	}
}

// applyVoiceSessionLocked stops the matching voice attempt with the Rust #59330
// notice and ignores stale notifications from an earlier attempt.
func (m *Model) applyVoiceSessionLocked(message VoiceSessionLockedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if message.AttemptID == 0 || message.AttemptID != m.VoiceConversation.AttemptID {
		return nil
	}
	m.stopVoiceLockWatch()
	text := voiceSessionLockedMessage
	if message.Err != nil {
		text = voiceSessionLockFailedMessage
	}
	if !m.VoiceConversation.Running() {
		m.VoiceConversation.ResetVoiceConversation()
		m.recordVoiceCommandResult(text)
		return nil
	}
	return m.handleVoiceNotification(chatwidget.VoiceNotification{
		Kind:    chatwidget.VoiceNotificationError,
		Message: text,
	})
}

// voiceSessionInitiallyLocked reports whether the session is locked before a
// call opens its media (Rust #59330 initial snapshot).
func (m *Model) voiceSessionInitiallyLocked() bool {
	if m == nil {
		return false
	}
	detector := m.voiceLockDetectorValue()
	if detector == nil {
		return false
	}
	locked, err := detector.Locked()
	return err == nil && locked
}
