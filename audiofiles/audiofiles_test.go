package audiofiles

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/attachmentstore"
	"codex_go/codexapi"
)

func audioMessage(fileIDs ...string) map[string]any {
	content := []any{}
	for _, id := range fileIDs {
		content = append(content,
			map[string]any{"type": "input_text", "text": "<audio>"},
			map[string]any{"type": "input_audio", "file_id": id},
			map[string]any{"type": "input_text", "text": "</audio>"},
		)
	}
	return map[string]any{"type": "message", "role": "user", "content": content}
}

type fakeStore struct {
	missing map[string]bool
	audioMs map[string]uint64
	delay   time.Duration
}

func (s fakeStore) Resolve(request attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.missing[request.FileID] {
		return attachmentstore.AttachmentMetadata{}, errors.New("attachment not found")
	}
	metadata := attachmentstore.AttachmentMetadata{}
	if duration, ok := s.audioMs[request.FileID]; ok {
		value := duration
		metadata.FormatSpecific = attachmentstore.AudioFormatSpecificMetadata(&attachmentstore.AudioMetadata{DurationMs: &value})
	}
	return metadata, nil
}

func (s fakeStore) IsResolvable(fileID string) bool { return !s.missing[fileID] }

func (fakeStore) Upload(attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	return attachmentstore.UploadResult{}, nil
}

// TestResolveCachesMeasuredDurationsLikeRust pins #59268: audio metadata fills
// the thread cache and existing entries are never resolved twice.
func TestResolveCachesMeasuredDurationsLikeRust(t *testing.T) {
	store := fakeStore{audioMs: map[string]uint64{"file_a": 1500, "file_b": 500}}
	durations := NewDurations()
	Resolve(context.Background(), store, durations, []string{"file_a", "file_b"})
	if seconds, ok := durations.Seconds("file_a"); !ok || seconds != 1.5 {
		t.Fatalf("file_a = %v/%v, want 1.5s", seconds, ok)
	}
	if got := durations.Tokens("file_b"); got != 5 {
		t.Fatalf("file_b tokens = %d, want 5", got)
	}
	if got := durations.Tokens("file_unknown"); got != 12000 {
		t.Fatalf("unknown tokens = %d, want 12000", got)
	}
	snapshot := durations.Snapshot()
	if snapshot["file_a"] != 1.5 || snapshot["file_b"] != 0.5 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

// TestResolveKeepsFullBudgetWithoutMetadataLikeRust pins the fallback: a store
// that reports audio without a duration leaves the conservative budget.
func TestResolveKeepsFullBudgetWithoutMetadataLikeRust(t *testing.T) {
	durations := NewDurations()
	Resolve(context.Background(), fakeStore{}, durations, []string{"file_a"})
	if durations.Has("file_a") {
		t.Fatal("unmeasured audio was cached as measured")
	}
	if got := durations.Tokens("file_a"); got != 12000 {
		t.Fatalf("tokens = %d, want 12000", got)
	}
}

// TestPendingConfirmationLikeRust pins Rust pending_audio_confirmation: only a
// request carrying an unknown file confirms decoding, and the evidence is
// scoped to the model and provider.
func TestPendingConfirmationLikeRust(t *testing.T) {
	items := []any{audioMessage("file_a", "file_b")}
	if got := PendingConfirmation(nil, "turn-1", "gpt-5", "openai", items); got == nil || len(got.FileIDs) != 2 {
		t.Fatalf("pending = %#v, want both files", got)
	}
	known := &Accepted{TurnID: "turn-0", Model: "gpt-5", Provider: "openai", FileIDs: []string{"file_a", "file_b"}}
	if got := PendingConfirmation(known, "turn-1", "gpt-5", "openai", items); got != nil {
		t.Fatalf("pending = %#v, want nil for accepted files", got)
	}
	if got := PendingConfirmation(known, "turn-1", "gpt-5", "other", items); got == nil {
		t.Fatal("switching provider must confirm decoding again")
	}
	roundTrip := ParseAccepted(known.Map())
	if roundTrip == nil || roundTrip.Model != "gpt-5" || !roundTrip.Contains("file_b") {
		t.Fatalf("round trip = %#v", roundTrip)
	}
}

// TestSubmittedAttributionLikeRust pins Rust SubmittedFileAudio::rejected_index:
// a per-recording audio error blames the only candidate, a request-wide size
// failure never blames a file when several were submitted, and an ambiguous
// request preserves every attachment.
func TestSubmittedAttributionLikeRust(t *testing.T) {
	single := SubmittedFromRequest([]any{audioMessage("file_a")}, nil, nil)
	perRecording := &codexapi.APIError{Kind: codexapi.ErrorInvalidAudioInput, Message: "bad clip", AudioValidationCode: "invalid_audio"}
	if id, ok := single.RejectedFileID(perRecording); !ok || id != "file_a" {
		t.Fatalf("single attribution = %q/%v", id, ok)
	}
	tooLarge := &codexapi.APIError{Kind: codexapi.ErrorInvalidAudioInput, Message: "too large", AudioValidationCode: "audio_too_large"}
	if _, ok := single.RejectedFileID(tooLarge); !ok {
		t.Fatal("a request-wide size failure still blames the only file")
	}
	several := SubmittedFromRequest([]any{audioMessage("file_a", "file_b")}, nil, nil)
	if _, ok := several.RejectedFileID(tooLarge); ok {
		t.Fatal("request-wide failure must not blame one of several files")
	}
	accepted := &Accepted{Model: "gpt-5", Provider: "openai", FileIDs: []string{"file_a"}}
	uniqueNew := SubmittedFromRequest([]any{audioMessage("file_a", "file_b")}, accepted, nil)
	if id, ok := uniqueNew.RejectedFileID(perRecording); !ok || id != "file_b" {
		t.Fatalf("unique unseen attribution = %q/%v", id, ok)
	}
	withInline := SubmittedFromRequest([]any{audioMessage("file_a"), map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAA"}},
	}}, nil, nil)
	if _, ok := withInline.RejectedFileID(perRecording); ok {
		t.Fatal("inline audio competes for attribution")
	}
	withCheckpoint := SubmittedFromRequest([]any{
		audioMessage("file_a"),
		map[string]any{"type": "compaction", "encrypted_content": "unknown"},
	}, nil, nil)
	if _, ok := withCheckpoint.RejectedFileID(perRecording); ok {
		t.Fatal("an unknown checkpoint competes for attribution")
	}
	knownCheckpoint := SubmittedFromRequest([]any{
		audioMessage("file_a"),
		map[string]any{"type": "compaction", "encrypted_content": "known"},
	}, nil, []string{"known"})
	if id, ok := knownCheckpoint.RejectedFileID(perRecording); !ok || id != "file_a" {
		t.Fatalf("clear checkpoint attribution = %q/%v", id, ok)
	}
}

