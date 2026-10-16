package turn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/attachmentstore"
	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
)

// imageGenerationRecordingStore records the uploads the image-generation handler
// performs, so the test can assert the policy tags and bytes Rust #54398 sends.
type imageGenerationRecordingStore struct {
	uploads []attachmentstore.UploadRequest
	result  attachmentstore.UploadResult
	err     error
}

func (s *imageGenerationRecordingStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	s.uploads = append(s.uploads, request)
	if s.err != nil {
		return attachmentstore.UploadResult{}, s.err
	}
	return s.result, nil
}

func (s *imageGenerationRecordingStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "not found")
}

// TestImageGenerationStoresFileReferenceLikeRust ports the write side of Rust
// #54398 (ext/image-generation/src/tool.rs): a completed generation is uploaded
// through the attachment store tagged `usage=image_gen`, the item keeps the
// durable `fileId` returned by a file-backed store, and every failure mode
// (undecodable image data, upload error, inline-only store) falls back to the
// inline base64 `result` instead of dropping the generation.
func TestImageGenerationStoresFileReferenceLikeRust(t *testing.T) {
	generated := []byte("\x89PNG\r\n\x1a\ngenerated-image")
	payload := base64.StdEncoding.EncodeToString(generated)

	handlerFor := func(t *testing.T, store attachmentstore.Store, b64 string) *ImageGenerationHandler {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(codexapi.ImageResponse{Data: []codexapi.ImageData{{B64JSON: b64}}})
		}))
		t.Cleanup(server.Close)
		return NewImageGenerationHandler(&ImageGenerationOptions{
			SessionID:       "thread-1",
			Provider:        model.APIProvider{BaseURL: server.URL + "/v1"},
			HTTPClient:      server.Client(),
			AttachmentStore: store,
		})
	}
	invocation := func() *tool.Invocation {
		return &tool.Invocation{
			CallID:   "call-image-store",
			ToolName: tool.NamespacedName(ImageGenerationNamespace, ImageGenerationToolName),
			Payload: tool.Payload{
				Kind:      tool.PayloadFunction,
				Arguments: `{"prompt":"draw a square"}`,
			},
			Context: map[string]any{"turnId": "turn-image-store"},
		}
	}

	// File-backed store: the item carries the durable reference.
	store := &imageGenerationRecordingStore{result: attachmentstore.FileUploadResult("file_generated")}
	output, err := handlerFor(t, store, payload).Execute(context.Background(), invocation())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	if len(store.uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.uploads))
	}
	if got := store.uploads[0].Tags[attachmentstore.TagUsageKey]; got != attachmentstore.TagUsageImageGen {
		t.Fatalf("upload tags = %#v, want usage=image_gen", store.uploads[0].Tags)
	}
	if store.uploads[0].ThreadID != "thread-1" {
		t.Fatalf("upload thread = %q, want thread-1", store.uploads[0].ThreadID)
	}
	if string(store.uploads[0].Data) != string(generated) {
		t.Fatalf("upload bytes = %q, want the decoded generated image", store.uploads[0].Data)
	}
	if got := output.Data["fileId"]; got != "file_generated" {
		t.Fatalf("fileId = %#v, want file_generated", got)
	}
	if got := output.Data["file_id"]; got != "file_generated" {
		t.Fatalf("file_id = %#v, want file_generated", got)
	}
	// The inline copy stays so the in-process readers keep working; the app-server
	// wire form prefers fileId.
	if got := output.Data["result"]; got != payload {
		t.Fatalf("result = %#v, want the inline base64 payload", got)
	}

	// Inline-only store (nil => attachmentstore.InlineStore): no file reference.
	inlineOutput, err := handlerFor(t, nil, payload).Execute(context.Background(), invocation())
	if err != nil {
		t.Fatalf("Execute(inline) error = %v", err)
	}
	if _, ok := inlineOutput.Data["fileId"]; ok {
		t.Fatalf("inline store produced a fileId: %#v", inlineOutput.Data)
	}
	if got := inlineOutput.Data["result"]; got != payload {
		t.Fatalf("inline result = %#v, want the inline base64 payload", got)
	}

	// Upload failure keeps the inline result instead of failing the generation.
	failing := &imageGenerationRecordingStore{err: attachmentstore.NewStoreError(attachmentstore.ErrorKindBackend, "attachment backend unavailable")}
	failedOutput, err := handlerFor(t, failing, payload).Execute(context.Background(), invocation())
	if err != nil {
		t.Fatalf("Execute(upload error) error = %v", err)
	}
	if !failedOutput.Success {
		t.Fatalf("upload failure must not fail the generation: %#v", failedOutput)
	}
	if _, ok := failedOutput.Data["fileId"]; ok {
		t.Fatalf("failed upload produced a fileId: %#v", failedOutput.Data)
	}
	if got := failedOutput.Data["result"]; got != payload {
		t.Fatalf("failed upload result = %#v, want the inline base64 payload", got)
	}

	// Undecodable image data is never uploaded.
	undecodable := &imageGenerationRecordingStore{result: attachmentstore.FileUploadResult("file_never")}
	undecodableOutput, err := handlerFor(t, undecodable, "!!!not-base64!!!").Execute(context.Background(), invocation())
	if err != nil {
		t.Fatalf("Execute(undecodable) error = %v", err)
	}
	if len(undecodable.uploads) != 0 {
		t.Fatalf("undecodable image was uploaded: %#v", undecodable.uploads)
	}
	if _, ok := undecodableOutput.Data["fileId"]; ok {
		t.Fatalf("undecodable image produced a fileId: %#v", undecodableOutput.Data)
	}
	if got := undecodableOutput.Data["result"]; got != "!!!not-base64!!!" {
		t.Fatalf("undecodable result = %#v, want the original payload", got)
	}
}
