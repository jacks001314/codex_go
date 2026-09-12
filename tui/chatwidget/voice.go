package chatwidget

// Voice conversation state for the local helper-owned session. The TUI starts
// the voice helper, posts its offer through the app-server, applies the remote
// answer, and renders captions. This module owns the observable lifecycle: the
// phase machine, the single startup retry, bounded live transcripts, ordered
// privacy controls, and the meter scale.

import (
	"strings"
	"time"
	"unicode/utf8"
)

// VoiceConversationPhase mirrors the Rust lifecycle.
type VoiceConversationPhase string

const (
	VoicePhaseInactive VoiceConversationPhase = "inactive"
	VoicePhaseStarting VoiceConversationPhase = "starting"
	VoicePhaseActive   VoiceConversationPhase = "active"
	VoicePhaseStopping VoiceConversationPhase = "stopping"
)

// VoiceStartupRetry tracks the single retry allowed after a failed startup.
type VoiceStartupRetry string

const (
	// VoiceRetryAvailable means a retry may still be attempted.
	VoiceRetryAvailable VoiceStartupRetry = "available"
	// VoiceRetryWaitingForStop means the failed helper is still shutting down.
	VoiceRetryWaitingForStop VoiceStartupRetry = "waiting_for_stop"
	// VoiceRetryUsed means the retry budget is spent.
	VoiceRetryUsed VoiceStartupRetry = "used"
)

// VoiceTranscriptRole identifies who spoke.
type VoiceTranscriptRole string

const (
	VoiceTranscriptUser      VoiceTranscriptRole = "user"
	VoiceTranscriptAssistant VoiceTranscriptRole = "assistant"
)

const (
	// VoiceTranscriptMaxBytes bounds one caption, matching the Rust limit.
	VoiceTranscriptMaxBytes = 1024
	// VoiceMaxPendingTranscripts bounds retained captions.
	VoiceMaxPendingTranscripts = 32
	// VoiceMaxReplayTranscripts keeps two extra captions for replay.
	VoiceMaxReplayTranscripts = VoiceMaxPendingTranscripts + 2
	// VoiceMicrophoneMeterInterval is the meter sampling cadence.
	VoiceMicrophoneMeterInterval = 100 * time.Millisecond
	// VoiceMeterNoiseFloor and VoiceMeterFullScale map a raw peak to segments.
	VoiceMeterNoiseFloor = 512
	VoiceMeterFullScale  = 8192
	// VoiceAudioMeterSegments is the rendered meter width.
	VoiceAudioMeterSegments = 5
)

// Telemetry counter names, matching the Rust instrumentation.
const (
	VoiceTelemetrySessionStart     = "codex.voice.session.start"
	VoiceTelemetrySessionConnected = "codex.voice.session.connected"
	VoiceTelemetrySessionEnded     = "codex.voice.session.ended"
	VoiceTelemetrySessionDuration  = "codex.voice.session.duration"
	VoiceTelemetrySessionFailure   = "codex.voice.session.failure"
)

// VoiceTranscriptRecord is one retained caption.
type VoiceTranscriptRecord struct {
	Role     VoiceTranscriptRole
	Text     string
	Complete bool
}

// VoiceConversationState is the TUI's view of one voice session.
type VoiceConversationState struct {
	Phase        VoiceConversationPhase
	StartupRetry VoiceStartupRetry
	AttemptID    uint64
	ThreadID     string

	// BackendStarted and WebRTCConnected gate the active phase: both must be
	// true before captions are live.
	BackendStarted  bool
	WebRTCConnected bool

	MicrophoneMuted bool
	MicrophonePeak  uint16
	SpeakerPeak     uint16

	// Live is the in-progress caption for the other speaker's role.
	Live *VoiceTranscriptRecord
	// Accepted holds completed and in-progress captions in arrival order.
	Accepted []VoiceTranscriptRecord

	ActiveSince     time.Time
	FailureRecorded bool

	// DelegatedSpeakable marks that the active turn came from a realtime
	// delegation whose final answer may still be spoken into the conversation.
	DelegatedSpeakable bool
	// PendingSpeech holds spoken answers awaiting delivery.
	PendingSpeech []PendingVoiceSpeech

	// InputGeneration increments whenever a new user input supersedes the
	// audio of the previous turn.
	InputGeneration uint64
	// TranscriptInputGeneration marks the generation a live user caption
	// belongs to, or nil when no user caption is streaming.
	TranscriptInputGeneration *uint64
	// AssistantTranscriptGeneration marks the generation an assistant caption
	// belongs to, or nil when no assistant caption is streaming.
	AssistantTranscriptGeneration *uint64
	// SpeakerSuppressionGeneration records the generation whose audio the
	// helper was asked to suppress, or nil when audio is audible.
	SpeakerSuppressionGeneration *uint64
	// LatestInputWasVoice reports whether the newest input came from speech.
	LatestInputWasVoice bool
	// InterruptionAcknowledgedUntil keeps the "heard" activity visible.
	InterruptionAcknowledgedUntil time.Time
	// SpeakerActiveUntil keeps the "speaking" activity visible.
	SpeakerActiveUntil time.Time
	// SpeakerLevel is the newest speaker meter level.
	SpeakerLevel uint16
}

