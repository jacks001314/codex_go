package appserver

import (
	"strings"
	"time"
)

const maxVoiceSessionWindows = 17 // one active window plus 16 recent closed windows

// maxAttributedVoiceTurns bounds the retained turn-to-voice-session map the way
// Rust bounds its pending turn states (MAX_PENDING_TURNS, Rust #46058).
const maxAttributedVoiceTurns = 256

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

// trackVoiceHandoffRequested records that a realtime handoff asked for a turn
// (Rust AnalyticsFact::RealtimeHandoffRequested, #46058). ThreadRealtimeStarted
// already attributes a running turn, so a handoff only needs queueing while no
// turn is active: the turn it starts may begin after the voice session closed.
// Only the thread id is kept - the handoff item's transcript never reaches it.
func (r *RuntimeRouter) trackVoiceHandoffRequested(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	r.ensureVoiceAttributionLocked()
	if _, active := r.voiceActiveTurns[threadID]; active {
		return
	}
	r.voiceHandoffQueue[threadID] = append(r.voiceHandoffQueue[threadID], r.voiceActiveSessions[threadID])
}

// trackVoiceTurnStarted attributes a newly started turn to the voice session
// that steered it. A queued handoff wins over the session still open for the
// thread, including a queued empty value: Rust pops the queue entry as the
// attribution and only falls back to the active session when the queue is empty.
func (r *RuntimeRouter) trackVoiceTurnStarted(threadID string, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	r.ensureVoiceAttributionLocked()
	r.voiceActiveTurns[threadID] = turnID
	voiceSessionID := ""
	if queue := r.voiceHandoffQueue[threadID]; len(queue) > 0 {
		voiceSessionID = queue[0]
		if len(queue) == 1 {
			delete(r.voiceHandoffQueue, threadID)
		} else {
			r.voiceHandoffQueue[threadID] = queue[1:]
		}
	} else {
		voiceSessionID = r.voiceActiveSessions[threadID]
	}
	if voiceSessionID == "" {
		delete(r.voiceTurnSessions, turnID)
	} else {
		r.voiceTurnSessions[turnID] = voiceSessionID
	}
	r.recordAttributedVoiceTurnLocked(turnID)
}

// trackVoiceTurnCompleted clears the thread's active-turn marker so a handoff
// queued afterwards is not swallowed. The turn keeps its attribution until its
// turn analytics event is emitted, which happens after turn/completed.
func (r *RuntimeRouter) trackVoiceTurnCompleted(threadID string, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	if r.voiceActiveTurns[threadID] == turnID {
		delete(r.voiceActiveTurns, threadID)
	}
}

// trackVoiceSessionStarted sets the thread's active voice session and stamps the
// running turn, matching Rust's ThreadRealtimeStarted handling (Rust #46058).
func (r *RuntimeRouter) trackVoiceSessionStarted(threadID string, realtimeSessionID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	realtimeSessionID = strings.TrimSpace(realtimeSessionID)
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	r.ensureVoiceAttributionLocked()
	if realtimeSessionID == "" {
		delete(r.voiceActiveSessions, threadID)
	} else {
		r.voiceActiveSessions[threadID] = realtimeSessionID
	}
	turnID := r.voiceActiveTurns[threadID]
	if turnID == "" {
		return
	}
	if realtimeSessionID == "" {
		delete(r.voiceTurnSessions, turnID)
	} else {
		r.voiceTurnSessions[turnID] = realtimeSessionID
	}
}

// trackVoiceSessionClosed forgets the thread's active voice session while
// keeping queued handoffs, so a turn started after closure stays attributed.
func (r *RuntimeRouter) trackVoiceSessionClosed(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	delete(r.voiceActiveSessions, threadID)
}

// clearVoiceAttribution drops a thread's voice attribution when it is archived
// or closed, matching Rust's ThreadArchived/ThreadClosed cleanup (Rust #46058).
func (r *RuntimeRouter) clearVoiceAttribution(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	delete(r.voiceActiveSessions, threadID)
	delete(r.voiceActiveTurns, threadID)
	delete(r.voiceHandoffQueue, threadID)
}

// voiceSessionIDForTurn reports the voice session recorded when the turn
// started, or "" for a plain text turn.
func (r *RuntimeRouter) voiceSessionIDForTurn(turnID string) string {
	if r == nil {
		return ""
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	r.realtimeEventMu.Lock()
	defer r.realtimeEventMu.Unlock()
	return r.voiceTurnSessions[turnID]
}

func (r *RuntimeRouter) recordAttributedVoiceTurnLocked(turnID string) {
	for _, retained := range r.voiceTurnOrder {
		if retained == turnID {
			return
		}
	}
	r.voiceTurnOrder = append(r.voiceTurnOrder, turnID)
	for len(r.voiceTurnOrder) > maxAttributedVoiceTurns {
		oldest := r.voiceTurnOrder[0]
		r.voiceTurnOrder = r.voiceTurnOrder[1:]
		delete(r.voiceTurnSessions, oldest)
	}
}

func (r *RuntimeRouter) ensureVoiceAttributionLocked() {
	if r.voiceTurnSessions == nil {
		r.voiceTurnSessions = map[string]string{}
	}
	if r.voiceHandoffQueue == nil {
		r.voiceHandoffQueue = map[string][]string{}
	}
	if r.voiceActiveSessions == nil {
		r.voiceActiveSessions = map[string]string{}
	}
	if r.voiceActiveTurns == nil {
		r.voiceActiveTurns = map[string]string{}
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
