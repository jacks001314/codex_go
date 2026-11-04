package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/audiofiles"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func audioCapableModels() *model.ModelService {
	return model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "gpt-audio", DisplayName: "gpt-audio", Visibility: model.VisibilityVisible, SupportedInAPI: true,
			InputModalities: []string{"text", "image", "audio"}},
		{Slug: "gpt-text", DisplayName: "gpt-text", Visibility: model.VisibilityVisible, SupportedInAPI: true,
			InputModalities: []string{"text", "image"}},
	}}))
}

// TestModelSupportsAudioInputLikeRust pins Rust #59234 admission: only a model
// that declares the audio input modality accepts an uploaded recording.
func TestModelSupportsAudioInputLikeRust(t *testing.T) {
	if !modelSupportsAudioInput(&model.ModelInfo{InputModalities: []string{"text", "audio"}}) {
		t.Fatal("audio modality was not detected")
	}
	if !modelSupportsAudioInput(&model.ModelInfo{InputModalities: []string{"TEXT", " Audio "}}) {
		t.Fatal("audio modality matching must be case insensitive")
	}
	if modelSupportsAudioInput(&model.ModelInfo{InputModalities: []string{"text", "image"}}) {
		t.Fatal("text/image model reported audio support")
	}
	if modelSupportsAudioInput(nil) {
		t.Fatal("nil model info reported audio support")
	}
}

// TestTurnStartAcceptsUploadedAudioForAudioCapableModelLikeRust pins the
// central behavior of #59234: an audio-capable model receives the uploaded
// file reference, the turn succeeds, and the completed request records the
// accepted decoding evidence.
func TestTurnStartAcceptsUploadedAudioForAudioCapableModelLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Models:       audioCapableModels(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:   t.TempDir(),
		Model: "gpt-audio",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input: []turn.TurnUserInput{
			{Type: "text", Text: "listen to this"},
			{Type: "audio", FileID: "file_audio"},
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error = %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	request := <-agent.requests
	found := false
	for _, value := range request.InputItems {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, part := range promptContentParts(item) {
			if strings.TrimSpace(stringFromAny(part["type"])) == "input_audio" &&
				strings.TrimSpace(stringFromAny(part["file_id"])) == "file_audio" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("uploaded audio did not reach inference: %#v", request.InputItems)
	}
	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	accepted := audiofiles.ParseAccepted(record.Metadata.Extra[acceptedAudioMetadataKey])
	if accepted == nil || !accepted.Contains("file_audio") || accepted.Model != "gpt-audio" {
		t.Fatalf("accepted evidence = %#v", record.Metadata.Extra[acceptedAudioMetadataKey])
	}
}

// TestAttributeRejectedAudioLikeRust pins durable rejection recovery: a
// per-recording audio failure blames the only submitted file, persists it, and
// adds the removal guidance to the reported message.
func TestAttributeRejectedAudioLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Models: audioCapableModels()})
	defer router.Close()
	record := &session.Record{ID: "thread-audio", Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-audio"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	items := []any{map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": "listen"},
			{"type": "input_audio", "file_id": "file_audio"},
		},
	}}
	err := router.attributeRejectedAudio(context.Background(), "thread-audio", items,
		&codexapi.APIError{Kind: codexapi.ErrorInvalidAudioInput, Message: "bad clip", AudioValidationCode: "invalid_audio"})
	apiErr, ok := err.(*codexapi.APIError)
	if !ok || !strings.HasSuffix(apiErr.Message, audiofiles.RemovalGuidance) {
		t.Fatalf("error = %v, want guidance", err)
	}
	if !router.rejectedAudioFileIDs("thread-audio").Has("file_audio") {
		t.Fatal("rejection was not persisted")
	}
	// A request-wide failure with several files stays ambiguous: nothing is
	// persisted, yet the guidance still reaches the user.
	router2 := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Models: audioCapableModels()})
	defer router2.Close()
	ambiguous := []any{map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]any{
			{"type": "input_audio", "file_id": "file_a"},
			{"type": "input_audio", "file_id": "file_b"},
		},
	}}
	ambiguousErr := router2.attributeRejectedAudio(context.Background(), "thread-audio", ambiguous,
		&codexapi.APIError{Kind: codexapi.ErrorInvalidAudioInput, Message: "too large", AudioValidationCode: "audio_too_large"})
	if ambiguousErr == nil || !strings.HasSuffix(ambiguousErr.Error(), audiofiles.RemovalGuidance) {
		t.Fatalf("ambiguous error = %v", ambiguousErr)
	}
	if router2.rejectedAudioFileIDs("thread-audio").Has("file_a") || router2.rejectedAudioFileIDs("thread-audio").Has("file_b") {
		t.Fatal("an ambiguous failure blamed a file")
	}
}

