package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/session"
)

func threadSampleOverrideRouter(t *testing.T, agent *threadSampleRecordingAgent) (*RuntimeRouter, *session.Store, *session.Record) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	record := &session.Record{
		ID: "thread-sample-model",
		Metadata: session.Metadata{
			CWD:          home,
			Model:        "gpt-source",
			RolloutTurns: []session.TurnSnapshot{{ID: "turn-latest", Status: "completed"}},
		},
		Items: []session.Item{
			{Role: "user", Text: "earlier question"},
			{Role: "assistant", Text: "earlier answer"},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Agent: agent, DefaultCWD: home})
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatal(err)
	}
	initializeThreadSampleConnection(t, router, "conn-1")
	return router, store, record
}

func threadSampleLastModel(t *testing.T, agent *threadSampleRecordingAgent) string {
	t.Helper()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.request == nil {
		t.Fatal("agent request = nil")
	}
	return agent.request.Model
}

// Mirrors Rust's per-sample model override (#62419): an explicit model applies to
// that sample only, and omitting it inherits the source thread's model.
func TestThreadSampleModelOverrideLikeRust(t *testing.T) {
	agent := &threadSampleRecordingAgent{response: &model.AgentResponse{
		ResponseID: "resp-sample",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "answer"}},
	}}
	router, store, record := threadSampleOverrideRouter(t, agent)

	override := "gpt-sample"
	response := router.Handle(&Request{
		JSONRPC:      "2.0",
		ID:           IntID(1),
		Method:       MethodThreadSample,
		ConnectionID: "conn-1",
		Params: mustJSON(t, ThreadSampleParams{
			ThreadID:       string(record.ID),
			ExpectedTurnID: "turn-latest",
			Prompt:         "next question",
			Model:          &override,
		}),
	})
	if response.Error != nil {
		t.Fatalf("thread/sample error = %+v", response.Error)
	}
	if got := threadSampleLastModel(t, agent); got != override {
		t.Fatalf("overridden sample model = %q, want %q", got, override)
	}

	// A follow-up sample without the field inherits the source model again.
	response = router.Handle(&Request{
		JSONRPC:      "2.0",
		ID:           IntID(2),
		Method:       MethodThreadSample,
		ConnectionID: "conn-1",
		Params: mustJSON(t, ThreadSampleParams{
			ThreadID:       string(record.ID),
			ExpectedTurnID: "turn-latest",
			Prompt:         "another question",
		}),
	})
	if response.Error != nil {
		t.Fatalf("thread/sample error = %+v", response.Error)
	}
	if got := threadSampleLastModel(t, agent); got != "gpt-source" {
		t.Fatalf("inherited sample model = %q, want the source model", got)
	}

	// The override never reaches the source thread's settings.
	after, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Metadata.Model != "gpt-source" {
		t.Fatalf("source thread model = %q, want it unchanged", after.Metadata.Model)
	}
}
