package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/voicehost"
)

// newVoiceTestModel returns a model with a running voice session and hooks that
// record what the runtime layer would do.
func newVoiceTestModel(t *testing.T) (*Model, *[]string, *int) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-voice")
	model := NewModel(state, Options{Width: 100, Height: 30})
	answers := &[]string{}
	closes := new(int)
	model.VoiceConversation.BeginVoiceConversation("thread-voice", 7)
	model.onVoiceApplyAnswer = func(_ string, _ uint64, answer string) bubbletea.Cmd {
		*answers = append(*answers, answer)
		return nil
	}
	model.onVoiceCloseHelper = func() { *closes++ }
	return model, answers, closes
}

func TestModelVoiceSessionLifecycle(t *testing.T) {
	model, answers, _ := newVoiceTestModel(t)

	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	if !model.VoiceConversation.BackendStarted {
		t.Fatal("the started notification did not reach the session state")
	}
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{
		Kind: chatwidget.VoiceNotificationSDP,
		Text: "v=0 answer",
	}})
	if len(*answers) != 1 || (*answers)[0] != "v=0 answer" {
		t.Fatalf("answers = %#v", *answers)
	}
	model.Update(VoiceAnswerResultMsg{AttemptID: 7})
	if model.VoiceConversation.Phase != chatwidget.VoicePhaseActive {
		t.Fatalf("phase = %q", model.VoiceConversation.Phase)
	}
}

func TestModelVoiceSessionStaleAnswerIsIgnored(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	model.Update(VoiceAnswerResultMsg{AttemptID: 6})
	if model.VoiceConversation.Phase == chatwidget.VoicePhaseActive {
		t.Fatal("a stale attempt activated the session")
	}
}

func TestModelVoiceSessionRendersEachCaptionOnce(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	model.Update(VoiceAnswerResultMsg{AttemptID: 7})

	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{
		Kind: chatwidget.VoiceNotificationTranscriptDelta,
		Role: chatwidget.VoiceTranscriptUser,
		Text: "hello",
	}})
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{
		Kind: chatwidget.VoiceNotificationTranscriptDone,
		Role: chatwidget.VoiceTranscriptUser,
		Text: "hello",
	}})
	if len(model.VoiceConversation.Accepted) != 1 {
		t.Fatalf("accepted = %#v", model.VoiceConversation.Accepted)
	}

	before := len(model.State.Messages)
	model.refreshVoiceTranscript()
	model.refreshVoiceTranscript()
	if after := len(model.State.Messages); after != before {
		t.Fatalf("re-rendering duplicated captions: %d -> %d", before, after)
	}
}