// Inactive reports whether no session is running.
func (s VoiceConversationState) Inactive() bool {
	return s.phase() == VoicePhaseInactive
}

// Running reports whether a session is starting, active, or stopping.
func (s VoiceConversationState) Running() bool {
	return s.phase() != VoicePhaseInactive
}

// phase returns the effective phase, treating the zero value as inactive.
func (s VoiceConversationState) phase() VoiceConversationPhase {
	if s.Phase == "" {
		return VoicePhaseInactive
	}
	return s.Phase
}

// retry returns the effective retry budget, treating the zero value as
// available.
func (s VoiceConversationState) retry() VoiceStartupRetry {
	if s.StartupRetry == "" {
		return VoiceRetryAvailable
	}
	return s.StartupRetry
}

// MeterSegments maps a raw peak to the rendered meter width.
func MeterSegments(peak uint16) int {
	if peak <= VoiceMeterNoiseFloor {
		return 0
	}
	span := VoiceMeterFullScale - VoiceMeterNoiseFloor
	scaled := int(peak-VoiceMeterNoiseFloor) * VoiceAudioMeterSegments / span
	if scaled > VoiceAudioMeterSegments {
		scaled = VoiceAudioMeterSegments
	}
	return scaled
}

// VoiceMeterIntensity maps a raw peak to the 0-255 intensity the voice strip's
// history renders, mirroring the Rust meter calibration.
func VoiceMeterIntensity(peak uint16) uint8 {
	if peak <= VoiceMeterNoiseFloor {
		return 0
	}
	span := VoiceMeterFullScale - VoiceMeterNoiseFloor
	scaled := (int(peak-VoiceMeterNoiseFloor)*255 + span - 1) / span
	if scaled > 255 {
		return 255
	}
	return uint8(scaled)
}

// VoiceSpeakerActivityHold keeps the "speaking" activity visible after the
// speaker level returns to silence.
const VoiceSpeakerActivityHold = 500 * time.Millisecond

// VoiceMeterHistoryCapacity bounds the rendered meter history.
const VoiceMeterHistoryCapacity = 12

// VoiceMaxSpeakableFinalTokens bounds a spoken final answer. The host leaves
// room for the backend prefix within its own 1,000-token speech limit.
const VoiceMaxSpeakableFinalTokens = 990

// VoicePendingSpeechCapacity bounds spoken answers awaiting delivery.
const VoicePendingSpeechCapacity = 16

// RealtimeDelegationInput extracts the delegated input from a realtime
// delegation prompt. It reports false for any other text, mirroring the Rust
// parser.
func RealtimeDelegationInput(text string) (string, bool) {
	body, ok := strings.CutPrefix(strings.TrimSpace(text), "<realtime_delegation>")
	if !ok {
		return "", false
	}
	body, ok = strings.CutSuffix(body, "</realtime_delegation>")
	if !ok {
		return "", false
	}
	_, rest, ok := strings.Cut(body, "<input>")
	if !ok {
		return "", false
	}
	input, _, ok := strings.Cut(rest, "</input>")
	if !ok {
		return "", false
	}
	return input, true
}

// VoiceSpeakableFinalText reports whether a final answer fits the speakable
// budget. The host approximates one token per four bytes, matching the shared
// token estimate the rest of the port uses.
func VoiceSpeakableFinalText(text string) bool {
	return (len(text)+3)/4 <= VoiceMaxSpeakableFinalTokens
}

