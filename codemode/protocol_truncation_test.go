package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/tool"
)

func intPtr(value int) *int { return &value }

func TestSessionOpenOutputTruncationWireShapeLikeRust(t *testing.T) {
	sessionID, _ := NewSessionID("session-1")
	server := OutputTruncationServer
	request := OpenSessionRequestWithOptions(sessionID, SessionOptions{OutputTruncation: server})
	data, err := json.Marshal(OperationRequest(1, request))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"outputTruncation":"server"`) {
		t.Fatalf("server session/open = %s", data)
	}
	var decoded ClientToHost
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Request == nil || decoded.Request.OutputTruncation == nil || *decoded.Request.OutputTruncation != server {
		t.Fatalf("decoded session/open = %#v", decoded.Request)
	}

	for _, truncation := range []OutputTruncation{"", OutputTruncationClient} {
		legacy := OpenSessionRequestWithOptions(sessionID, SessionOptions{OutputTruncation: truncation})
		data, err := json.Marshal(OperationRequest(1, legacy))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "outputTruncation") {
			t.Fatalf("legacy session/open with %q = %s", truncation, data)
		}
	}
}

func TestWaitAndTerminateOutputBudgetWireShapeLikeRust(t *testing.T) {
	sessionID, _ := NewSessionID("session-1")
	cellID := NewCellID("cell-1")

	wait := WaitSessionRequest(sessionID, WaitRequest{CellID: cellID, YieldTimeMS: 10})
	legacyWait, err := json.Marshal(OperationRequest(1, wait))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyWait), "maxOutputTokens") {
		t.Fatalf("legacy wait = %s", legacyWait)
	}

	wait.MaxOutputTokens = intPtr(500)
	data, err := json.Marshal(OperationRequest(1, wait))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"maxOutputTokens":500`) {
		t.Fatalf("bounded wait = %s", data)
	}
	var decodedWait ClientToHost
	if err := json.Unmarshal(data, &decodedWait); err != nil {
		t.Fatal(err)
	}
	if decodedWait.Request == nil || decodedWait.Request.Wait == nil {
		t.Fatalf("decoded wait = %#v", decodedWait.Request)
	}
	if decodedWait.Request.MaxOutputTokens == nil || *decodedWait.Request.MaxOutputTokens != 500 {
		t.Fatalf("decoded wait budget = %#v", decodedWait.Request.MaxOutputTokens)
	}
	if decodedWait.Request.Wait.MaxOutputTokens == nil || *decodedWait.Request.Wait.MaxOutputTokens != 500 {
		t.Fatalf("decoded wait request budget = %#v", decodedWait.Request.Wait.MaxOutputTokens)
	}

	terminate := TerminateSessionRequestWithBudget(sessionID, TerminateRequest{CellID: cellID, MaxOutputTokens: intPtr(64)})
	data, err = json.Marshal(OperationRequest(2, terminate))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"maxOutputTokens":64`) || !strings.Contains(string(data), `"cellId":"cell-1"`) {
		t.Fatalf("bounded terminate = %s", data)
	}
	var decodedTerminate ClientToHost
	if err := json.Unmarshal(data, &decodedTerminate); err != nil {
		t.Fatal(err)
	}
	if decodedTerminate.Request == nil || decodedTerminate.Request.MaxOutputTokens == nil || *decodedTerminate.Request.MaxOutputTokens != 64 {
		t.Fatalf("decoded terminate = %#v", decodedTerminate.Request)
	}

	legacyTerminate, err := json.Marshal(OperationRequest(3, TerminateSessionRequest(sessionID, cellID)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyTerminate), "maxOutputTokens") {
		t.Fatalf("legacy terminate = %s", legacyTerminate)
	}
}

func TestValidateSessionOptionsLikeRust(t *testing.T) {
	for _, options := range []SessionOptions{{}, {OutputTruncation: OutputTruncationClient}} {
		if err := ValidateSessionOptions(options); err != nil {
			t.Fatalf("ValidateSessionOptions(%#v) error = %v", options, err)
		}
	}
	err := ValidateSessionOptions(SessionOptions{OutputTruncation: OutputTruncationServer})
	if err == nil || err.Error() != "code-mode session provider does not support server output truncation" {
		t.Fatalf("server truncation error = %v", err)
	}
	if err := ValidateSessionOptions(SessionOptions{OutputTruncation: "host"}); err == nil {
		t.Fatalf("unknown truncation should fail")
	}
}

func TestCreateSessionWithOptionsRejectsServerTruncationLikeRust(t *testing.T) {
	provider := &recordingSessionProvider{}
	if _, err := CreateSessionWithOptions(provider, nil, SessionOptions{}); err != nil {
		t.Fatalf("client session error = %v", err)
	}
	if provider.created != 1 {
		t.Fatalf("client session created %d times", provider.created)
	}
	if _, err := CreateSessionWithOptions(provider, nil, SessionOptions{OutputTruncation: OutputTruncationServer}); err == nil {
		t.Fatalf("server truncation should be rejected")
	}
	if provider.created != 1 {
		t.Fatalf("rejected server session created a session")
	}
	if _, err := CreateSessionWithOptions(nil, nil, SessionOptions{}); err == nil {
		t.Fatalf("nil provider should fail")
	}
}

func TestSessionHostCapturesOutputTruncationLikeRust(t *testing.T) {
	sessionID, _ := NewSessionID("session-1")
	host := NewSessionHost(nil)
	if host.OutputTruncation() != OutputTruncationClient {
		t.Fatalf("default truncation = %q", host.OutputTruncation())
	}
	server := OutputTruncationServer
	if _, _, err := host.Handle(context.Background(), &HostRequest{Method: "session/open", SessionID: sessionID, OutputTruncation: &server}, nil); err != nil {
		t.Fatalf("Handle(session/open) error = %v", err)
	}
	if host.OutputTruncation() != server {
		t.Fatalf("captured truncation = %q", host.OutputTruncation())
	}
}

type recordingSessionProvider struct {
	created int
}

func (p *recordingSessionProvider) NewSession(tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	p.created++
	return nil
}
