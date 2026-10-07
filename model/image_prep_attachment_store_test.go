package model

import (
	"testing"

	"codex_go/attachmentstore"
	"codex_go/eventmap"
)

// recordingModelImageStore records every upload the image-preparation pipeline
// issues and answers with a configurable result.
type recordingModelImageStore struct {
	requests []attachmentstore.UploadRequest
	result   *attachmentstore.UploadResult
}

func (s *recordingModelImageStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	s.requests = append(s.requests, request)
	if s.result == nil {
		return attachmentstore.InlineUploadResult(request.Data), nil
	}
	return *s.result, nil
}

func (s *recordingModelImageStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "not found")
}

const attachmentStoreTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func attachmentStoreTestItems() []any {
	return []any{map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type":      "input_image",
			"image_url": "data:image/png;base64," + attachmentStoreTestPNG,
			"detail":    "high",
		}},
	}}
}

// TestPrepareResponseInputImagesUploadsWithPersistenceIntentLikeRust covers Rust
// #51517 for the request-time preparation path: a persistent thread's prepared
// images reach the image store with `ephemeral: false` next to the thread id.
func TestPrepareResponseInputImagesUploadsWithPersistenceIntentLikeRust(t *testing.T) {
	store := &recordingModelImageStore{}
	out := prepareResponseInputImagesWithStore(
		attachmentStoreTestItems(),
		store,
		eventmap.ImagePrepOrigin{ThreadID: "thread-1"},
	)
	if len(store.requests) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.requests))
	}
	if got := store.requests[0].ThreadID; got != "thread-1" {
		t.Fatalf("upload thread id = %q, want thread-1", got)
	}
	if store.requests[0].Ephemeral {
		t.Fatal("persistent request uploaded with ephemeral: true")
	}
	block := out[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if _, ok := block["image_url"].(string); !ok {
		t.Fatalf("inline store did not keep the image inline: %#v", block)
	}
}

// TestPrepareResponseInputImagesCarriesEphemeralIntentLikeRust covers the
// ephemeral half of Rust #51517: an ephemeral thread's uploads are marked so the
// store can skip durable persistence.
func TestPrepareResponseInputImagesCarriesEphemeralIntentLikeRust(t *testing.T) {
	store := &recordingModelImageStore{}
	prepareResponseInputImagesWithStore(
		attachmentStoreTestItems(),
		store,
		eventmap.ImagePrepOrigin{ThreadID: "ephemeral-thread", Ephemeral: true},
	)
	if len(store.requests) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.requests))
	}
	if !store.requests[0].Ephemeral {
		t.Fatal("ephemeral thread uploaded without the persistence intent")
	}
	if got := store.requests[0].ThreadID; got != "ephemeral-thread" {
		t.Fatalf("upload thread id = %q", got)
	}
}

// TestPrepareResponseInputImagesFileReferenceReplacesInlineURLLikeRust covers
// Rust's `UploadResult::File` branch on the request path: the block keeps the
// store's file id instead of the inline data URL.
func TestPrepareResponseInputImagesFileReferenceReplacesInlineURLLikeRust(t *testing.T) {
	fileResult := attachmentstore.FileUploadResult("file_request")
	store := &recordingModelImageStore{result: &fileResult}
	out := prepareResponseInputImagesWithStore(
		attachmentStoreTestItems(),
		store,
		eventmap.ImagePrepOrigin{ThreadID: "thread-1"},
	)
	block := out[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if block["file_id"] != "file_request" {
		t.Fatalf("file_id = %#v, want file_request", block["file_id"])
	}
	if _, ok := block["image_url"]; ok {
		t.Fatalf("inline url was kept next to the file reference: %#v", block)
	}
	if _, ok := block["detail"]; ok {
		t.Fatalf("detail was kept next to the file reference: %#v", block)
	}
}

// TestAgentRequestEphemeralReachesUploadsLikeRust drives the runner-level
// plumbing: the request's `Ephemeral` flag (Rust
// `turn_context.config.ephemeral`) is what the origin carries into uploads.
func TestAgentRequestEphemeralReachesUploadsLikeRust(t *testing.T) {
	store := &recordingModelImageStore{}
	request := &AgentRequest{
		ThreadID:   "thread-1",
		Ephemeral:  true,
		InputItems: attachmentStoreTestItems(),
	}
	responsesInputItemsWithStore(request, store)
	if len(store.requests) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.requests))
	}
	if !store.requests[0].Ephemeral {
		t.Fatal("AgentRequest.Ephemeral did not reach the upload request")
	}
	if got := store.requests[0].ThreadID; got != "thread-1" {
		t.Fatalf("upload thread id = %q, want thread-1", got)
	}
}