// PendingVoiceSpeech is one spoken answer awaiting delivery.
type PendingVoiceSpeech struct {
	ItemID string
	Text   string
}

// VoiceSpeakerAction is the speaker transition one caption boundary requires.
type VoiceSpeakerAction string

const (
	VoiceSpeakerNone   VoiceSpeakerAction = ""
	VoiceSpeakerMute   VoiceSpeakerAction = "suppress"
	VoiceSpeakerUnmute VoiceSpeakerAction = "release"
)

// VoiceInterruptionAcknowledgment keeps the "heard" activity visible after the
// user interrupts the assistant.
const VoiceInterruptionAcknowledgment = 400 * time.Millisecond

// BeginVoiceConversation moves the state into a new startup attempt.
//
// It deliberately leaves the retry budget untouched: a retry re-enters through
// this function and must not grant itself another retry.
func (s *VoiceConversationState) BeginVoiceConversation(threadID string, attemptID uint64) {
	if s == nil {
		return
	}
	s.Phase = VoicePhaseStarting
	s.ThreadID = threadID
	s.AttemptID = attemptID
	s.BackendStarted = false
	s.WebRTCConnected = false
	s.FailureRecorded = false
}

// MarkVoiceBackendStarted records the app-server accepting the start RPC.
func (s *VoiceConversationState) MarkVoiceBackendStarted() {
	if s == nil || s.phase() != VoicePhaseStarting {
		return
	}
	s.BackendStarted = true
	s.activateIfReady()
}

// MarkVoiceWebRTCConnected records a successful local answer.
func (s *VoiceConversationState) MarkVoiceWebRTCConnected() {
	if s == nil || s.phase() != VoicePhaseStarting {
		return
	}
	s.WebRTCConnected = true
	s.activateIfReady()
}

// activateIfReady promotes a fully started session to the active phase.
func (s *VoiceConversationState) activateIfReady() {
	if !s.BackendStarted || !s.WebRTCConnected {
		return
	}
	s.Phase = VoicePhaseActive
	if s.ActiveSince.IsZero() {
		s.ActiveSince = time.Now()
	}
}

// VoiceStartupOutcome describes how a failed startup may be retried.
type VoiceStartupOutcome struct {
	// Retry reports whether the caller should negotiate once more.
	Retry bool
	// NeedsStop reports whether the failed session must finish stopping first.
	NeedsStop bool
	// Message is the bounded explanation for the user.
	Message string
}

// FailVoiceStartup applies the retry rules for a startup failure.
//
// A negotiation timeout or an early transport close during startup retries
// once; every other failure stops the conversation.
func (s *VoiceConversationState) FailVoiceStartup(negotiationTimedOut bool, earlyTransportClose bool) VoiceStartupOutcome {
	if s == nil {
		return VoiceStartupOutcome{}
	}
	retryable := (negotiationTimedOut || earlyTransportClose) && s.retry() == VoiceRetryAvailable
	if !retryable {
		s.RecordVoiceFailure()
		return VoiceStartupOutcome{}
	}
	s.Phase = VoicePhaseStopping
	s.StartupRetry = VoiceRetryWaitingForStop
	if negotiationTimedOut {
		return VoiceStartupOutcome{
			Retry:     true,
			NeedsStop: true,
			Message:   "Voice connection timed out. Retrying once after cleanup.",
		}
	}
	return VoiceStartupOutcome{
		Retry:     true,
		NeedsStop: true,
		Message:   "Voice connection closed during startup. Retrying once.",
	}
}

// CompleteVoiceStop finalizes a stop and reports whether a queued retry should
// start now.
func (s *VoiceConversationState) CompleteVoiceStop() bool {
	if s == nil {
		return false
	}
	retry := s.retry() == VoiceRetryWaitingForStop
	if retry {
		s.StartupRetry = VoiceRetryUsed
	}
	s.Phase = VoicePhaseInactive
	s.WebRTCConnected = false
	s.BackendStarted = false
	if !retry {
		s.StartupRetry = VoiceRetryUsed
	}
	return retry
}

// BeginVoiceStop moves an active session into the stopping phase.
func (s *VoiceConversationState) BeginVoiceStop() {
	if s == nil || s.Inactive() || s.phase() == VoicePhaseStopping {
		return
	}
	s.StartupRetry = VoiceRetryUsed
	s.Phase = VoicePhaseStopping
	s.Live = nil
}

