package realtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAudioChunkValidate(t *testing.T) {
	chunk := &AudioChunk{
		Data:        base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		SampleRate:  24000,
		NumChannels: 1,
	}
	if err := chunk.Validate(); err != nil {
		t.Fatalf("validate audio: %v", err)
	}
	chunk.Data = "not-base64"
	if err := chunk.Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected invalid audio, got %v", err)
	}
}

func TestStartParamsNormalizeDefaultsAndPrompt(t *testing.T) {
	includeStartup := false
	var params StartParams
	if err := json.Unmarshal([]byte(`{
		"threadId":"thread-a",
		"outputModality":"audio",
		"prompt":null,
		"transport":{"type":"webrtc","sdp":"offer"}
	}`), &params); err != nil {
		t.Fatalf("unmarshal start params: %v", err)
	}
	params.IncludeStartupContext = &includeStartup
	config, err := params.Normalized("rt-model", "", "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if config.Version != VersionV2 || config.Voice != VoiceMarin || config.Model != "rt-model" {
		t.Fatalf("defaults not applied: %+v", config)
	}
	if !config.PromptSet || config.Prompt != "" {
		t.Fatalf("prompt null semantics not preserved: %+v", config)
	}
	if config.IncludeStartupContext {
		t.Fatal("include startup context should be false")
	}
	if config.Transport.Type != "webrtc" || config.Transport.SDP != "offer" {
		t.Fatalf("transport = %+v", config.Transport)
	}
}

func TestStartParamsValidateV3InitialItemsAndHandoffMode(t *testing.T) {
	mode := HandoffModeBemTags
	params := StartParams{ThreadID: "thread-v3", OutputModality: OutputText, Version: func() *Version { value := VersionV2; return &value }(), CodexResponseHandoffMode: &mode, InitialItems: []InitialTextItem{{Role: RoleDeveloper, Text: "Remember this."}, {Role: RoleAssistant, Text: "Understood."}}}
	config, err := params.Normalized("model", VersionV2, VoiceMarin)
	if err != nil {
		t.Fatalf("normalize v3 params: %v", err)
	}
	if config.CodexResponseHandoffMode != HandoffModeBemTags || len(config.InitialItems) != 2 || config.InitialItems[0].Role != RoleDeveloper {
		t.Fatalf("config = %#v", config)
	}
	manager := NewManager()
	state, notifications, err := manager.Start(&params)
	if err != nil || state == nil || len(notifications) != 3 {
		t.Fatalf("initial item notifications state=%#v notifications=%#v err=%v", state, notifications, err)
	}
	first := notifications[1].Params.(ItemAddedNotification)
	if first.Item["role"] != string(RoleDeveloper) || first.Item["text"] != "Remember this." {
		t.Fatalf("first initial item = %#v", first.Item)
	}
	tooMany := params
	tooMany.InitialItems = make([]InitialTextItem, 129)
	if err := tooMany.Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected initial item limit error, got %v", err)
	}
}

func TestBuiltinVoices(t *testing.T) {
	voices := BuiltinVoices()
	if voices.DefaultForVersion(VersionV1) != VoiceCove {
		t.Fatalf("default v1 = %s", voices.DefaultForVersion(VersionV1))
	}
	if voices.DefaultForVersion(VersionV2) != VoiceMarin {
		t.Fatalf("default v2 = %s", voices.DefaultForVersion(VersionV2))
	}
	if len(voices.V1) == 0 || len(voices.V2) == 0 {
		t.Fatalf("voices should not be empty: %+v", voices)
	}
}

