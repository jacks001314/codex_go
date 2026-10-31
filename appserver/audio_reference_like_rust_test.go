package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #58430: the app-server wire form keeps the existing inline audio
// shape and adds the flattened uploaded-file shape.
func TestTurnUserInputAudioWireShapesLikeRust(t *testing.T) {
	var inline turnUserInputAlias
	if err := json.Unmarshal([]byte(`{"type":"audio","url":"data:audio/wav;base64,AAA"}`), &inline); err != nil {
		t.Fatal(err)
	}
	if inline.URL == "" || inline.FileID != "" {
		t.Fatalf("inline audio input = %+v", inline)
	}
	var file turnUserInputAlias
	if err := json.Unmarshal([]byte(`{"type":"audio","fileId":"file_audio"}`), &file); err != nil {
		t.Fatal(err)
	}
	if file.URL != "" || file.FileID != "file_audio" {
		t.Fatalf("file audio input = %+v", file)
	}
}

type turnUserInputAlias struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	FileID string `json:"fileId"`
}


// Mirrors Rust validate_injected_response_item: raw injection cannot smuggle an
// uploaded audio reference into history.
func TestValidateResponseItemAudioFilesLikeRust(t *testing.T) {
	inline := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_audio","audio_url":"data:audio/wav;base64,AAA"}]}`)
	if err := validateResponseItemAudioFiles([]json.RawMessage{inline}); err != nil {
		t.Fatalf("inline audio injection rejected: %v", err)
	}
	file := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_audio","file_id":"file_audio"}]}`)
	err := validateResponseItemAudioFiles([]json.RawMessage{file})
	if err == nil || !strings.Contains(err.Error(), uploadedAudioInjectionMessage) {
		t.Fatalf("uploaded audio injection error = %v, want %q", err, uploadedAudioInjectionMessage)
	}
	camel := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"inputAudio","fileId":"file_audio"}]}`)
	if err := validateResponseItemAudioFiles([]json.RawMessage{camel}); err == nil || !strings.Contains(err.Error(), uploadedAudioInjectionMessage) {
		t.Fatalf("camelCase uploaded audio injection error = %v", err)
	}
}

// Mirrors Rust #58430: thread/inject_items rejects uploaded audio before the
// item reaches history.
func TestThreadInjectItemsRejectsUploadedAudioLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	before, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_audio","file_id":"file_audio"},{"type":"input_text","text":"keep me"}]}`)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: threadID, Items: []json.RawMessage{raw}}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode || response.Error.Message != uploadedAudioInjectionMessage {
		t.Fatalf("inject response error = %+v", response.Error)
	}
	after, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != len(before.Items) {
		t.Fatalf("rejected injection changed history: %d -> %d items", len(before.Items), len(after.Items))
	}
}

// Mirrors Rust #58430: history reconstruction keeps mixed inline and
// file-backed audio at their original content positions.
func TestSessionContentPartsPreserveAudioFileIDLikeRust(t *testing.T) {
	content := []any{
		map[string]any{"type": "input_text", "text": "before"},
		map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAA"},
		map[string]any{"type": "input_audio", "file_id": "file_audio"},
		map[string]any{"type": "input_text", "text": "after"},
	}
	parts := sessionContentPartsFromResponseContent(content, "user")
	if len(parts) != 4 {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].AudioURL == "" || parts[1].FileID != "" {
		t.Fatalf("inline audio part = %+v", parts[1])
	}
	if parts[2].AudioURL != "" || parts[2].FileID != "file_audio" {
		t.Fatalf("file audio part = %+v", parts[2])
	}
	if parts[0].Text != "before" || parts[3].Text != "after" {
		t.Fatalf("mixed audio ordering lost: %+v", parts)
	}
}