// RecordVoiceFailure counts a failure once per session.
func (s *VoiceConversationState) RecordVoiceFailure() bool {
	if s == nil || s.FailureRecorded {
		return false
	}
	s.FailureRecorded = true
	return true
}

// ConsumeVoiceDuration reports the active duration once, for telemetry.
func (s *VoiceConversationState) ConsumeVoiceDuration() (time.Duration, bool) {
	if s == nil || s.ActiveSince.IsZero() {
		return 0, false
	}
	duration := time.Since(s.ActiveSince)
	s.ActiveSince = time.Time{}
	return duration, true
}

// ToggleVoiceMicrophone flips the microphone mute state and reports the new
// value.
func (s *VoiceConversationState) ToggleVoiceMicrophone() bool {
	if s == nil {
		return false
	}
	s.MicrophoneMuted = !s.MicrophoneMuted
	return s.MicrophoneMuted
}

// SetVoiceAudioState stores the sampled levels.
func (s *VoiceConversationState) SetVoiceAudioState(microphonePeak uint16, speakerPeak uint16) {
	if s == nil {
		return
	}
	s.MicrophonePeak = microphonePeak
	s.SpeakerPeak = speakerPeak
}

// ApplyVoiceTranscriptDelta appends live caption text for a role.
func (s *VoiceConversationState) ApplyVoiceTranscriptDelta(role VoiceTranscriptRole, delta string) {
	if s == nil || delta == "" || !s.Running() {
		return
	}
	if s.Live == nil || s.Live.Role != role {
		// The other speaker interleaved; keep only the newest live caption.
		s.Live = &VoiceTranscriptRecord{Role: role}
	}
	s.Live.Text = truncateVoiceTranscript(s.Live.Text + delta)
}

// ApplyVoiceTranscriptDone seals the live caption for a role.
func (s *VoiceConversationState) ApplyVoiceTranscriptDone(role VoiceTranscriptRole, text string) {
	if s == nil || !s.Running() {
		return
	}
	text = truncateVoiceTranscript(strings.TrimSpace(text))
	if text == "" {
		if s.Live != nil && s.Live.Role == role {
			s.Live = nil
		}
		return
	}
	if s.Live != nil && s.Live.Role == role && s.Live.Text != "" {
		// A separate final can share a prefix with the live caption; show it in
		// full rather than guessing it extends that caption.
		if !strings.HasPrefix(text, s.Live.Text) {
			s.appendVoiceTranscript(role, text)
			s.Live = nil
			return
		}
		text = truncateVoiceTranscript(s.Live.Text + strings.TrimPrefix(text, s.Live.Text))
	}
	s.appendVoiceTranscript(role, text)
	s.Live = nil
}

// FinishVoiceLiveTranscripts seals any caption still streaming, which a stop or
// transport close can leave behind.
func (s *VoiceConversationState) FinishVoiceLiveTranscripts() {
	if s == nil || s.Live == nil {
		return
	}
	live := *s.Live
	s.Live = nil
	if live.Text != "" {
		s.appendVoiceTranscript(live.Role, live.Text)
	}
}

func (s *VoiceConversationState) appendVoiceTranscript(role VoiceTranscriptRole, text string) {
	for len(s.Accepted) >= VoiceMaxReplayTranscripts {
		s.Accepted = s.Accepted[1:]
	}
	s.Accepted = append(s.Accepted, VoiceTranscriptRecord{Role: role, Text: text, Complete: true})
}

// ResetVoiceConversation clears session-scoped state but keeps the attempt
// counter and the retained captions.
func (s *VoiceConversationState) ResetVoiceConversation() {
	if s == nil {
		return
	}
	attemptID := s.AttemptID
	accepted := s.Accepted
	*s = VoiceConversationState{AttemptID: attemptID, Accepted: accepted}
}

// IsCurrentVoiceAttempt reports whether a late async result belongs to the
// attempt the TUI is still waiting on.
func (s VoiceConversationState) IsCurrentVoiceAttempt(threadID string, attemptID uint64) bool {
	return s.Running() && s.ThreadID == threadID && s.AttemptID == attemptID
}

// VoiceNotificationKind identifies the realtime notifications the TUI consumes.
type VoiceNotificationKind string

