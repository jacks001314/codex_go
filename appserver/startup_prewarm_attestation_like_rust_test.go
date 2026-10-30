package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
)

type startupPrewarmAttestationAgent struct {
	captured chan *model.AgentRequest
}

func (a *startupPrewarmAttestationAgent) Run(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{}, nil
}

func (a *startupPrewarmAttestationAgent) Prewarm(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	copyRequest := *request
	a.captured <- &copyRequest
	return &model.AgentResponse{ResponseID: "resp-prewarm"}, nil
}

// Mirrors Rust #47912: thread/start registers the creating connection before
// the startup prewarm dials, so the prewarm request carries the thread id and
// the app-server attestation provider and its handshake can request attestation.
func TestStartupPrewarmCarriesThreadAttestationLikeRust(t *testing.T) {
	agent := &startupPrewarmAttestationAgent{captured: make(chan *model.AgentRequest, 1)}
	router := NewRuntimeRouter(RuntimeServices{Agent: agent})
	const (
		threadID     = "thread-prewarm-attest"
		connectionID = "conn-prewarm"
	)
	initializeAttestationTestConnection(t, router, connectionID, true)
	router.subscribeThreadConnection(threadID, connectionID)
	sink := &attestationTargetSink{router: router}
	router.SetServerRequestSink(sink)

	router.scheduleStartupPrewarm(&ThreadStartResponse{
		Thread: &Thread{ID: threadID, CWD: t.TempDir()},
		Model:  "gpt-test",
	})

	var request *model.AgentRequest
	select {
	case request = <-agent.captured:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the startup prewarm request")
	}
	if request.ThreadID != threadID {
		t.Fatalf("prewarm thread id = %q, want %q", request.ThreadID, threadID)
	}
	if request.AttestationProvider == nil {
		t.Fatal("startup prewarm request has no attestation provider")
	}
	value, ok, err := request.AttestationProvider.HeaderForRequest(context.Background(), &codexapi.AttestationContext{ThreadID: request.ThreadID})
	if err != nil {
		t.Fatalf("HeaderForRequest error = %v", err)
	}
	if !ok || value != `{"v":1,"s":0,"t":"v1.client"}` {
		t.Fatalf("prewarm attestation header = %q %v", value, ok)
	}
	if sink.connectionID != connectionID {
		t.Fatalf("attestation request connection = %q, want %q", sink.connectionID, connectionID)
	}
}
