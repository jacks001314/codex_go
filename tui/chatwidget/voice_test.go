package chatwidget

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceConversationRequiresBothStartupSignals(t *testing.T) {
	var state VoiceConversationState
	if !state.Inactive() {
		t.Fatalf("phase = %q", state.Phase)
	}
	state.BeginVoiceConversation("thread-1", 1)
	if state.Phase != VoicePhaseStarting || !state.Running() {
		t.Fatalf("state = %#v", state)
	}
	state.MarkVoiceBackendStarted()
	if state.Phase != VoicePhaseStarting {
		t.Fatal("the backend alone must not activate the session")
	}
	state.MarkVoiceWebRTCConnected()
	if state.Phase != VoicePhaseActive {
		t.Fatalf("phase = %q", state.Phase)
	}
	if state.ActiveSince.IsZero() {
		t.Fatal("an active session must record when it started")
	}
}

func TestVoiceConversationRetriesNegotiationTimeoutOnce(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	outcome := state.FailVoiceStartup(true, false)
	if !outcome.Retry || !outcome.NeedsStop {
		t.Fatalf("outcome = %#v", outcome)
	}
	if state.StartupRetry != VoiceRetryWaitingForStop || state.Phase != VoicePhaseStopping {
		t.Fatalf("state = %#v", state)
	}
	if !state.CompleteVoiceStop() {
		t.Fatal("the queued retry was not reported")
	}
	if state.StartupRetry != VoiceRetryUsed || state.Phase != VoicePhaseInactive {
		t.Fatalf("state = %#v", state)
	}
}

func TestVoiceConversationDoesNotRetryTwice(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	state.FailVoiceStartup(true, false)
	state.CompleteVoiceStop()
	state.BeginVoiceConversation("thread-1", 2)
	if outcome := state.FailVoiceStartup(true, false); outcome.Retry {
		t.Fatalf("a second timeout retried: %#v", outcome)
	}
	if !state.FailureRecorded {
		t.Fatal("the second failure was not recorded")
	}
}