const (
	VoiceNotificationStarted          VoiceNotificationKind = "thread/realtime/started"
	VoiceNotificationItemStarted      VoiceNotificationKind = "thread/realtime/item/started"
	VoiceNotificationItemCompleted    VoiceNotificationKind = "thread/realtime/item/completed"
	VoiceNotificationItemTranscript   VoiceNotificationKind = "thread/realtime/item/transcript/delta"
	VoiceNotificationTranscriptDelta  VoiceNotificationKind = "thread/realtime/transcript/delta"
	VoiceNotificationTranscriptDone   VoiceNotificationKind = "thread/realtime/transcript/done"
	VoiceNotificationSDP              VoiceNotificationKind = "thread/realtime/sdp"
	VoiceNotificationError            VoiceNotificationKind = "thread/realtime/error"
	VoiceNotificationClosed           VoiceNotificationKind = "thread/realtime/closed"
	VoiceNotificationOutputAudioDelta VoiceNotificationKind = "thread/realtime/outputAudio/delta"
)

// VoiceNotification is the decoded shape the TUI needs from the app-server.
type VoiceNotification struct {
	Kind    VoiceNotificationKind
	Role    VoiceTranscriptRole
	Text    string
	Reason  string
	Message string
}

// VoiceNotificationEffect describes how the TUI reacts to one notification.
type VoiceNotificationEffect struct {
	// Handled reports whether the notification belonged to a voice session.
	Handled bool
	// PublishSDP carries a remote answer that must be applied to the helper.
	PublishSDP string
	// CloseHelper reports that the local helper must be closed and reaped.
	CloseHelper bool
	// EndSession reports that the conversation is over.
	EndSession bool
	// Message is a bounded user-facing notice.
	Message string
}

// ApplyVoiceNotification folds one app-server notification into the state.
func (s *VoiceConversationState) ApplyVoiceNotification(notification VoiceNotification) VoiceNotificationEffect {
	if s == nil {
		return VoiceNotificationEffect{}
	}
	switch notification.Kind {
	case VoiceNotificationStarted:
		if s.phase() != VoicePhaseStarting {
			return VoiceNotificationEffect{}
		}
		s.MarkVoiceBackendStarted()
		return VoiceNotificationEffect{Handled: true}
	case VoiceNotificationSDP:
		if s.phase() != VoicePhaseStarting {
			return VoiceNotificationEffect{}
		}
		return VoiceNotificationEffect{Handled: true, PublishSDP: notification.Text}
	case VoiceNotificationItemTranscript, VoiceNotificationTranscriptDelta:
		if !s.Running() {
			return VoiceNotificationEffect{}
		}
		s.ApplyVoiceTranscriptDelta(notification.Role, notification.Text)
		return VoiceNotificationEffect{Handled: true}
	case VoiceNotificationItemCompleted, VoiceNotificationTranscriptDone:
		if !s.Running() {
			return VoiceNotificationEffect{}
		}
		s.ApplyVoiceTranscriptDone(notification.Role, notification.Text)
		return VoiceNotificationEffect{Handled: true}
	case VoiceNotificationOutputAudioDelta:
		return VoiceNotificationEffect{Handled: s.Running()}
	case VoiceNotificationError:
		if !s.Running() {
			return VoiceNotificationEffect{}
		}
		s.RecordVoiceFailure()
		s.BeginVoiceStop()
		return VoiceNotificationEffect{Handled: true, CloseHelper: true, EndSession: true, Message: notification.Message}
	case VoiceNotificationClosed:
		if s.Inactive() {
			// Late transcript events can still arrive after the session ended.
			s.FinishVoiceLiveTranscripts()
			return VoiceNotificationEffect{}
		}
		s.FinishVoiceLiveTranscripts()
		if s.phase() == VoicePhaseStopping && notification.Reason != "requested" {
			return VoiceNotificationEffect{Handled: true}
		}
		retry := s.CompleteVoiceStop()
		effect := VoiceNotificationEffect{Handled: true, CloseHelper: true, EndSession: true}
		if retry {
			s.StartupRetry = VoiceRetryUsed
			return effect
		}
		if notification.Reason != "" && notification.Reason != "error" && !(s.FailureRecorded && notification.Reason == "requested") {
			effect.Message = "Voice conversation ended: " + notification.Reason
		}
		return effect
	default:
		return VoiceNotificationEffect{}
	}
}

