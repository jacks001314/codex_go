package audiofiles

import (
	"testing"

	"codex_go/codexapi"
)

func toolAudioOutput(fileID string) map[string]any {
	return map[string]any{
		"type":    "function_call_output",
		"call_id": "call-audio",
		"output": []any{
			map[string]any{"type": "input_audio", "file_id": fileID},
		},
	}
}

// TestToolFileAudioCompetesForAttributionLikeRust mirrors Rust #61394:
// file-backed tool audio is a competing audio and file source, so an audio or
// unknown-file failure cannot be blamed on the user's only uploaded clip.
func TestToolFileAudioCompetesForAttributionLikeRust(t *testing.T) {
	perRecording := &codexapi.APIError{
		Kind:                codexapi.ErrorInvalidAudioInput,
		Message:             "bad clip",
		AudioValidationCode: "invalid_audio",
	}
	unknownFile := &codexapi.APIError{Status: 404, Message: unknownFileMessage}

	userOnly := SubmittedFromRequest([]any{audioMessage("file_user")}, nil, nil)
	if id, ok := userOnly.RejectedFileID(perRecording); !ok || id != "file_user" {
		t.Fatalf("user-only attribution = %q/%v", id, ok)
	}
	if id, ok := userOnly.RejectedFileID(unknownFile); !ok || id != "file_user" {
		t.Fatalf("user-only unknown-file attribution = %q/%v", id, ok)
	}

	toolOnly := SubmittedFromRequest([]any{toolAudioOutput("file_tool")}, nil, nil)
	if id, ok := toolOnly.RejectedFileID(perRecording); ok {
		t.Fatalf("tool audio attribution = %q, want none", id)
	}
	if id, ok := toolOnly.RejectedFileID(unknownFile); ok {
		t.Fatalf("tool audio unknown-file attribution = %q, want none", id)
	}
}

// TestFileAudioIDsIncludeToolFilesLikeRust pins duration lookup coverage:
// file-backed tool clips are resolved with the rest of the request audio.
func TestFileAudioIDsIncludeToolFilesLikeRust(t *testing.T) {
	ids := FileAudioIDs([]any{audioMessage("file_user"), toolAudioOutput("file_tool")})
	if len(ids) != 2 || ids[0] != "file_user" || ids[1] != "file_tool" {
		t.Fatalf("file audio ids = %#v", ids)
	}
}

// TestToolFileAudioNeverJoinsSubmittedFileIDsLikeRust pins the Rust shape of
// SubmittedFileAudio::from_request: only message uploads populate `file_ids`,
// while a tool clip merely competes for the failure. Otherwise a later probe on
// an unknown-file error could persist the tool clip as a rejected user upload.
func TestToolFileAudioNeverJoinsSubmittedFileIDsLikeRust(t *testing.T) {
	submitted := SubmittedFromRequest([]any{audioMessage("file_user"), toolAudioOutput("file_tool")}, nil, nil)
	if submitted == nil {
		t.Fatal("SubmittedFromRequest() = nil")
	}
	if len(submitted.FileIDs) != 1 || submitted.FileIDs[0] != "file_user" {
		t.Fatalf("file ids = %#v, want only the message upload", submitted.FileIDs)
	}
	if !submitted.hasCompetingAudio || !submitted.hasCompetingFile {
		t.Fatalf("competing flags = audio:%v file:%v, want both", submitted.hasCompetingAudio, submitted.hasCompetingFile)
	}
}

// TestPendingConfirmationIgnoresToolAudioLikeRust pins Rust
// pending_audio_confirmation, which only inspects message content: a fresh
// file-backed tool clip must not be persisted as an accepted user upload.
func TestPendingConfirmationIgnoresToolAudioLikeRust(t *testing.T) {
	if pending := PendingConfirmation(nil, "turn", "model", "provider", []any{toolAudioOutput("file_tool")}); pending != nil {
		t.Fatalf("tool-only pending confirmation = %#v, want nil", pending)
	}
	pending := PendingConfirmation(nil, "turn", "model", "provider", []any{audioMessage("file_user"), toolAudioOutput("file_tool")})
	if pending == nil || len(pending.FileIDs) != 1 || pending.FileIDs[0] != "file_user" {
		t.Fatalf("pending confirmation = %#v, want only the message upload", pending)
	}
}
