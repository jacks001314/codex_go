package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

type persistenceIntentRuntimeAgent struct {
	requests chan model.AgentRequest
}

func (a *persistenceIntentRuntimeAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	return &model.AgentResponse{
		ResponseID: "resp-persistence-intent",
		Message:    "ok",
		Items:      []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "ok"}},
	}, nil
}

func (a *persistenceIntentRuntimeAgent) nextRequest(t *testing.T) model.AgentRequest {
	t.Helper()
	select {
	case request := <-a.requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the model request")
		return model.AgentRequest{}
	}
}

// TestRuntimeRouterTurnCarriesThreadPersistenceIntentLikeRust covers Rust
// #51517 for the app-server turn path: a persistent thread's model request is
// not marked ephemeral, and an ephemeral thread's request is, which is what the
// attachment-store uploads made while preparing it observe.
func TestRuntimeRouterTurnCarriesThreadPersistenceIntentLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		ephemeral bool
	}{
		{name: "persistent", ephemeral: false},
		{name: "ephemeral", ephemeral: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			agent := &persistenceIntentRuntimeAgent{requests: make(chan model.AgentRequest, 4)}
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Turns:        turn.NewTurnService(),
				Agent:        agent,
				ThreadStatus: NewThreadStatusManager(),
			})
			router.SetNotificationSink(NewNotificationBuffer())
			t.Cleanup(func() { _ = router.Close() })

			start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD:       t.TempDir(),
				Ephemeral: tc.ephemeral,
			}))
			if start.Error != nil {
				t.Fatalf("thread start error: %+v", start.Error)
			}
			thread := start.Result.(*ThreadStartResponse).Thread
			if thread.Ephemeral != tc.ephemeral {
				t.Fatalf("thread ephemeral = %v, want %v", thread.Ephemeral, tc.ephemeral)
			}
			turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
				ThreadID: thread.ID,
				Prompt:   "hello",
			}))
			if turnStart.Error != nil {
				t.Fatalf("turn start error: %+v", turnStart.Error)
			}

			request := agent.nextRequest(t)
			if request.ThreadID != thread.ID {
				t.Fatalf("request thread id = %q, want %q", request.ThreadID, thread.ID)
			}
			if request.Ephemeral != tc.ephemeral {
				t.Fatalf("request ephemeral = %v, want %v", request.Ephemeral, tc.ephemeral)
			}
		})
	}
}