// truncateVoiceTranscript bounds caption text at a rune boundary.
func truncateVoiceTranscript(text string) string {
	if len(text) <= VoiceTranscriptMaxBytes {
		return text
	}
	end := VoiceTranscriptMaxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// RecordVoiceSpeechInputNote reports whether a user caption may be treated as a
// voice turn for delegation purposes.
func RecordVoiceSpeechInputNote(role VoiceTranscriptRole, text string) bool {
	return role == VoiceTranscriptUser && strings.TrimSpace(text) != ""
}

// MarkVoiceDelegatedTurn records that the active turn came from a realtime
// delegation. It reports false when no voice session is running, which is when
// the answer must render normally instead of being spoken.
func (s *VoiceConversationState) MarkVoiceDelegatedTurn() bool {
	if s == nil || !s.Running() {
		return false
	}
	s.DelegatedSpeakable = true
	return true
}

// ClearVoiceDelegatedTurn clears the delegated-turn marker.
func (s *VoiceConversationState) ClearVoiceDelegatedTurn() {
	if s == nil {
		return
	}
	s.DelegatedSpeakable = false
}

// DelegatedVoiceTurnSpeakable reports whether the active delegated turn may
// still be spoken.
func (s VoiceConversationState) DelegatedVoiceTurnSpeakable() bool {
	return s.DelegatedSpeakable
}

// TakeVoiceSpeech queues a final answer for spoken delivery. It reports false
// when the active turn was not a speakable delegation, when the answer is
// empty, or when it exceeds the speakable budget; the caller then renders the
// answer normally.
//
// A turn is spoken at most once, so the marker is cleared even when the answer
// is rejected for its size.
func (s *VoiceConversationState) TakeVoiceSpeech(itemID, text string) (string, bool) {
	if s == nil || !s.Running() || !s.DelegatedSpeakable {
		return "", false
	}
	s.DelegatedSpeakable = false
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !VoiceSpeakableFinalText(trimmed) {
		return "", false
	}
	if len(s.PendingSpeech) >= VoicePendingSpeechCapacity {
		s.PendingSpeech = s.PendingSpeech[1:]
	}
	s.PendingSpeech = append(s.PendingSpeech, PendingVoiceSpeech{ItemID: strings.TrimSpace(itemID), Text: trimmed})
	return trimmed, true
}

// AcceptVoiceSpeech drops a delivered spoken answer.
func (s *VoiceConversationState) AcceptVoiceSpeech(itemID string) {
	if s == nil {
		return
	}
	itemID = strings.TrimSpace(itemID)
	for index := range s.PendingSpeech {
		if s.PendingSpeech[index].ItemID == itemID {
			s.PendingSpeech = append(s.PendingSpeech[:index], s.PendingSpeech[index+1:]...)
			return
		}
	}
}

// RestoreVoiceSpeech removes a pending answer and returns its text so the
// caller can render it normally.
func (s *VoiceConversationState) RestoreVoiceSpeech(itemID string) (string, bool) {
	if s == nil {
		return "", false
	}
	itemID = strings.TrimSpace(itemID)
	for index := range s.PendingSpeech {
		if s.PendingSpeech[index].ItemID != itemID {
			continue
		}
		text := s.PendingSpeech[index].Text
		s.PendingSpeech = append(s.PendingSpeech[:index], s.PendingSpeech[index+1:]...)
		return text, true
	}
	return "", false
}

// TakeUndeliveredVoiceSpeech returns and clears every pending answer, which a
// stop or failure must render normally rather than lose.
func (s *VoiceConversationState) TakeUndeliveredVoiceSpeech() []PendingVoiceSpeech {
	if s == nil || len(s.PendingSpeech) == 0 {
		return nil
	}
	pending := s.PendingSpeech
	s.PendingSpeech = nil
	return pending
}

// VoiceCaptionDelta folds one streaming caption delta into the input
// generation state and reports the speaker transition it requires.
//
// A user caption that starts while the session is active supersedes the
// previous turn's audio; an assistant caption belonging to that generation
// releases the suppression.
func (s *VoiceConversationState) VoiceCaptionDelta(role VoiceTranscriptRole, delta string, active bool, now time.Time) VoiceSpeakerAction {
	if s == nil {
		return VoiceSpeakerNone
	}
	hasText := strings.TrimSpace(delta) != ""
	if role == VoiceTranscriptUser && s.TranscriptInputGeneration == nil {
		interrupted := active && !s.MicrophoneMuted && hasText &&
			(s.SpeakerLevel > 0 || now.Before(s.SpeakerActiveUntil))
		s.InputGeneration++
		generation := s.InputGeneration
		s.TranscriptInputGeneration = &generation
		if interrupted {
			s.InterruptionAcknowledgedUntil = now.Add(VoiceInterruptionAcknowledgment)
		}
		return s.suppressVoiceSpeaker()
	}
	if active && role == VoiceTranscriptAssistant && hasText && s.AssistantTranscriptGeneration == nil {
		generation := s.InputGeneration
		s.AssistantTranscriptGeneration = &generation
	}
	action := VoiceSpeakerNone
	if active {
		action = s.resumeVoiceSpeakerFor(role, delta)
	}
	if active && role == VoiceTranscriptUser &&
		s.TranscriptInputGeneration != nil && *s.TranscriptInputGeneration == s.InputGeneration {
		s.LatestInputWasVoice = true
	}
	return action
}

// VoiceCaptionDone folds one completed caption into the input generation state
// and reports the speaker transition it requires.
func (s *VoiceConversationState) VoiceCaptionDone(role VoiceTranscriptRole, text string, active bool, now time.Time) VoiceSpeakerAction {
	if s == nil {
		return VoiceSpeakerNone
	}
	hasText := strings.TrimSpace(text) != ""
	action := VoiceSpeakerNone
	if hasText && s.TranscriptInputGeneration == nil {
		action = s.resumeVoiceSpeakerFor(role, text)
	}
	// Retire the assistant caption ownership before deciding on the user side,
	// so a final chunk cannot un-mute the newest answer.
	if role == VoiceTranscriptAssistant {
		s.AssistantTranscriptGeneration = nil
	}
	if role != VoiceTranscriptUser {
		return action
	}
	if s.TranscriptInputGeneration != nil {
		generation := *s.TranscriptInputGeneration
		s.TranscriptInputGeneration = nil
		if generation == s.InputGeneration {
			s.LatestInputWasVoice = hasText
			if !hasText {
				return s.releaseVoiceSpeaker()
			}
		}
		return action
	}
	if !hasText {
		return action
	}
	s.InputGeneration++
	s.LatestInputWasVoice = true
	return s.suppressVoiceSpeaker()
}

// InvalidateVoiceInputForTyping reports that typed input supersedes the current
// voice answer, so the helper's audio is suppressed before it can play.
func (s *VoiceConversationState) InvalidateVoiceInputForTyping() VoiceSpeakerAction {
	if s == nil || !s.Running() {
		return VoiceSpeakerNone
	}
	s.LatestInputWasVoice = false
	s.InputGeneration++
	return s.suppressVoiceSpeaker()
}

// suppressVoiceSpeaker records the suppressed generation.
func (s *VoiceConversationState) suppressVoiceSpeaker() VoiceSpeakerAction {
	generation := s.InputGeneration
	s.SpeakerSuppressionGeneration = &generation
	s.SpeakerLevel = 0
	s.SpeakerActiveUntil = time.Time{}
	return VoiceSpeakerMute
}

// releaseVoiceSpeaker clears the suppression.
func (s *VoiceConversationState) releaseVoiceSpeaker() VoiceSpeakerAction {
	if s.SpeakerSuppressionGeneration == nil {
		return VoiceSpeakerNone
	}
	s.SpeakerSuppressionGeneration = nil
	return VoiceSpeakerUnmute
}

// resumeVoiceSpeakerFor releases the suppression only for an assistant caption
// that belongs to the suppressed generation of a voice input.
func (s *VoiceConversationState) resumeVoiceSpeakerFor(role VoiceTranscriptRole, text string) VoiceSpeakerAction {
	if role != VoiceTranscriptAssistant || strings.TrimSpace(text) == "" {
		return VoiceSpeakerNone
	}
	if s.AssistantTranscriptGeneration == nil || *s.AssistantTranscriptGeneration != s.InputGeneration {
		return VoiceSpeakerNone
	}
	if !s.LatestInputWasVoice {
		return VoiceSpeakerNone
	}
	if s.SpeakerSuppressionGeneration == nil || *s.SpeakerSuppressionGeneration != s.InputGeneration {
		return VoiceSpeakerNone
	}
	return s.releaseVoiceSpeaker()
}

// VoiceInterruptionAcknowledged reports whether the "heard" activity is still
// visible.
func (s VoiceConversationState) VoiceInterruptionAcknowledged(now time.Time) bool {
	return !s.InterruptionAcknowledgedUntil.IsZero() && now.Before(s.InterruptionAcknowledgedUntil)
}

// VoiceCommandKind classifies one /voice invocation.
type VoiceCommandKind string

const (
	VoiceCommandToggle   VoiceCommandKind = "toggle"
	VoiceCommandStart    VoiceCommandKind = "start"
	VoiceCommandStop     VoiceCommandKind = "stop"
	VoiceCommandMute     VoiceCommandKind = "mute"
	VoiceCommandSettings VoiceCommandKind = "settings"
	VoiceCommandUsage    VoiceCommandKind = "usage"
)

// VoiceCommandUsageText is the bounded usage hint.
const VoiceCommandUsageText = "Usage: /voice [on|off|mute|settings]"

// VoiceCommandDecision is the parsed command.
type VoiceCommandDecision struct {
	Kind    VoiceCommandKind
	Message string
}

// ParseVoiceCommand interprets the arguments to /voice.
func ParseVoiceCommand(args string) VoiceCommandDecision {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "":
		return VoiceCommandDecision{Kind: VoiceCommandToggle}
	case "on", "start":
		return VoiceCommandDecision{Kind: VoiceCommandStart}
	case "off", "stop":
		return VoiceCommandDecision{Kind: VoiceCommandStop}
	case "mute":
		return VoiceCommandDecision{Kind: VoiceCommandMute}
	case "settings":
		return VoiceCommandDecision{Kind: VoiceCommandSettings}
	default:
		return VoiceCommandDecision{Kind: VoiceCommandUsage, Message: VoiceCommandUsageText}
	}
}

