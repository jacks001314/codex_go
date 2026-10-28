package model

import (
	"sync"
	"testing"
	"time"

	"codex_go/attachmentstore"
)

type resolvableImageStore struct {
	mu      sync.Mutex
	missing map[string]bool
	calls   []string
}

func (s *resolvableImageStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	return attachmentstore.InlineUploadResult(request.Data), nil
}

func (s *resolvableImageStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "missing")
}

func (s *resolvableImageStore) IsResolvable(fileID string) bool {
	s.mu.Lock()
	s.calls = append(s.calls, fileID)
	s.mu.Unlock()
	return !s.missing[fileID]
}

func (s *resolvableImageStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func unavailableImageRequest() *AgentRequest {
	return &AgentRequest{
		Store: true,
		InputItems: []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "look"},
					map[string]any{"type": "input_image", "file_id": "file-missing"},
					map[string]any{"type": "input_image", "file_id": "file-present", "detail": "high"},
				},
				"internal_chat_message_metadata_passthrough": map[string]any{
					"content_item_kinds": []any{"user.text", "user.image", "user.image"},
				},
			},
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call-1",
				"output": []any{
					map[string]any{"type": "input_image", "file_id": "file-missing"},
				},
			},
		},
	}
}

func TestResponsesInputReplacesUnavailableFileImagesLikeRust(t *testing.T) {
	store := &resolvableImageStore{missing: map[string]bool{"file-missing": true}}
	items := responsesInputItemsWithStore(unavailableImageRequest(), store)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	message, _ := items[0].(map[string]any)
	content, _ := message["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("content = %#v", content)
	}
	placeholder, _ := content[1].(map[string]any)
	if placeholder["type"] != "input_text" || placeholder["text"] != UnavailableImagePlaceholder {
		t.Fatalf("placeholder = %#v", content[1])
	}
	kept, _ := content[2].(map[string]any)
	if kept["type"] != "input_image" || kept["file_id"] != "file-present" || kept["detail"] != "high" {
		t.Fatalf("available image changed: %#v", content[2])
	}
	metadata, _ := message["internal_chat_message_metadata_passthrough"].(map[string]any)
	kinds, _ := metadata["content_item_kinds"].([]any)
	if len(kinds) != 3 || kinds[1] != "images.unavailable" || kinds[0] != "user.text" || kinds[2] != "user.image" {
		t.Fatalf("content kinds = %#v", kinds)
	}

	outputItem, _ := items[1].(map[string]any)
	output, _ := outputItem["output"].([]any)
	outputPlaceholder, _ := output[0].(map[string]any)
	if outputPlaceholder["type"] != "input_text" || outputPlaceholder["text"] != UnavailableImagePlaceholder {
		t.Fatalf("tool output image = %#v", output)
	}
}

func TestResponsesInputRetainsImagesWhenStoreCannotConfirmLikeRust(t *testing.T) {
	items := responsesInputItemsWithStore(unavailableImageRequest(), attachmentstore.InlineStore{})
	message, _ := items[0].(map[string]any)
	content, _ := message["content"].([]any)
	first, _ := content[1].(map[string]any)
	if first["type"] != "input_image" || first["file_id"] != "file-missing" {
		t.Fatalf("inline store changed image: %#v", content[1])
	}
}

func TestResponsesInputChecksDistinctFileIDsOnceLikeRust(t *testing.T) {
	request := unavailableImageRequest()
	request.InputItems = append(request.InputItems, map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_image", "file_id": "file-missing"},
		},
	})
	store := &resolvableImageStore{missing: map[string]bool{}}
	responsesInputItemsWithStore(request, store)
	if store.callCount() != 2 {
		t.Fatalf("IsResolvable calls = %d (%v), want one per distinct file id", store.callCount(), store.calls)
	}
}

type concurrentImageStore struct {
	entered chan string
	release chan struct{}
}

func (s *concurrentImageStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	return attachmentstore.InlineUploadResult(request.Data), nil
}

func (s *concurrentImageStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "missing")
}

func (s *concurrentImageStore) IsResolvable(fileID string) bool {
	s.entered <- fileID
	<-s.release
	return true
}

func TestResponsesInputChecksFileIDsConcurrentlyLikeRust(t *testing.T) {
	const distinct = 4
	store := &concurrentImageStore{entered: make(chan string, distinct), release: make(chan struct{})}
	request := &AgentRequest{Store: true}
	for index := 0; index < distinct; index++ {
		request.InputItems = append(request.InputItems, map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_image", "file_id": "file-" + string(rune('a'+index))},
			},
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		responsesInputItemsWithStore(request, store)
	}()
	for index := 0; index < distinct; index++ {
		select {
		case <-store.entered:
		case <-time.After(5 * time.Second):
			close(store.release)
			t.Fatalf("only %d checks were in flight; file ids are checked serially", index)
		}
	}
	close(store.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("responses input preparation did not finish")
	}
}
