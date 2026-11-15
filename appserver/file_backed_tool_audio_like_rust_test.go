package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/attachmentstore"
	"codex_go/audiofiles"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

const fileBackedToolAudioWAV = "data:audio/wav;base64,UklGRiYAAABXQVZFZm10IBAAAAABAAEAwF0AAIC7AAACABAAZGF0YQIAAAAAAA=="

// TestCompactHistoryPromptKeepsToolAudioAttributionLikeRust pins the compacted
// submitted projection: file-backed tool audio still competes with a user
// upload for an unknown-file error after compaction.
func TestCompactHistoryPromptKeepsToolAudioAttributionLikeRust(t *testing.T) {
	history := []compact.Item{
		{
			Type:    "message",
			Role:    "user",
			Content: []compact.ContentPart{{Type: "input_audio", FileID: "file_user"}},
		},
		{
			Type:   "tool_output",
			CallID: "call-audio",
			Data: map[string]any{"content_items": []any{
				map[string]any{"type": "input_audio", "file_id": "file_tool"},
			}},
		},
	}
	submitted := audiofiles.SubmittedFromRequest(compactHistoryPromptItems(history), nil, nil)
	unknownFile := &codexapi.APIError{Status: 404, Message: "Invalid input: Unknown `file_id` in input content."}
	if id, ok := submitted.RejectedFileID(unknownFile); ok {
		t.Fatalf("compacted attribution = %q, want none", id)
	}
}

// TestRuntimeRouterFileBackedToolAudioPersistsFileReferenceLikeRust covers
// Rust #61394 end to end: with the feature enabled, fresh dynamic-tool audio is
// uploaded once, persisted as a file reference with the audio-aware budget
// marker, and replayed from history without another upload.
func TestRuntimeRouterFileBackedToolAudioPersistsFileReferenceLikeRust(t *testing.T) {
	home := t.TempDir()
	configBody := "model = \"gpt-5\"\n\n[features]\nfile_backed_tool_audio = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	broker := NewServerRequestBroker()
	requests := make(chan *ServerRequest, 1)
	cwd := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:   NewRouter(store),
		Config:         config.NewConfigService(home),
		Turns:          turn.NewTurnService(),
		Agent:          &dynamicToolRuntimeAgent{},
		ThreadStatus:   NewThreadStatusManager(),
		ServerRequests: broker,
		DefaultCWD:     cwd,
	})
	router.SetNotificationSink(sink)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))
	attachments := &recordingTurnAttachmentStore{result: attachmentstore.FileUploadResult("file_tool_audio")}
	router.threads.SetAttachmentStore(attachments)
	defer router.Close()

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.2.3"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "conn-file-backed-audio"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD:          cwd,
		ThreadSource: threadSourcePtr(string(ThreadSourceUser)),
	})
	threadStart.ConnectionID = "conn-file-backed-audio"
	response := router.Handle(threadStart)
	if response.Error != nil {
		t.Fatalf("thread/start error: %+v", response.Error)
	}
	threadID := response.Result.(*ThreadStartResponse).Thread.ID
	turnStart := requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "record the clip",
		CWD:      cwd,
		DynamicTools: []turn.DynamicToolSpec{{
			Type: "namespace",
			Namespace: &turn.DynamicToolNamespaceSpec{
				Name:        "codex_app",
				Description: "Demo namespace tools",
				Tools: []turn.DynamicToolFunctionSpec{{
					Name:        "demo_tool",
					Description: "Demo dynamic tool",
					InputSchema: map[string]any{"type": "object"},
				}},
			},
		}},
	})
	turnStart.ConnectionID = "conn-file-backed-audio"
	response = router.Handle(turnStart)
	if response.Error != nil {
		t.Fatalf("turn/start error: %+v", response.Error)
	}
	turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
	pending := waitForServerRequestFromChannel(t, requests, ServerRequestDynamicToolCall)
	params, ok := pending.Params.(*turn.DynamicToolCallParams)
	if !ok || params.CallID != "dynamic-analytics" {
		t.Fatalf("dynamic params = %#v", pending.Params)
	}
	if ok, err := broker.Resolve(OK(pending.ID, &DynamicToolCallResponse{
		Success: true,
		ContentItems: []DynamicToolCallOutputContent{
			{Type: "inputText", Text: "clip follows"},
			{Type: "inputAudio", AudioURL: fileBackedToolAudioWAV},
		},
	})); err != nil || !ok {
		t.Fatalf("resolve dynamic tool ok=%v err=%v", ok, err)
	}
	dynamicItemID := "tool-output-" + safeIdentifier(turnID) + "-dynamic-analytics"
	waitForItemCompleted(t, sink, dynamicItemID)
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	uploads := attachments.uploads()
	if len(uploads) != 1 {
		t.Fatalf("uploads = %d, want one tool audio upload", len(uploads))
	}
	if uploads[0].Tags[attachmentstore.TagUsageKey] != "tool_audio" || uploads[0].Tags["mime_type"] != "audio/wav" {
		t.Fatalf("upload tags = %#v", uploads[0].Tags)
	}
	if uploads[0].ThreadID != threadID || uploads[0].Ephemeral {
		t.Fatalf("upload scope = thread %q ephemeral %v", uploads[0].ThreadID, uploads[0].Ephemeral)
	}

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	var outputItem *session.Item
	for i := range record.Items {
		if record.Items[i].ID == dynamicItemID {
			outputItem = &record.Items[i]
			break
		}
	}
	if outputItem == nil {
		t.Fatalf("tool output item missing from %#v", record.Items)
	}
	parts, ok := outputItem.Data["content_items"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content items = %#v", outputItem.Data["content_items"])
	}
	audio, _ := parts[1].(map[string]any)
	if strings.TrimSpace(stringFromAny(audio["type"])) != "input_audio" ||
		strings.TrimSpace(stringFromAny(audio["file_id"])) != "file_tool_audio" ||
		strings.TrimSpace(stringFromAny(audio["audio_url"])) != "" {
		t.Fatalf("persisted audio = %#v", audio)
	}
	rawMetadata, ok := outputItem.Data["harness_metadata"]
	if !ok {
		t.Fatalf("missing harness metadata: %#v", outputItem.Data)
	}
	var metadata map[string]any
	switch typed := rawMetadata.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(typed, &metadata); err != nil {
			t.Fatalf("decode harness metadata: %v", err)
		}
	case []byte:
		if err := json.Unmarshal(typed, &metadata); err != nil {
			t.Fatalf("decode harness metadata: %v", err)
		}
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			t.Fatalf("encode harness metadata: %v", err)
		}
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatalf("decode harness metadata: %v", err)
		}
	}
	if applied, _ := metadata["tool_audio_budget_applied"].(bool); !applied {
		t.Fatalf("harness metadata = %#v, want tool_audio_budget_applied", metadata)
	}

	history := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: cwd})
	found := false
	for _, item := range history {
		object, _ := item.(map[string]any)
		if strings.TrimSpace(stringFromAny(object["type"])) != "function_call_output" {
			continue
		}
		output, _ := object["output"].([]any)
		for _, part := range output {
			block, _ := part.(map[string]any)
			if strings.TrimSpace(stringFromAny(block["file_id"])) == "file_tool_audio" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("replayed history lost the file-backed tool audio: %#v", history)
	}
	if len(attachments.uploads()) != 1 {
		t.Fatalf("replay uploaded the clip again: %d uploads", len(attachments.uploads()))
	}
}