// VoiceCommandAvailability describes whether /voice can run right now.
type VoiceCommandAvailability struct {
	// Allowed reports whether the command proceeds.
	Allowed bool
	// Message is the bounded reason when the command is refused.
	Message string
}

// normalizeVoicePhase treats the zero value as inactive.
func normalizeVoicePhase(phase VoiceConversationPhase) VoiceConversationPhase {
	if phase == "" {
		return VoicePhaseInactive
	}
	return phase
}

// VoiceCommandContext is the TUI state that gates a voice command.
type VoiceCommandContext struct {
	Phase             VoiceConversationPhase
	FeatureEnabled    bool
	PlatformSupported bool
	// PlatformMessage overrides the refusal text when voice is unavailable for
	// a reason more specific than the platform (for example a missing runtime).
	PlatformMessage    string
	ThreadID           string
	SideConversation   bool
	BlockedDirectInput bool
}

// CheckVoiceCommandAvailability applies the same gates as the Rust TUI.
func CheckVoiceCommandAvailability(context VoiceCommandContext) VoiceCommandAvailability {
	if normalizeVoicePhase(context.Phase) == VoicePhaseStopping {
		return VoiceCommandAvailability{Message: "Voice conversation is still stopping."}
	}
	if normalizeVoicePhase(context.Phase) != VoicePhaseInactive {
		// Stopping an active session is always allowed.
		return VoiceCommandAvailability{Allowed: true}
	}
	if !context.FeatureEnabled {
		return VoiceCommandAvailability{Message: "Voice conversations are not enabled."}
	}
	if !context.PlatformSupported {
		if message := strings.TrimSpace(context.PlatformMessage); message != "" {
			return VoiceCommandAvailability{Message: message}
		}
		return VoiceCommandAvailability{Message: "Voice requires macOS, an MSVC-based Windows build, or a glibc-based Linux build."}
	}
	if context.SideConversation {
		return VoiceCommandAvailability{Message: "Voice mode is unavailable in side conversations. Return to the main thread first."}
	}
	if context.BlockedDirectInput {
		return VoiceCommandAvailability{Message: "Voice mode is unavailable while another agent owns input."}
	}
	if strings.TrimSpace(context.ThreadID) == "" {
		return VoiceCommandAvailability{Message: "Start a conversation before using voice mode."}
	}
	return VoiceCommandAvailability{Allowed: true}
}
