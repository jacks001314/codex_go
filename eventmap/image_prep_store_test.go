package eventmap

import (
	"testing"

	"codex_go/attachmentstore"
)

// failingImagePrepStore mirrors Rust image_preparation_tests.rs
// `FailingAttachmentStore`: every upload fails.
type failingImagePrepStore struct {
	kind attachmentstore.ErrorKind
}

func (s failingImagePrepStore) Upload(attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	return attachmentstore.UploadResult{}, attachmentstore.NewStoreError(s.kind, "attachment backend unavailable")
}

func (s failingImagePrepStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(s.kind, "attachment backend unavailable")
}

// recordingImagePrepStore records every upload request the preparation pipeline
// issues and answers with a configurable result.
type recordingImagePrepStore struct {
	requests []attachmentstore.UploadRequest
	// result overrides the answer; when nil the store echoes the request bytes
	// like attachmentstore.InlineStore.
	result *attachmentstore.UploadResult
	err    error
}

func (s *recordingImagePrepStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return attachmentstore.UploadResult{}, s.err
	}
	if s.result == nil {
		return attachmentstore.InlineUploadResult(request.Data), nil
	}
	return *s.result, nil
}

func (s *recordingImagePrepStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "not found")
}

// TestUploadFailureKeepsResizedImageInlineLikeRust ports Rust
// `upload_failure_keeps_resized_image_inline`: a store failure must not drop the
// prepared image; it stays inline with the resized dimensions.
func TestUploadFailureKeepsResizedImageInlineLikeRust(t *testing.T) {
	items := []ImagePrepContentItem{{
		Kind:     ImagePrepContentImage,
		ImageURL: pngDataURL(2048, 2048),
		Detail:   ImagePrepDetailHigh,
	}}
	origin := ImagePrepOrigin{ThreadID: "image-preparation-thread"}
	store := failingImagePrepStore{kind: attachmentstore.ErrorKindBackend}

	prepared, resized, err := PrepareImagePrepContentWithNoticesAndStore(store, origin, items)
	if err != nil {
		t.Fatalf("PrepareImagePrepContentWithNoticesAndStore: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Kind != ImagePrepContentImage {
		t.Fatalf("expected one inline image, got %#v", prepared)
	}
	if prepared[0].FileID != "" {
		t.Fatalf("failed upload produced a file reference: %#v", prepared[0])
	}
	if len(resized) != 1 || resized[0].PreparedWidth != 1600 || resized[0].PreparedHeight != 1600 {
		t.Fatalf("resize = %#v, want 1600x1600", resized)
	}
	_, _, width, height, err := decodeImagePrepBytes(mustBase64Payload(t, prepared[0].ImageURL))
	if err != nil {
		t.Fatalf("decode prepared image: %v", err)
	}
	if width != 1600 || height != 1600 {
		t.Fatalf("prepared dimensions = %dx%d, want 1600x1600", width, height)
	}
}

// TestPrepareImagePrepUploadsThreadPersistenceIntentLikeRust freezes the Rust
// #51517 contract for the Go pipeline: every prepared image reaches the store
// as an upload request carrying the receiving thread id and its persistence
// intent, and an inline answer keeps the prepared bytes inline.
func TestPrepareImagePrepUploadsThreadPersistenceIntentLikeRust(t *testing.T) {
	store := &recordingImagePrepStore{}
	items := []ImagePrepContentItem{{
		Kind:     ImagePrepContentImage,
		ImageURL: pngDataURL(64, 32),
		Detail:   ImagePrepDetailHigh,
	}}
	origin := ImagePrepOrigin{ThreadID: "image-preparation-thread", Ephemeral: true}

	prepared, _, err := PrepareImagePrepContentWithNoticesAndStore(store, origin, items)
	if err != nil {
		t.Fatalf("PrepareImagePrepContentWithNoticesAndStore: %v", err)
	}
	if len(store.requests) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.requests))
	}
	request := store.requests[0]
	if request.ThreadID != "image-preparation-thread" {
		t.Fatalf("upload thread id = %q", request.ThreadID)
	}
	if !request.Ephemeral {
		t.Fatal("upload request did not carry the ephemeral persistence intent")
	}
	// The inline answer echoes the request bytes, so the item keeps them.
	if prepared[0].FileID != "" || prepared[0].ImageURL == "" {
		t.Fatalf("inline answer changed the reference: %#v", prepared[0])
	}
	if got := mustBase64Payload(t, prepared[0].ImageURL); string(got) != string(request.Data) {
		t.Fatalf("inline bytes = %d bytes, want the uploaded %d bytes", len(got), len(request.Data))
	}
}

// TestPrepareImagePrepFileReferenceReplacesInlineURLLikeRust covers Rust's
// `UploadResult::File` branch: the store's file id replaces the inline data URL
// on the content item.
func TestPrepareImagePrepFileReferenceReplacesInlineURLLikeRust(t *testing.T) {
	fileResult := attachmentstore.FileUploadResult("file_prepared")
	store := &recordingImagePrepStore{result: &fileResult}
	items := []ImagePrepContentItem{{
		Kind:     ImagePrepContentImage,
		ImageURL: pngDataURL(64, 32),
		Detail:   ImagePrepDetailHigh,
	}}

	prepared, _, err := PrepareImagePrepContentWithNoticesAndStore(store, ImagePrepOrigin{ThreadID: "thread-1"}, items)
	if err != nil {
		t.Fatalf("PrepareImagePrepContentWithNoticesAndStore: %v", err)
	}
	if prepared[0].FileID != "file_prepared" {
		t.Fatalf("file id = %q", prepared[0].FileID)
	}
	if prepared[0].ImageURL != "" {
		t.Fatalf("inline url was kept next to the file reference: %q", prepared[0].ImageURL)
	}
	if got, want := prepared[0].FileID, "file_prepared"; got != want {
		t.Fatalf("file id = %q, want %q", got, want)
	}
}

// TestPrepareImagePrepSkipsFileBackedImagesLikeRust covers Rust
// `resize_notices_count_file_backed_images_and_skip_failed_images`: images that
// are already file references keep their position and are never re-uploaded.
func TestPrepareImagePrepSkipsFileBackedImagesLikeRust(t *testing.T) {
	store := &recordingImagePrepStore{}
	items := []ImagePrepContentItem{
		{Kind: ImagePrepContentImage, FileID: "file_message", Detail: ImagePrepDetailHigh},
		{Kind: ImagePrepContentImage, ImageURL: pngDataURL(64, 32), Detail: ImagePrepDetailHigh},
	}

	prepared, _, err := PrepareImagePrepContentWithNoticesAndStore(store, ImagePrepOrigin{ThreadID: "thread-1"}, items)
	if err != nil {
		t.Fatalf("PrepareImagePrepContentWithNoticesAndStore: %v", err)
	}
	if len(store.requests) != 1 {
		t.Fatalf("uploads = %d, want 1 (the file-backed image is skipped)", len(store.requests))
	}
	if prepared[0].FileID != "file_message" || prepared[0].ImageURL != "" {
		t.Fatalf("file-backed image changed: %#v", prepared[0])
	}
}