func TestManagerLifecycle(t *testing.T) {
	manager := NewManager()
	now := fixedTime()
	manager.SetClock(func() time.Time { return now })
	state, notifications, err := manager.Start(&StartParams{
		ThreadID:       "thread-a",
		OutputModality: OutputText,
		Transport:      WebRTCTransport("offer"),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if state.StartedAt != now || state.Config.Version != VersionV2 {
		t.Fatalf("state = %+v", state)
	}
	if len(notifications) != 2 || notifications[0].Method != NotificationStarted || notifications[1].Method != NotificationSDP {
		t.Fatalf("notifications = %+v", notifications)
	}
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText}); !errors.Is(err, ErrRealtimeAlreadyRunning) {
		t.Fatalf("expected already running, got %v", err)
	}

	now = now.Add(time.Second)
	audio := validAudio()
	state, err = manager.AppendAudio(&AppendAudioParams{ThreadID: "thread-a", Audio: audio})
	if err != nil {
		t.Fatalf("append audio: %v", err)
	}
	if state.AudioFrames != 1 || !state.LastActivity.Equal(now) {
		t.Fatalf("audio state = %+v", state)
	}

	state, err = manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "hello", Role: RoleAssistant})
	if err != nil {
		t.Fatalf("append text: %v", err)
	}
	if state.TextInputs != 1 {
		t.Fatalf("text state = %+v", state)
	}

	state, err = manager.AppendSpeech(&AppendSpeechParams{ThreadID: "thread-a", Text: "speak"})
	if err != nil {
		t.Fatalf("append speech: %v", err)
	}
	if state.SpeechInputs != 1 {
		t.Fatalf("speech state = %+v", state)
	}

	state, closed, err := manager.Stop(&StopParams{ThreadID: "thread-a"}, "client")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state.ClosedAt == nil || closed.Method != NotificationClosed {
		t.Fatalf("closed = %+v notification=%+v", state, closed)
	}
	if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "again"}); !errors.Is(err, ErrRealtimeNotRunning) {
		t.Fatalf("expected not running, got %v", err)
	}
}

func TestManagerZeroValueIsUsable(t *testing.T) {
	var manager Manager
	state, notifications, err := manager.Start(&StartParams{
		ThreadID:       "thread-a",
		OutputModality: OutputText,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if state == nil || state.Config.ThreadID != "thread-a" || state.Config.Version != VersionV2 {
		t.Fatalf("state = %+v", state)
	}
	if len(notifications) != 1 || notifications[0].Method != NotificationStarted {
		t.Fatalf("notifications = %+v", notifications)
	}
	state, err = manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "hello"})
	if err != nil {
		t.Fatalf("append text: %v", err)
	}
	if state.TextInputs != 1 {
		t.Fatalf("state after append = %+v", state)
	}
	closed, notification, err := manager.Stop(&StopParams{ThreadID: "thread-a"}, "client")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if closed.ClosedAt == nil || notification.Method != NotificationClosed {
		t.Fatalf("closed = %+v notification=%+v", closed, notification)
	}
}

func TestManagerStreamsCodexHandoffByPhase(t *testing.T) {
	manager := NewManager()
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText}); err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.BeginCodexOutput("thread-a", "commentary", "commentary")
	assertHandoffText(t, manager.StreamCodexOutput("thread-a", "commentary", "working"), "thinking", "working", false)

	manager.BeginCodexOutput("thread-a", "final", "final_answer")
	assertHandoffText(t, manager.StreamCodexOutput("thread-a", "final", "done"), "final", agentFinalMessagePrefix+"done", false)
	if notifications := manager.FinishCodexOutput("thread-a", "final"); len(notifications) != 0 {
		t.Fatalf("unexpected final notifications = %#v", notifications)
	}
	if notifications := manager.FinishCodexOutput("thread-a", "final"); len(notifications) != 0 {
		t.Fatalf("finished stream should only be removed once: %#v", notifications)
	}
}

