package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"codex_go/attachmentstore"
	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

// recordingTurnAttachmentStore records the uploads a turn's image preparation
// issues and answers with a configurable result. Rust #45556 routes every
// upload through the store the thread manager hands to the session, so this
// store is the observable end of that plumbing.
type recordingTurnAttachmentStore struct {
	mu       sync.Mutex
	requests []attachmentstore.UploadRequest
	result   attachmentstore.UploadResult
}

func (s *recordingTurnAttachmentStore) Upload(request attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	if s.result.FileID != "" {
		return s.result, nil
	}
	return attachmentstore.InlineUploadResult(request.Data), nil
}

func (s *recordingTurnAttachmentStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "not found")
}

func (s *recordingTurnAttachmentStore) uploads() []attachmentstore.UploadRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]attachmentstore.UploadRequest(nil), s.requests...)
}

const turnAttachmentTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func turnAttachmentTestItems() []any {
	return []any{map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type":      "input_image",
			"image_url": "data:image/png;base64," + turnAttachmentTestPNG,
			"detail":    "high",
		}},
	}}
}

// attachmentStoreRuntimeRouter builds a router whose thread manager carries a
// recording attachment store, mirroring a host that installs a durable backend
// on `ThreadManager::image_store` (Rust #45556).
func attachmentStoreRuntimeRouter(t *testing.T, handler http.HandlerFunc) (*RuntimeRouter, *recordingTurnAttachmentStore) {
	t.Helper()
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-attachment")); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	configBody := "model = \"gpt-attachment\"\nmodel_provider = \"openai\"\nopenai_base_url = \"" + server.URL + "/v1\"\n\n[features]\napps = false\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("Write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:     config.NewConfigService(home),
		HTTPClient: server.Client(),
	})
	store := &recordingTurnAttachmentStore{result: attachmentstore.FileUploadResult("file_from_thread_manager")}
	router.threads.SetAttachmentStore(store)
	return router, store
}

// TestRuntimeRouterTurnUsesThreadManagerAttachmentStoreLikeRust covers the
// session-spawn half of Rust #45556: `SessionSpawnArgs::image_store` is the
// thread manager's configured store, not the inline default.
func TestRuntimeRouterTurnUsesThreadManagerAttachmentStoreLikeRust(t *testing.T) {
	router, store := attachmentStoreRuntimeRouter(t, func(http.ResponseWriter, *http.Request) {})

	agent, err := router.responsesAgentForTurn(&turn.TurnStartParams{ThreadID: "thread-parent", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("responsesAgentForTurn error = %v", err)
	}
	if agent == nil {
		t.Fatal("responsesAgentForTurn returned no agent")
	}
	if agent.ImageStore != store {
		t.Fatalf("turn image store = %T, want the thread manager store", agent.ImageStore)
	}
}

// TestDelegatedSubagentTurnInheritsThreadManagerAttachmentStoreLikeRust covers
// Rust's delegated-session inheritance: `run_codex_thread_interactive` clones
// the parent's `image_store`, and Go's delegated subagent turns run on the same
// router, so they must resolve the same store for a different thread.
func TestDelegatedSubagentTurnInheritsThreadManagerAttachmentStoreLikeRust(t *testing.T) {
	router, store := attachmentStoreRuntimeRouter(t, func(http.ResponseWriter, *http.Request) {})

	parent, err := router.responsesAgentForTurn(&turn.TurnStartParams{ThreadID: "thread-parent", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("parent responsesAgentForTurn error = %v", err)
	}
	child, err := router.responsesAgentForTurn(&turn.TurnStartParams{
		ThreadID:    "thread-child",
		TurnTrigger: "subagent",
		CWD:         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("child responsesAgentForTurn error = %v", err)
	}
	if child.ImageStore != parent.ImageStore || child.ImageStore != store {
		t.Fatalf("delegated image store = %T, want the parent's thread manager store", child.ImageStore)
	}
}

// TestRuntimeRouterTurnUploadsPreparedImagesThroughThreadManagerStoreLikeRust
// drives a real turn: the prepared image reaches the thread manager's store and
// the store's file id replaces the inline data URL in the outbound request.
func TestRuntimeRouterTurnUploadsPreparedImagesThroughThreadManagerStoreLikeRust(t *testing.T) {
	var body map[string]any
	router, store := attachmentStoreRuntimeRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if rejectRuntimeRouterWebsocketUpgrade(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Decode request body error = %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(modelResponsesSSE(
			`{"type":"response.created","response":{"id":"resp-attachment"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-1","delta":"ok"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-attachment","model":"gpt-attachment","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)))
	})

	agent, err := router.responsesAgentForTurn(&turn.TurnStartParams{ThreadID: "thread-parent", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("responsesAgentForTurn error = %v", err)
	}
	agent.Stream = true
	if _, err := agent.Run(context.Background(), &model.AgentRequest{
		ThreadID:   "thread-parent",
		Model:      "gpt-attachment",
		Prompt:     "describe",
		InputItems: turnAttachmentTestItems(),
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	uploads := store.uploads()
	// The runner prepares images once per transport attempt (WebSocket, then
	// the HTTP fallback), so the count is not the contract; every upload must
	// reach the thread manager's store with this turn's thread id.
	if len(uploads) == 0 {
		t.Fatal("the configured attachment store received no upload")
	}
	for _, upload := range uploads {
		if got := upload.ThreadID; got != "thread-parent" {
			t.Fatalf("upload thread id = %q, want thread-parent", got)
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal request body error = %v", err)
	}
	if !strings.Contains(string(encoded), "file_from_thread_manager") {
		t.Fatalf("outbound request missing the store file id: %s", encoded)
	}
	if strings.Contains(string(encoded), turnAttachmentTestPNG) {
		t.Fatalf("inline base64 image leaked into the outbound request: %s", encoded)
	}
}
