package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// Rust #58430: turn/start accepts uploaded audio and rejects it inside the turn
// runner, after the accompanying text is recorded, so the client sees a failed
// turn instead of a refused request.
func TestTurnStartRejectsUploadedAudioInsideTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:   t.TempDir(),
		Model: "gpt-test",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input: []turn.TurnUserInput{
			{Type: "text", Text: "keep me"},
			{Type: "audio", FileID: "file_audio"},
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start must accept the turn instead of refusing it: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusFailed)

	select {
	case request := <-agent.requests:
		t.Fatalf("uploaded audio must not reach inference: %#v", request.InputItems)
	default:
	}

	reported := false
	for _, notification := range sink.List() {
		if notification.Method != NotificationError {
			continue
		}
		payload, ok := notification.Params.(*ErrorNotification)
		if !ok || payload == nil || payload.TurnID != turnID {
			continue
		}
		reported = true
		if !strings.Contains(payload.Error.Message, uploadedAudioDisabledMessage) {
			t.Fatalf("error message = %q, want %q", payload.Error.Message, uploadedAudioDisabledMessage)
		}
		if payload.Error.CodexErrorInfo != "badRequest" {
			t.Fatalf("codexErrorInfo = %q, want badRequest", payload.Error.CodexErrorInfo)
		}
	}
	if !reported {
		t.Fatalf("turn %s failed without an error notification: %#v", turnID, sink.List())
	}

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	recordedText := false
	for _, item := range record.Items {
		if item.Type != "message" || item.Role != "user" {
			continue
		}
		if strings.Contains(item.Text, "keep me") {
			recordedText = true
		}
		for _, part := range item.Content {
			if strings.Contains(strings.ToLower(part.Type), "audio") ||
				strings.TrimSpace(part.AudioURL) != "" {
				t.Fatalf("rejected audio reached history: %#v", part)
			}
		}
	}
	if !recordedText {
		t.Fatalf("the accompanying text was not recorded: %#v", record.Items)
	}
}

// The filtered parameters keep every other input and drop only uploaded audio.
func TestTurnStartParamsWithoutUploadedAudioLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{
		ThreadID: "thread-audio",
		Prompt:   "keep me",
		Input: []turn.TurnUserInput{
			{Type: "text", Text: "keep me"},
			{Type: "audio", FileID: "file_audio"},
			{Type: "audio", URL: "data:audio/wav;base64,AAA"},
		},
	}
	if got, rejected := turnStartParamsWithoutUploadedAudio(&turn.TurnStartParams{ThreadID: "t"}); rejected || got == nil {
		t.Fatalf("params without audio must pass through unchanged: %#v / %t", got, rejected)
	}
	filtered, rejected := turnStartParamsWithoutUploadedAudio(params)
	if !rejected {
		t.Fatal("uploaded audio was not reported")
	}
	if len(params.Input) != 3 {
		t.Fatalf("the caller's parameters were mutated: %#v", params.Input)
	}
	if len(filtered.Input) != 2 {
		t.Fatalf("filtered input = %#v, want the text and the inline audio", filtered.Input)
	}
	if filtered.Input[0].Text != "keep me" || filtered.Input[1].URL == "" {
		t.Fatalf("filtered input = %#v, want the text and the inline audio", filtered.Input)
	}
	if filtered.Prompt != "keep me" || filtered.ThreadID != "thread-audio" {
		t.Fatalf("filtered params lost unrelated fields: %#v", filtered)
	}
}

// Rust #58430: the audio input keeps the uploaded-file arm when it is
// serialized, so a round-trip does not silently drop the file reference.
func TestTurnUserInputAudioKeepsFileIDOnTheWireLikeRust(t *testing.T) {
	encoded, err := json.Marshal(turn.TurnUserInput{Type: "audio", FileID: "file_audio"})
	if err != nil {
		t.Fatalf("marshal uploaded audio: %v", err)
	}
	if !strings.Contains(string(encoded), `"fileId":"file_audio"`) {
		t.Fatalf("uploaded audio wire shape = %s, want the fileId arm", encoded)
	}
	encoded, err = json.Marshal(turn.TurnUserInput{Type: "audio", URL: "data:audio/wav;base64,AAA"})
	if err != nil {
		t.Fatalf("marshal inline audio: %v", err)
	}
	if !strings.Contains(string(encoded), `"url":"data:audio/wav;base64,AAA"`) || strings.Contains(string(encoded), "fileId") {
		t.Fatalf("inline audio wire shape = %s, want only the url arm", encoded)
	}
}