// TestRejectedAudioIsProjectedOutOfRequestsLikeRust pins that retained history
// keeps the attachment while later requests omit a confirmed rejection.
func TestRejectedAudioIsProjectedOutOfRequestsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Models: audioCapableModels()})
	defer router.Close()
	record := &session.Record{ID: "thread-projection", Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-audio"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create error = %v", err)
	}
	router.recordRejectedAudioFileIDs("thread-projection", []string{"file_audio"})
	if _, err := router.runtimeAppendItems("thread-projection", []session.Item{{
		ID: "user-1", Type: "message", Role: "user", Text: "listen",
		Content: []session.ContentPart{
			{Type: "input_text", Text: "listen"},
			{Type: "input_audio", FileID: "file_audio"},
		},
	}}); err != nil {
		t.Fatalf("runtimeAppendItems error = %v", err)
	}
	items, _ := router.historyInputItemsForTurn("thread-projection")
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, part := range promptContentParts(item) {
			if strings.TrimSpace(stringFromAny(part["file_id"])) == "file_audio" {
				t.Fatalf("rejected audio reached the request: %#v", items)
			}
		}
	}
	stored, err := store.Read(session.ThreadID("thread-projection"), true, true)
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	kept := false
	for _, item := range stored.Items {
		for _, part := range item.Content {
			if part.FileID == "file_audio" {
				kept = true
			}
		}
	}
	if !kept {
		t.Fatal("retained history lost the attachment")
	}
}

// TestCompactHistoryProjectsExpiredAudioLikeRust pins the compaction
// projection: only the confirmed expired recording is removed and an emptied
// user message disappears.
func TestCompactHistoryProjectsExpiredAudioLikeRust(t *testing.T) {
	history := []compact.Item{
		{Type: "message", Role: "user", Content: []compact.ContentPart{
			{Type: "input_audio", FileID: "file_old"},
		}},
		{Type: "message", Role: "user", Content: []compact.ContentPart{
			{Type: "input_text", Text: "keep"},
			{Type: "input_audio", FileID: "file_keep"},
		}},
	}
	projected := compactHistoryWithoutFileAudio(history, audiofiles.RejectedSet{"file_old": {}})
	if len(projected) != 1 {
		t.Fatalf("projected = %#v", projected)
	}
	if len(projected[0].Content) != 2 || projected[0].Content[1].FileID != "file_keep" {
		t.Fatalf("projected content = %#v", projected[0].Content)
	}
	if len(history) != 2 || len(history[0].Content) != 1 {
		t.Fatal("the input history was mutated")
	}
	if id, ok := compactionRejectedAudioID([]compact.Item{history[0]},
		&codexapi.APIError{Kind: codexapi.ErrorInvalidAudioInput, Message: "bad", AudioValidationCode: "invalid_audio"}); !ok || id != "file_old" {
		t.Fatalf("compaction attribution = %q/%v", id, ok)
	}
}

func promptContentParts(item map[string]any) []map[string]any {
	switch content := item["content"].(type) {
	case []map[string]any:
		return content
	case []any:
		out := make([]map[string]any, 0, len(content))
		for _, raw := range content {
			if part, ok := raw.(map[string]any); ok {
				out = append(out, part)
			}
		}
		return out
	default:
		return nil
	}
}