func TestVoiceConversationRetriesEarlyTransportClose(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	outcome := state.FailVoiceStartup(false, true)
	if !outcome.Retry || !strings.Contains(outcome.Message, "during startup") {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestVoiceConversationDoesNotRetryOtherFailures(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	if outcome := state.FailVoiceStartup(false, false); outcome.Retry {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestVoiceTranscriptKeepsNewestLiveCaption(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()

	state.ApplyVoiceTranscriptDelta(VoiceTranscriptUser, "hello")
	if state.Live == nil || state.Live.Text != "hello" || state.Live.Role != VoiceTranscriptUser {
		t.Fatalf("live = %#v", state.Live)
	}
	// The other speaker interleaves, so only the newest live caption is kept.
	state.ApplyVoiceTranscriptDelta(VoiceTranscriptAssistant, "hi")
	if state.Live.Role != VoiceTranscriptAssistant || state.Live.Text != "hi" {
		t.Fatalf("live = %#v", state.Live)
	}
	state.FinishVoiceLiveTranscripts()
	if state.Live != nil {
		t.Fatalf("live = %#v", state.Live)
	}
	if len(state.Accepted) != 1 || state.Accepted[0].Text != "hi" {
		t.Fatalf("accepted = %#v", state.Accepted)
	}
}

func TestVoiceTranscriptDoneSealsAndBounds(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()

	state.ApplyVoiceTranscriptDelta(VoiceTranscriptUser, "hel")
	state.ApplyVoiceTranscriptDone(VoiceTranscriptUser, "hello")
	if len(state.Accepted) != 1 || state.Accepted[0].Text != "hello" || !state.Accepted[0].Complete {
		t.Fatalf("accepted = %#v", state.Accepted)
	}

	// An empty done clears a stale live caption without recording anything.
	state.ApplyVoiceTranscriptDelta(VoiceTranscriptUser, "")
	state.ApplyVoiceTranscriptDone(VoiceTranscriptUser, "   ")
	if len(state.Accepted) != 1 {
		t.Fatalf("accepted = %#v", state.Accepted)
	}

	for index := 0; index < VoiceMaxReplayTranscripts+8; index++ {
		state.ApplyVoiceTranscriptDone(VoiceTranscriptAssistant, "line")
	}
	if len(state.Accepted) != VoiceMaxReplayTranscripts {
		t.Fatalf("accepted = %d, want %d", len(state.Accepted), VoiceMaxReplayTranscripts)
	}
}

func TestVoiceTranscriptTruncatesAtRuneBoundary(t *testing.T) {
	long := strings.Repeat("a", VoiceTranscriptMaxBytes)
	truncated := truncateVoiceTranscript(long + "extra")
	if len(truncated) != VoiceTranscriptMaxBytes {
		t.Fatalf("length = %d", len(truncated))
	}
	// A multi-byte rune split at the boundary is excluded rather than corrupted.
	multibyte := strings.Repeat("é", VoiceTranscriptMaxBytes)
	result := truncateVoiceTranscript(multibyte)
	if len(result) > VoiceTranscriptMaxBytes {
		t.Fatalf("length = %d", len(result))
	}
	for _, r := range result {
		if r == '\uFFFD' {
			t.Fatal("truncation produced an invalid rune")
		}
	}
}

func TestVoiceNotificationLifecycle(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 7)

	effect := state.ApplyVoiceNotification(VoiceNotification{Kind: VoiceNotificationStarted})
	if !effect.Handled || !state.BackendStarted {
		t.Fatalf("effect = %#v state = %#v", effect, state)
	}
	effect = state.ApplyVoiceNotification(VoiceNotification{Kind: VoiceNotificationSDP, Text: "v=0 answer"})
	if effect.PublishSDP != "v=0 answer" {
		t.Fatalf("effect = %#v", effect)
	}
	state.MarkVoiceWebRTCConnected()
	if state.Phase != VoicePhaseActive {
		t.Fatalf("phase = %q", state.Phase)
	}

	state.ApplyVoiceNotification(VoiceNotification{
		Kind: VoiceNotificationTranscriptDelta,
		Role: VoiceTranscriptUser,
		Text: "hi",
	})
	state.ApplyVoiceNotification(VoiceNotification{
		Kind: VoiceNotificationTranscriptDone,
		Role: VoiceTranscriptUser,
		Text: "hi",
	})
	if len(state.Accepted) != 1 {
		t.Fatalf("accepted = %#v", state.Accepted)
	}

	effect = state.ApplyVoiceNotification(VoiceNotification{Kind: VoiceNotificationClosed, Reason: "requested"})
	if !effect.EndSession || !effect.CloseHelper || !state.Inactive() {
		t.Fatalf("effect = %#v state = %#v", effect, state)
	}
}

func TestVoiceNotificationIgnoresLateEvents(t *testing.T) {
	var state VoiceConversationState
	if effect := state.ApplyVoiceNotification(VoiceNotification{
		Kind: VoiceNotificationTranscriptDelta,
		Role: VoiceTranscriptUser,
		Text: "late",
	}); effect.Handled {
		t.Fatal("an inactive session handled a transcript delta")
	}
	state.BeginVoiceConversation("thread-1", 1)
	if effect := state.ApplyVoiceNotification(VoiceNotification{
		Kind: VoiceNotificationSDP,
		Text: "v=0",
	}); effect.PublishSDP != "v=0" {
		t.Fatalf("effect = %#v", effect)
	}
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()
	// A late close still seals any streaming caption.
	state.ApplyVoiceTranscriptDelta(VoiceTranscriptAssistant, "half")
	state.ApplyVoiceNotification(VoiceNotification{Kind: VoiceNotificationClosed, Reason: "transport_closed"})
	if len(state.Accepted) != 1 || state.Accepted[0].Text != "half" {
		t.Fatalf("accepted = %#v", state.Accepted)
	}
}

func TestVoiceErrorStopsAndRecordsFailure(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()
	effect := state.ApplyVoiceNotification(VoiceNotification{Kind: VoiceNotificationError, Message: "boom"})
	if !effect.Handled || !effect.EndSession || effect.Message != "boom" {
		t.Fatalf("effect = %#v", effect)
	}
	if !state.FailureRecorded {
		t.Fatal("the failure was not recorded")
	}
}

func TestVoiceMicrophoneToggleAndDuration(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 1)
	if !state.ToggleVoiceMicrophone() || !state.MicrophoneMuted {
		t.Fatalf("state = %#v", state)
	}
	if state.ToggleVoiceMicrophone() || state.MicrophoneMuted {
		t.Fatalf("state = %#v", state)
	}

	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()
	state.ActiveSince = time.Now().Add(-time.Second)
	duration, ok := state.ConsumeVoiceDuration()
	if !ok || duration < time.Second {
		t.Fatalf("duration = %v, %v", duration, ok)
	}
	if _, ok := state.ConsumeVoiceDuration(); ok {
		t.Fatal("the duration was reported twice")
	}
}

func TestVoiceAttemptIdentity(t *testing.T) {
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 3)
	if !state.IsCurrentVoiceAttempt("thread-1", 3) {
		t.Fatal("the current attempt was rejected")
	}
	if state.IsCurrentVoiceAttempt("thread-1", 4) || state.IsCurrentVoiceAttempt("thread-2", 3) {
		t.Fatal("a stale attempt was accepted")
	}
	state.ResetVoiceConversation()
	if state.Running() || state.AttemptID != 3 {
		t.Fatalf("state = %#v", state)
	}
}

func TestVoiceMeterSegments(t *testing.T) {
	if MeterSegments(VoiceMeterNoiseFloor) != 0 {
		t.Fatal("the noise floor produced a segment")
	}
	if MeterSegments(VoiceMeterFullScale) != VoiceAudioMeterSegments {
		t.Fatal("full scale did not fill the meter")
	}
	if MeterSegments(0) != 0 {
		t.Fatal("silence produced a segment")
	}
	if MeterSegments(65535) != VoiceAudioMeterSegments {
		t.Fatal("the meter overflowed")
	}
	mid := MeterSegments((VoiceMeterNoiseFloor + VoiceMeterFullScale) / 2)
	if mid <= 0 || mid >= VoiceAudioMeterSegments {
		t.Fatalf("midpoint segments = %d", mid)
	}
}

func TestParseVoiceCommand(t *testing.T) {
	tests := []struct {
		args string
		want VoiceCommandKind
	}{
		{args: "", want: VoiceCommandToggle},
		{args: "  ", want: VoiceCommandToggle},
		{args: "on", want: VoiceCommandStart},
		{args: "START", want: VoiceCommandStart},
		{args: "off", want: VoiceCommandStop},
		{args: "stop", want: VoiceCommandStop},
		{args: "mute", want: VoiceCommandMute},
		{args: "Settings", want: VoiceCommandSettings},
		{args: "bogus", want: VoiceCommandUsage},
	}
	for _, test := range tests {
		t.Run(test.args, func(t *testing.T) {
			decision := ParseVoiceCommand(test.args)
			if decision.Kind != test.want {
				t.Fatalf("kind = %q, want %q", decision.Kind, test.want)
			}
			if test.want == VoiceCommandUsage && decision.Message != VoiceCommandUsageText {
				t.Fatalf("message = %q", decision.Message)
			}
		})
	}
}

func TestCheckVoiceCommandAvailability(t *testing.T) {
	ready := VoiceCommandContext{
		Phase:             VoicePhaseInactive,
		FeatureEnabled:    true,
		PlatformSupported: true,
		ThreadID:          "thread-1",
	}
	if result := CheckVoiceCommandAvailability(ready); !result.Allowed {
		t.Fatalf("result = %#v", result)
	}

	tests := []struct {
		name    string
		context VoiceCommandContext
		want    string
	}{
		{
			name:    "feature disabled",
			context: VoiceCommandContext{},
			want:    "Voice conversations are not enabled.",
		},
		{
			name: "unsupported platform",
			context: VoiceCommandContext{
				FeatureEnabled: true,
			},
			want: "Voice requires macOS, an MSVC-based Windows build, or a glibc-based Linux build.",
		},
		{
			name: "side conversation",
			context: VoiceCommandContext{
				FeatureEnabled:    true,
				PlatformSupported: true,
				SideConversation:  true,
			},
			want: "Voice mode is unavailable in side conversations. Return to the main thread first.",
		},
		{
			name: "blocked input",
			context: VoiceCommandContext{
				FeatureEnabled:     true,
				PlatformSupported:  true,
				BlockedDirectInput: true,
			},
			want: "Voice mode is unavailable while another agent owns input.",
		},
		{
			name: "no thread",
			context: VoiceCommandContext{
				FeatureEnabled:    true,
				PlatformSupported: true,
			},
			want: "Start a conversation before using voice mode.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := CheckVoiceCommandAvailability(test.context)
			if result.Allowed || result.Message != test.want {
				t.Fatalf("result = %#v, want %q", result, test.want)
			}
		})
	}

	stopping := VoiceCommandContext{Phase: VoicePhaseStopping}
	if result := CheckVoiceCommandAvailability(stopping); result.Allowed || result.Message != "Voice conversation is still stopping." {
		t.Fatalf("result = %#v", result)
	}
	active := VoiceCommandContext{Phase: VoicePhaseActive}
	if result := CheckVoiceCommandAvailability(active); !result.Allowed {
		t.Fatalf("result = %#v", result)
	}
}