func TestModelVoiceSessionErrorStopsAndClosesHelper(t *testing.T) {
	model, _, closes := newVoiceTestModel(t)
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	model.Update(VoiceAnswerResultMsg{AttemptID: 7})

	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{
		Kind:    chatwidget.VoiceNotificationError,
		Message: "transport failed",
	}})
	if *closes != 1 {
		t.Fatalf("helper closes = %d", *closes)
	}
	if !model.VoiceConversation.FailureRecorded {
		t.Fatal("the failure was not recorded")
	}
	if model.notice != "transport failed" {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestModelVoiceSessionTimeoutRetriesOnce(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	model.Update(VoiceAnswerResultMsg{AttemptID: 7, Err: voicehost.ConnectionNegotiationTimedOut})
	if model.VoiceConversation.StartupRetry != chatwidget.VoiceRetryWaitingForStop {
		t.Fatalf("retry = %q, phase = %q", model.VoiceConversation.StartupRetry, model.VoiceConversation.Phase)
	}
	if model.notice == "" {
		t.Fatal("the retry was not reported to the user")
	}
}

func TestModelVoiceSessionOtherFailureStops(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.Update(VoiceNotificationMsg{Notification: chatwidget.VoiceNotification{Kind: chatwidget.VoiceNotificationStarted}})
	model.Update(VoiceAnswerResultMsg{AttemptID: 7, Err: errors.New("device failure")})
	if model.VoiceConversation.StartupRetry == chatwidget.VoiceRetryWaitingForStop {
		t.Fatal("a non-retryable failure queued a retry")
	}
	if !model.VoiceConversation.FailureRecorded {
		t.Fatal("the failure was not recorded")
	}
}

func TestModelVoiceStripRendersPhaseActivityAndMeters(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	// Starting without a helper reports connecting and no live capture.
	lines := model.voiceStripLines(80)
	if len(lines) != 2 || !strings.Contains(lines[0], "connecting") {
		t.Fatalf("starting strip = %#v", lines)
	}

	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.onVoicePeaks = func() (uint16, uint16) { return chatwidget.VoiceMeterFullScale, 0 }
	// The meter renders the sample history, so fill it before asserting.
	for index := 0; index < chatwidget.VoiceMeterHistoryCapacity; index++ {
		model.sampleVoicePeaks()
	}

	lines = model.voiceStripLines(80)
	if !strings.Contains(lines[0], "listening") {
		t.Fatalf("active strip = %#v", lines)
	}
	if !strings.Contains(lines[1], strings.Repeat("█", 6)) {
		t.Fatalf("full-scale capture meter missing from %q", lines[1])
	}

	// Muting reports the muted activity and releases the capture meter.
	model.VoiceConversation.ToggleVoiceMicrophone()
	model.sampleVoicePeaks()
	lines = model.voiceStripLines(80)
	if !strings.Contains(lines[0], "muted") {
		t.Fatalf("muted strip = %#v", lines)
	}
	if strings.Contains(lines[1], "█") {
		t.Fatalf("muted capture still rendered a level: %q", lines[1])
	}

	// An inactive session renders no strip at all.
	model.VoiceConversation.ResetVoiceConversation()
	if lines := model.voiceStripLines(80); lines != nil {
		t.Fatalf("inactive strip = %#v", lines)
	}
}

func TestModelVoiceStripActivityFollowsSpeaker(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()

	model.onVoicePeaks = func() (uint16, uint16) { return 0, chatwidget.VoiceMeterFullScale }
	model.sampleVoicePeaks()
	if lines := model.voiceStripLines(80); !strings.Contains(lines[0], "speaking") {
		t.Fatalf("speaking strip = %#v", lines)
	}

	// Silence returns the activity to listening once the hold expires.
	model.onVoicePeaks = func() (uint16, uint16) { return 0, 0 }
	model.sampleVoicePeaks()
	model.speakerActiveUntil = model.currentTime().Add(-time.Second)
	if lines := model.voiceStripLines(80); !strings.Contains(lines[0], "listening") {
		t.Fatalf("listening strip = %#v", lines)
	}
}

func TestModelVoiceStripShowsMuteHint(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	lines := model.voiceStripLines(80)
	if !strings.Contains(lines[0], "ctrl-x") {
		t.Fatalf("mute hint missing from %q", lines[0])
	}
}

func TestModelVoiceMeterTickSamplesWhileRunning(t *testing.T) {
	// An inactive session schedules nothing.
	idle := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	if _, cmd := idle.Update(voiceMeterTickMsg{}); cmd != nil {
		t.Fatal("an inactive session scheduled a meter tick")
	}

	model, _, _ := newVoiceTestModel(t)
	model.onVoicePeaks = func() (uint16, uint16) { return 4096, 2048 }
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	if model.VoiceConversation.Phase != chatwidget.VoicePhaseActive {
		t.Fatalf("phase = %q", model.VoiceConversation.Phase)
	}
	_, cmd := model.Update(voiceMeterTickMsg{})
	if cmd == nil {
		t.Fatal("a running session did not reschedule the meter tick")
	}
	if microphone, speaker := model.VoiceConversation.MicrophonePeak, model.VoiceConversation.SpeakerPeak; microphone != 4096 || speaker != 2048 {
		t.Fatalf("peaks = %d/%d", microphone, speaker)
	}
}

func TestModelVoiceAnswerResultStartsMeterTick(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.onVoicePeaks = func() (uint16, uint16) { return 1, 1 }
	model.VoiceConversation.MarkVoiceBackendStarted()
	cmd := model.handleVoiceAnswerResult(VoiceAnswerResultMsg{AttemptID: 7})
	if model.VoiceConversation.Phase != chatwidget.VoicePhaseActive {
		t.Fatalf("phase = %q", model.VoiceConversation.Phase)
	}
	if cmd == nil {
		t.Fatal("activation did not schedule the first meter sample")
	}
}

func TestModelVoiceMuteShortcutTogglesRunningSession(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	var applied []bool
	model.onVoiceSetMicrophoneMuted = func(muted bool) error {
		applied = append(applied, muted)
		return nil
	}
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()

	model.Update(key(bubbletea.KeyCtrlX))
	if !model.VoiceConversation.MicrophoneMuted {
		t.Fatal("the shortcut did not mute the microphone")
	}
	if len(applied) != 1 || !applied[0] {
		t.Fatalf("applied = %#v", applied)
	}
	if model.notice != "Microphone muted." {
		t.Fatalf("notice = %q", model.notice)
	}

	model.Update(key(bubbletea.KeyCtrlX))
	if model.VoiceConversation.MicrophoneMuted {
		t.Fatal("the shortcut did not unmute the microphone")
	}
	if len(applied) != 2 || applied[1] {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestModelVoiceMuteShortcutIgnoresInactiveSession(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	applied := 0
	model.onVoiceSetMicrophoneMuted = func(bool) error {
		applied++
		return nil
	}
	model.Update(key(bubbletea.KeyCtrlX))
	if applied != 0 {
		t.Fatalf("an inactive session applied %d microphone states", applied)
	}
	if model.VoiceConversation.MicrophoneMuted {
		t.Fatal("an inactive session changed the microphone state")
	}
}

func TestModelVoiceSettingsOpensPickerAndSaves(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	requested := 0
	model.onVoiceSettings = func() bubbletea.Cmd {
		requested++
		return func() bubbletea.Msg {
			return VoiceSettingsMsg{Voices: []string{"alloy", "cove"}, Current: "alloy"}
		}
	}
	var saved []string
	model.onVoiceSaveVoice = func(voice string) bubbletea.Cmd {
		saved = append(saved, voice)
		return func() bubbletea.Msg { return VoiceSavedMsg{Voice: voice} }
	}

	// The command is what the runtime executes; run it directly.
	if cmd := model.requestVoiceSettings(); cmd != nil {
		model.Update(cmd())
	}
	if requested != 1 {
		t.Fatalf("settings requests = %d", requested)
	}
	if model.modal == nil {
		t.Fatal("the voice picker did not open")
	}
	if model.voicePreference != "alloy" {
		t.Fatalf("preference = %q", model.voicePreference)
	}

	if cmd := model.applyVoicePickerOption(chatwidget.VoicePickerOptionPrefix + "cove"); cmd != nil {
		model.Update(cmd())
	}
	if len(saved) != 1 || saved[0] != "cove" {
		t.Fatalf("saved = %#v", saved)
	}
	if model.voicePreference != "cove" {
		t.Fatalf("preference = %q", model.voicePreference)
	}
	if model.notice != "Voice set to cove. Applies to your next voice conversation." {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestModelVoiceSettingsReportsFailures(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	// Without a wired runtime the command reports rather than opening a picker.
	model.requestVoiceSettings()
	if model.modal != nil {
		t.Fatal("a picker opened without a settings reader")
	}
	if model.notice == "" {
		t.Fatal("no notice was reported")
	}

	model.onVoiceSettings = func() bubbletea.Cmd {
		return func() bubbletea.Msg { return VoiceSettingsMsg{Err: errors.New("config unavailable")} }
	}
	model.Update(model.requestVoiceSettings()())
	if model.modal != nil {
		t.Fatal("a picker opened after a settings failure")
	}
	if !strings.Contains(model.notice, "config unavailable") {
		t.Fatalf("notice = %q", model.notice)
	}

	model.onVoiceSaveVoice = func(string) bubbletea.Cmd {
		return func() bubbletea.Msg { return VoiceSavedMsg{Voice: "cove", Err: errors.New("read-only config")} }
	}
	model.Update(model.applyVoicePickerOption(chatwidget.VoicePickerOptionPrefix + "cove")())
	if !strings.Contains(model.notice, "read-only config") {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestModelVoicePickerRejectsUnknownOption(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	called := 0
	model.onVoiceSaveVoice = func(string) bubbletea.Cmd {
		called++
		return nil
	}
	if cmd := model.applyVoicePickerOption("not-a-voice"); cmd != nil {
		t.Fatal("an unknown option produced a command")
	}
	if called != 0 {
		t.Fatal("an unknown option was saved")
	}
	if model.notice == "" {
		t.Fatal("no notice was reported")
	}
}

func TestModelVoiceSpeaksDelegatedAnswer(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	var spoken []string
	model.onVoiceAppendSpeech = func(_ string, text string) bubbletea.Cmd {
		spoken = append(spoken, text)
		return nil
	}

	// The delegation prompt marks the active turn as speakable.
	model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "<realtime_delegation>\n  <input>inspect the workspace</input>\n</realtime_delegation>",
	})
	if !model.VoiceConversation.DelegatedVoiceTurnSpeakable() {
		t.Fatal("the delegation prompt did not mark the turn speakable")
	}
	before := len(model.State.Messages)
	model.applyItemCompleted(&protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "the answer"})

	if len(spoken) != 1 || spoken[0] != "the answer" {
		t.Fatalf("spoken = %#v", spoken)
	}
	if after := len(model.State.Messages); after != before {
		t.Fatalf("a spoken answer was also rendered inline: %d -> %d", before, after)
	}
	if len(model.VoiceConversation.PendingSpeech) != 1 {
		t.Fatalf("pending speech = %#v", model.VoiceConversation.PendingSpeech)
	}
}

func TestModelVoiceRendersNonDelegatedAnswerNormally(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	spoken := 0
	model.onVoiceAppendSpeech = func(string, string) bubbletea.Cmd {
		spoken++
		return nil
	}
	model.applyItemCompleted(&protocol.ThreadItem{ID: "user-1", Type: "user_message", Text: "typed by hand"})
	before := len(model.State.Messages)
	model.applyItemCompleted(&protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "the answer"})
	if spoken != 0 {
		t.Fatalf("a typed turn was spoken %d times", spoken)
	}
	if after := len(model.State.Messages); after <= before {
		t.Fatalf("the answer was not rendered: %d -> %d", before, after)
	}
}

func TestModelVoiceSpeechFailureRestoresAnswer(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.onVoiceAppendSpeech = func(itemID string, text string) bubbletea.Cmd {
		return func() bubbletea.Msg {
			return VoiceSpeechResultMsg{ItemID: itemID, Err: errors.New("speech unavailable")}
		}
	}
	model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "<realtime_delegation><input>work</input></realtime_delegation>",
	})
	before := len(model.State.Messages)
	if cmd := model.applyItemCompleted(&protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "the answer"}); cmd == nil {
		t.Fatal("a speech command was not issued")
	} else {
		model.Update(cmd())
	}
	if after := len(model.State.Messages); after <= before {
		t.Fatalf("a failed spoken answer was not restored: %d -> %d", before, after)
	}
	if len(model.VoiceConversation.PendingSpeech) != 0 {
		t.Fatalf("pending speech = %#v", model.VoiceConversation.PendingSpeech)
	}
	if !strings.Contains(model.notice, "speech unavailable") {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestModelVoiceStopRestoresUndeliveredSpeech(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	// The speech command never resolves, so the answer stays pending.
	model.onVoiceAppendSpeech = func(string, string) bubbletea.Cmd { return nil }
	model.applyItemCompleted(&protocol.ThreadItem{
		ID:   "user-1",
		Type: "user_message",
		Text: "<realtime_delegation><input>work</input></realtime_delegation>",
	})
	model.applyItemCompleted(&protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "the answer"})
	before := len(model.State.Messages)
	model.closeVoiceHelper()
	if after := len(model.State.Messages); after <= before {
		t.Fatalf("undelivered speech was not restored: %d -> %d", before, after)
	}
	if len(model.VoiceConversation.PendingSpeech) != 0 {
		t.Fatalf("pending speech = %#v", model.VoiceConversation.PendingSpeech)
	}
}
