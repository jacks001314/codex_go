package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
)

type threadSampleRecordingAgent struct {
	mu       sync.Mutex
	request  *model.AgentRequest
	response *model.AgentResponse
	err      error
}

type threadSampleBlockingAgent struct {
	started chan struct{}
}

func (a *threadSampleBlockingAgent) Run(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
	if a.started != nil {
		close(a.started)
		a.started = nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (a *threadSampleRecordingAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	copied := *request
	a.request = &copied
	a.mu.Unlock()
	return a.response, a.err
}

func TestThreadSampleInheritsHistoryAndDoesNotMutateSource(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	record := &session.Record{
		ID: "thread-sample-source",
		Metadata: session.Metadata{
			CWD:   home,
			Model: "gpt-test",
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-latest", Status: "completed"},
			},
		},
		Items: []session.Item{
			{Role: "user", Text: "earlier question"},
			{Role: "assistant", Text: "earlier answer"},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	agent := &threadSampleRecordingAgent{response: &model.AgentResponse{
		ResponseID: "resp-sample",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "sample answer"}},
		Usage:      model.AgentUsage{InputTokens: 3, CachedInputTokens: 1, OutputTokens: 2},
	}}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Agent: agent, DefaultCWD: home})
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatal(err)
	}
	router.experimentalAPI = map[string]bool{"conn-1": true}
	initializeThreadSampleConnection(t, router, "conn-1")
	before, err := store.Load("thread-sample-source")
	if err != nil {
		t.Fatal(err)
	}
	router.experimentalAPI = map[string]bool{"conn-1": true}

	response := router.Handle(&Request{
		JSONRPC:      "2.0",
		ID:           IntID(1),
		Method:       MethodThreadSample,
		ConnectionID: "conn-1",
		Params:       mustJSON(t, ThreadSampleParams{ThreadID: "thread-sample-source", ExpectedTurnID: "turn-latest", Prompt: "next question"}),
	})
	if response.Error != nil {
		t.Fatalf("thread/sample error = %+v", response.Error)
	}
	result := response.Result.(*ThreadSampleResponse)
	if result.SourceTurnID != "turn-latest" || result.ResponseID != "resp-sample" || result.Text == nil || *result.Text != "sample answer" {
		t.Fatalf("result = %+v", result)
	}
	agent.mu.Lock()
	request := agent.request
	agent.mu.Unlock()
	if request == nil || request.Prompt != "next question" || len(request.InputItems) < 2 {
		t.Fatalf("agent request = %#v", request)
	}
	after, err := store.Load("thread-sample-source")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != len(before.Items) || after.Metadata.LastResponseID != before.Metadata.LastResponseID {
		t.Fatalf("source history changed: before=%d after=%d", len(before.Items), len(after.Items))
	}
}

func TestThreadSampleReturnsNullTextForToolCalls(t *testing.T) {
	text, err := threadSampleResponseText(&model.AgentResponse{
		Items: []model.AgentItem{
			{Type: "agent_message", Text: "partial"},
			{Type: "function_call", Name: "shell"},
		},
	})
	if err != nil || text != nil {
		t.Fatalf("text=%v err=%v", text, err)
	}
	commentary, err := threadSampleResponseText(&model.AgentResponse{
		Items: []model.AgentItem{{Type: "agent_message", Text: "working", Data: map[string]any{"phase": "commentary"}}},
	})
	if err != nil || commentary != nil {
		t.Fatalf("commentary text=%v err=%v", commentary, err)
	}
	final := "done"
	if got, _ := threadSampleResponseText(&model.AgentResponse{Items: []model.AgentItem{{Type: "agent_message", Text: final}}}); got == nil || *got != final {
		t.Fatalf("final text = %v", got)
	}
}

func TestThreadSampleRejectsPromptAndStaleTurnLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	record := &session.Record{ID: "thread-sample-invalid", Metadata: session.Metadata{CWD: home, RolloutTurns: []session.TurnSnapshot{{ID: "turn-latest", Status: "completed"}}}}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	agent := &threadSampleRecordingAgent{response: &model.AgentResponse{ResponseID: "unused"}}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Agent: agent, DefaultCWD: home})
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatal(err)
	}
	router.experimentalAPI = map[string]bool{"conn-1": true}
	initializeThreadSampleConnection(t, router, "conn-1")
	for name, params := range map[string]ThreadSampleParams{
		"empty prompt": {ThreadID: string(record.ID), ExpectedTurnID: "turn-latest", Prompt: "   "},
		"oversized":    {ThreadID: string(record.ID), ExpectedTurnID: "turn-latest", Prompt: strings.Repeat("x", threadSampleMaxPrompt+1)},
		"stale turn":   {ThreadID: string(record.ID), ExpectedTurnID: "turn-old", Prompt: "ok"},
	} {
		response := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodThreadSample, ConnectionID: "conn-1", Params: mustJSON(t, params)})
		if response.Error == nil {
			t.Fatalf("%s: expected invalid request", name)
		}
	}
}

func TestThreadSampleCancelStopsMatchingRequestOnly(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	record := &session.Record{ID: "thread-sample-cancel", Metadata: session.Metadata{CWD: home, RolloutTurns: []session.TurnSnapshot{{ID: "turn-latest", Status: "completed"}}}}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	agent := &threadSampleBlockingAgent{started: make(chan struct{})}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Agent: agent, DefaultCWD: home})
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatal(err)
	}
	initializeThreadSampleConnection(t, router, "conn-1")
	initializeThreadSampleConnection(t, router, "conn-2")

	sampleDone := make(chan *Response, 1)
	go func() {
		sampleDone <- router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSample, ConnectionID: "conn-1",
			Params: mustJSON(t, ThreadSampleParams{ThreadID: string(record.ID), ExpectedTurnID: "turn-latest", Prompt: "sample"}),
		})
	}()
	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sample did not start")
	}
	wrongConnection := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(8), Method: MethodThreadSampleCancel, ConnectionID: "conn-2",
		Params: mustJSON(t, ThreadSampleCancelParams{RequestID: IntID(7)}),
	})
	if wrongConnection.Error != nil {
		t.Fatalf("wrong-connection cancel error = %+v", wrongConnection.Error)
	}
	canceled := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(9), Method: MethodThreadSampleCancel, ConnectionID: "conn-1",
		Params: mustJSON(t, ThreadSampleCancelParams{RequestID: IntID(7)}),
	})
	if canceled.Error != nil || canceled.Result == nil {
		t.Fatalf("cancel response = %+v", canceled)
	}
	repeated := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(10), Method: MethodThreadSampleCancel, ConnectionID: "conn-1",
		Params: mustJSON(t, ThreadSampleCancelParams{RequestID: IntID(7)}),
	})
	if repeated.Error != nil {
		t.Fatalf("repeated cancel error = %+v", repeated.Error)
	}
	select {
	case result := <-sampleDone:
		if result.Error == nil || !strings.Contains(result.Error.Message, "sample canceled") {
			t.Fatalf("sample response = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled sample did not return")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func initializeThreadSampleConnection(t *testing.T, router *RuntimeRouter, connectionID string) {
	t.Helper()
	response := router.Handle(&Request{
		JSONRPC:      "2.0",
		ID:           IntID(0),
		Method:       MethodInitialize,
		ConnectionID: connectionID,
		Params: mustJSON(t, InitializeParams{
			ClientInfo:   ClientInfo{Name: "thread-sample-test", Version: "1.0"},
			Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
		}),
	})
	if response.Error != nil {
		t.Fatalf("initialize error = %+v", response.Error)
	}
}
