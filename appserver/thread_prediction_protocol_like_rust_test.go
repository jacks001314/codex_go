package appserver

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #49480's protocol surface: the experimental prediction request
// and notification use camelCase members, and a result is either `completed`
// with optional text or `failed` with no text at all.
func TestThreadPredictionProtocolTypesLikeRust(t *testing.T) {
	params, err := json.Marshal(ThreadPredictionRequestParams{ThreadID: "thread-1", SourceTurnID: "turn-1"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if string(params) != `{"threadId":"thread-1","sourceTurnId":"turn-1"}` {
		t.Fatalf("params = %s", params)
	}

	text := "next question"
	cases := []struct {
		name   string
		result ThreadPredictionResult
		want   string
	}{
		{"completed_with_text", ThreadPredictionResult{Type: "completed", Text: &text}, `{"type":"completed","text":"next question"}`},
		{"completed_without_text", ThreadPredictionResult{Type: "completed"}, `{"type":"completed"}`},
		{"failed", ThreadPredictionResult{Type: "failed"}, `{"type":"failed"}`},
	}
	for _, tc := range cases {
		notification := ThreadPredictionUpdatedNotification{ThreadID: "thread-1", SourceTurnID: "turn-1", Result: tc.result}
		encoded, err := json.Marshal(notification)
		if err != nil {
			t.Fatalf("%s: marshal notification: %v", tc.name, err)
		}
		want := `{"threadId":"thread-1","sourceTurnId":"turn-1","result":` + tc.want + `}`
		if string(encoded) != want {
			t.Fatalf("%s: notification = %s, want %s", tc.name, encoded, want)
		}
	}

	if got := string(MethodThreadPredictionRequest); got != "thread/prediction/request" {
		t.Fatalf("request method = %q", got)
	}
	if got := string(NotificationThreadPredictionUpdated); got != "thread/prediction/updated" {
		t.Fatalf("notification method = %q", got)
	}
	if !protocolMethodListed(experimentalClientRequestMethods(), string(MethodThreadPredictionRequest)) {
		t.Fatal("thread/prediction/request must remain experimental")
	}
	if !protocolMethodListed(notificationMethods(true), string(NotificationThreadPredictionUpdated)) {
		t.Fatal("thread/prediction/updated must remain experimental")
	}
}

func protocolMethodListed(methods []ProtocolMethod, method string) bool {
	for _, entry := range methods {
		if entry.Method == method && entry.Experimental {
			return true
		}
	}
	return false
}

// Rust #49480 defines the request but leaves it unimplemented, so the
// app-server answers a capability-enabled call with method-not-found.
func TestThreadPredictionRequestIsMethodNotFoundLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	initResponse := router.Handle(requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-test", Version: "1.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	}))
	if initResponse.Error != nil {
		t.Fatalf("initialize = %+v", initResponse.Error)
	}
	params, err := json.Marshal(ThreadPredictionRequestParams{ThreadID: "thread-1", SourceTurnID: "turn-1"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	response := router.Handle(&Request{
		JSONRPC: "2.0",
		ID:      IntID(2),
		Method:  MethodThreadPredictionRequest,
		Params:  params,
	})
	if response == nil || response.Error == nil || response.Error.Code != JSONRPCMethodNotFoundErrorCode {
		if response != nil && response.Error != nil {
			t.Fatalf("response error = %d %q", response.Error.Code, response.Error.Message)
		}
		t.Fatalf("response = %+v", response)
	}
}
