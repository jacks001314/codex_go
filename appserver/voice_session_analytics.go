package appserver

import (
	"strings"
	"time"
)

const maxVoiceSessionWindows = 17 // one active window plus 16 recent closed windows

type voiceSessionWindow struct {
	SessionID string
	StartedAt time.Time
	ClosedAt  *time.Time
}

// trackRealtimeSessionStarted opens a voice-session window, closing the prior
// active window at the same boundary (Rust #51963).
func (r *RuntimeRouter) trackRealtimeSessionStarted(threadID string, realtimeSessionID string, startedAt time.Time) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	r.ensureVoiceSessionWindowsLocked()
	windows := r.voiceSessionWindows[threadID]
	if len(windows) > 0 && windows[len(windows)-1].ClosedAt == nil {
		closedAt := startedAt
		windows[len(windows)-1].ClosedAt = &closedAt
	}
	windows = append(windows, voiceSessionWindow{
		SessionID: strings.TrimSpace(realtimeSessionID),
		StartedAt: startedAt.UTC(),
	})
	if len(windows) > maxVoiceSessionWindows {
		windows = append([]voiceSessionWindow(nil), windows[len(windows)-maxVoiceSessionWindows:]...)
	}
	r.voiceSessionWindows[threadID] = windows
}

// trackRealtimeSessionUpdated updates the ID of the active window and the
// active turn's attribution (Rust AnalyticsFact::RealtimeSessionUpdated).
func (r *RuntimeRouter) trackRealtimeSessionUpdated(threadID string, realtimeSessionID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	windows := r.voiceSessionWindows[threadID]
	if len(windows) == 0 || windows[len(windows)-1].ClosedAt != nil {
		return
	}
	windows[len(windows)-1].SessionID = strings.TrimSpace(realtimeSessionID)
	r.voiceSessionWindows[threadID] = windows
}

// trackRealtimeSessionClosed closes the active voice-session window while
// retaining it for delayed compaction attribution.
func (r *RuntimeRouter) trackRealtimeSessionClosed(threadID string, closedAt time.Time) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if closedAt.IsZero() {
		closedAt = time.Now().UTC()
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	windows := r.voiceSessionWindows[threadID]
	if len(windows) == 0 || windows[len(windows)-1].ClosedAt != nil {
		return
	}
	windows[len(windows)-1].ClosedAt = timePtr(closedAt.UTC())
	r.voiceSessionWindows[threadID] = windows
}

// voiceSessionIDAt resolves the session whose window contains timestamp,
// preferring the most recent matching window.
func (r *RuntimeRouter) voiceSessionIDAt(threadID string, timestamp time.Time) string {
	if r == nil || timestamp.IsZero() {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	timestamp = timestamp.UTC()
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	windows := r.voiceSessionWindows[threadID]
	for i := len(windows) - 1; i >= 0; i-- {
		window := windows[i]
		if window.SessionID == "" || timestamp.Before(window.StartedAt) {
			continue
		}
		if window.ClosedAt != nil && timestamp.After(*window.ClosedAt) {
			continue
		}
		return window.SessionID
	}
	return ""
}

func (r *RuntimeRouter) voiceSessionIDForCompaction(threadID string, startedAt time.Time) string {
	if voiceSessionID := r.voiceSessionIDAt(threadID, startedAt); voiceSessionID != "" {
		return voiceSessionID
	}
	if r != nil && r.services.Realtime != nil {
		return strings.TrimSpace(r.services.Realtime.ActiveRealtimeSessionID(threadID))
	}
	return ""
}

func (r *RuntimeRouter) ensureVoiceSessionWindowsLocked() {
	if r.voiceSessionWindows == nil {
		r.voiceSessionWindows = map[string][]voiceSessionWindow{}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