func TestManagerBoundsCodexHandoffAndPreservesUTF8(t *testing.T) {
	manager := NewManager()
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText}); err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.BeginCodexOutput("thread-a", "item-a", "commentary")
	input := strings.Repeat("界", 2000)
	first := manager.StreamCodexOutput("thread-a", "item-a", input)
	if len(first) != 1 {
		t.Fatalf("first notifications = %#v", first)
	}
	firstText := first[0].Params.(ItemAddedNotification).Item["text"].(string)
	if len(firstText) > codexOutputHeadLimit || !strings.HasPrefix(input, firstText) {
		t.Fatalf("first text bytes=%d valid prefix=%v", len(firstText), strings.HasPrefix(input, firstText))
	}
	finished := manager.FinishCodexOutput("thread-a", "item-a")
	if len(finished) != 1 {
		t.Fatalf("finish notifications = %#v", finished)
	}
	finishText := finished[0].Params.(ItemAddedNotification).Item["text"].(string)
	if !strings.HasPrefix(finishText, codexOutputTruncationText) || len(finishText) > len(codexOutputTruncationText)+codexOutputTailLimit {
		t.Fatalf("finish text bytes=%d", len(finishText))
	}
	if !strings.HasSuffix(input, strings.TrimPrefix(finishText, codexOutputTruncationText)) {
		t.Fatal("finish text does not preserve UTF-8 tail")
	}
}

func TestManagerSkipsAutomaticCodexHandoffModes(t *testing.T) {
	for _, field := range []string{"clientManagedHandoffs", "codexResponsesAsItems"} {
		t.Run(field, func(t *testing.T) {
			value := true
			params := &StartParams{ThreadID: field, OutputModality: OutputText}
			if field == "clientManagedHandoffs" {
				params.ClientManagedHandoffs = &value
			} else {
				params.CodexResponsesAsItems = &value
			}
			manager := NewManager()
			if _, _, err := manager.Start(params); err != nil {
				t.Fatalf("start: %v", err)
			}
			manager.BeginCodexOutput(field, "item", "final_answer")
			if notifications := manager.StreamCodexOutput(field, "item", "ignored"); len(notifications) != 0 {
				t.Fatalf("notifications = %#v", notifications)
			}
		})
	}
}

func assertHandoffText(t *testing.T, notifications []Notification, channel string, text string, final bool) {
	t.Helper()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %#v", notifications)
	}
	params := notifications[0].Params.(ItemAddedNotification)
	if params.Item["type"] != "handoff_append" || params.Item["channel"] != channel || params.Item["text"] != text || params.Item["final"] != final {
		t.Fatalf("handoff item = %#v", params.Item)
	}
}

func TestNotificationFromEvent(t *testing.T) {
	audio := validAudio()
	cases := []struct {
		event Event
		want  NotificationMethod
	}{
		{Event{Type: "input_audio_buffer.speech_started", ItemID: "item-a"}, NotificationItemAdded},
		{Event{Type: "input_transcript.delta", Delta: "he"}, NotificationTranscriptDelta},
		{Event{Type: "input_transcript.done", Text: "hello"}, NotificationTranscriptDone},
		{Event{Type: "output_transcript.delta", Delta: "hi"}, NotificationTranscriptDelta},
		{Event{Type: "output_transcript.done", Text: "hello"}, NotificationTranscriptDone},
		{Event{Type: "audio.out", Audio: &audio}, NotificationOutputAudioDelta},
		{Event{Type: "response.cancelled", ResponseID: "resp"}, NotificationItemAdded},
		{Event{Type: "conversation.item.added", Item: map[string]any{"type": "message"}}, NotificationItemAdded},
		{Event{Type: "handoff.requested", HandoffID: "handoff", ItemID: "item"}, NotificationItemAdded},
		{Event{Type: "error", Message: "boom"}, NotificationError},
	}
	for _, tc := range cases {
		notification, ok := NotificationFromEvent("thread-a", tc.event)
		if !ok {
			t.Fatalf("event %s not mapped", tc.event.Type)
		}
		if notification.Method != tc.want {
			t.Fatalf("event %s method = %s, want %s", tc.event.Type, notification.Method, tc.want)
		}
	}
	if _, ok := NotificationFromEvent("thread-a", Event{Type: "session.updated"}); ok {
		t.Fatal("session.updated should be ignored")
	}
}

func TestAppendValidations(t *testing.T) {
	if err := (&AppendTextParams{ThreadID: "thread-a", Role: "weird", Text: "x"}).Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected invalid role, got %v", err)
	}
	if err := WebRTCTransport("").Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected invalid webrtc, got %v", err)
	}
}

func validAudio() AudioChunk {
	return AudioChunk{
		Data:        base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		SampleRate:  24000,
		NumChannels: 1,
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
