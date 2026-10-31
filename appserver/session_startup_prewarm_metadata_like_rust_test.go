package appserver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// prewarmMetadataAgent records the warmup requests the router issues.
type prewarmMetadataAgent struct {
	*prewarmingRuntimeAgent
	mu       sync.Mutex
	requests []*model.AgentRequest
}

func (a *prewarmMetadataAgent) Prewarm(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	return a.prewarmingRuntimeAgent.Prewarm(ctx, request)
}

// startupRequest returns the newest session-startup warmup; the router also
// issues guardian review prewarms through the same agent.
func (a *prewarmMetadataAgent) startupRequest() *model.AgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := len(a.requests) - 1; index >= 0; index-- {
		if request := a.requests[index]; request != nil && request.Originator == "session_startup" {
			return request
		}
	}
	return nil
}

// Rust #47709: the session-startup warmup handshake carries the startup turn's
// metadata (window, session, model and `request_kind: prewarm`), so the socket
// is prepared for the window the first turn will use.
func TestSessionStartupPrewarmCarriesTurnMetadataLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	agent := &prewarmMetadataAgent{prewarmingRuntimeAgent: &prewarmingRuntimeAgent{
		recordingRuntimeAgent: newRecordingRuntimeAgent("ok"),
		prewarmedID:           "prewarm-resp-metadata",
	}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(NewNotificationBuffer())
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	started, ok := threadStart.Result.(*ThreadStartResponse)
	if !ok || started == nil || started.Thread == nil {
		t.Fatalf("thread start result = %#v", threadStart.Result)
	}
	threadID := started.Thread.ID

	deadline := time.Now().Add(5 * time.Second)
	for agent.startupRequest() == nil {
		if time.Now().After(deadline) {
			t.Fatal("startup prewarm request was not issued")
		}
		time.Sleep(5 * time.Millisecond)
	}
	request := agent.startupRequest()
	if request.ClientMetadata == nil {
		t.Fatalf("prewarm client metadata = nil, want the turn metadata headers")
	}
	raw := request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]
	if raw == "" {
		t.Fatalf("prewarm client metadata = %#v, want %q", request.ClientMetadata, codexapi.ClientCodexTurnMetadataHeader)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("prewarm turn metadata is not JSON: %v", err)
	}
	if got, _ := document[codexapi.RequestKindKey].(string); got != string(codexapi.ClientRequestPrewarm) {
		t.Fatalf("request_kind = %q, want %q", got, codexapi.ClientRequestPrewarm)
	}
	if got, _ := document[codexapi.SessionIDKey].(string); got != threadID {
		t.Fatalf("session_id = %q, want %q", got, threadID)
	}
	wantWindowID := turn.ConversationWindowID(threadID, 0)
	if got, _ := document[codexapi.WindowIDKey].(string); got != wantWindowID {
		t.Fatalf("window_id = %q, want %q", got, wantWindowID)
	}
	if got, _ := document[codexapi.ModelKey].(string); got != "gpt-test" {
		t.Fatalf("model = %q, want gpt-test", got)
	}
	if got, _ := document[codexapi.ThreadIDKey].(string); got != threadID {
		t.Fatalf("thread_id = %q, want %q", got, threadID)
	}
}