func TestSubmittedUnknownFileErrorProbesStoreLikeRust(t *testing.T) {
	unknown := &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 404, Message: unknownFileMessage}
	store := fakeStore{missing: map[string]bool{"file_b": true}}
	submitted := SubmittedFromRequest([]any{audioMessage("file_a", "file_b")}, nil, nil)
	ids := submitted.RejectedFileIDs(context.Background(), store, unknown)
	if len(ids) != 1 || ids[0] != "file_b" {
		t.Fatalf("rejected ids = %#v, want [file_b]", ids)
	}
	if id, ok := SubmittedFromRequest([]any{audioMessage("file_a")}, nil, nil).RejectedFileID(unknown); !ok || id != "file_a" {
		t.Fatalf("unique unknown-file attribution = %q/%v", id, ok)
	}
	generic := &codexapi.APIError{Kind: codexapi.ErrorAPI, Status: 404, Message: "not found"}
	if _, ok := SubmittedFromRequest([]any{audioMessage("file_a")}, nil, nil).RejectedFileID(generic); ok {
		t.Fatal("a generic 404 must not blame audio")
	}
}

func TestProjectFileAudioLikeRust(t *testing.T) {
	message := audioMessage("file_a")
	projected := ProjectFileAudio([]any{message}, RejectedSet{"file_a": {}})
	// Rust keeps the surviving harness labels; only the audio part disappears.
	if len(projected) != 1 || len(projected[0].(map[string]any)["content"].([]any)) != 2 {
		t.Fatalf("projected labels = %#v", projected)
	}
	if len(message["content"].([]any)) != 3 {
		t.Fatal("the original history item was mutated")
	}
	unlabelled := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_audio", "file_id": "file_a"},
	}}
	if kept := ProjectFileAudio([]any{unlabelled}, RejectedSet{"file_a": {}}); len(kept) != 0 {
		t.Fatalf("empty user message survived: %#v", kept)
	}
	text := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "keep me"},
		map[string]any{"type": "input_audio", "file_id": "file_a"},
	}}
	projected = ProjectFileAudio([]any{text}, RejectedSet{"file_a": {}})
	if len(projected) != 1 {
		t.Fatalf("projected = %#v", projected)
	}
	content := projected[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "keep me" {
		t.Fatalf("sibling text lost: %#v", content)
	}
	if len(text["content"].([]any)) != 2 {
		t.Fatal("the retained item was mutated")
	}
}

func TestRejectedSetRoundTripLikeRust(t *testing.T) {
	set := RejectedSet{"file_a": {}, "file_b": {}}
	parsed := ParseRejected(set.List())
	if !parsed.Has("file_a") || !parsed.Has("file_b") || len(parsed) != 2 {
		t.Fatalf("parsed = %#v", parsed)
	}
	if len(ParseRejected(nil)) != 0 {
		t.Fatal("nil metadata produced rejections")
	}
}
