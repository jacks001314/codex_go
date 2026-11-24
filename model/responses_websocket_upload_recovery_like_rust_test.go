package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Mirrors Rust #62749: a server that rejects an oversized upload after reading
// only its first frame header answers before the client's broken pipe, and that
// answer is the request outcome instead of a retry on a fresh socket.
func TestResponsesWebsocketFailedUploadAdoptsServerErrorLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			return
		}
		// The peer never reads the upload; it reports the reason first and only
		// then retires the socket, so the client's write fails afterwards.
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"error","error":{"code":"request_too_large","message":"Upload exceeded the endpoint limit."}}`))
		time.Sleep(150 * time.Millisecond)
		_ = conn.Close(websocket.StatusMessageTooBig, `message too large`)
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Auth:                    &AuthHeaders{Headers: http.Header{`Authorization`: []string{`Bearer secret`}}},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Model:  `gpt-test`,
		Prompt: strings.Repeat(`x`, 8<<20),
	})
	if err == nil {
		t.Fatal(`oversized upload reported no error`)
	}
	if !strings.Contains(err.Error(), `Upload exceeded the endpoint limit.`) {
		t.Fatalf(`error = %v, want the server's rejection to be adopted`, err)
	}
}

func TestResponsesWebsocketUploadRecoveryAdoptsErrorEventLikeRust(t *testing.T) {
	messages := make(chan responsesWebsocketReadResult, 4)
	messages <- responsesWebsocketReadResult{data: []byte(`{"type":"response.created","response":{"id":"resp-1"}}`)}
	messages <- responsesWebsocketReadResult{data: []byte(`{"type":"error","error":{"code":"server_error","message":"boom"}}`)}
	recovered := recoverResponsesWebsocketUploadReply(messages)
	if recovered.event == nil || websocketEventError(recovered.event) != `boom` {
		t.Fatalf(`recovered = %#v, want the server error event`, recovered)
	}
	if recovered.oversized {
		t.Fatal(`error event reported an oversized close`)
	}
}

func TestResponsesWebsocketUploadRecoveryReportsOversizedCloseLikeRust(t *testing.T) {
	messages := make(chan responsesWebsocketReadResult, 1)
	messages <- responsesWebsocketReadResult{err: websocket.CloseError{Code: websocket.StatusMessageTooBig, Reason: `too large`}}
	recovered := recoverResponsesWebsocketUploadReply(messages)
	if !recovered.oversized {
		t.Fatalf(`recovered = %#v, want the 1009 close surfaced`, recovered)
	}
	if recovered.event != nil {
		t.Fatalf(`oversized close adopted an event: %#v`, recovered.event)
	}
}

func TestResponsesWebsocketUploadRecoveryStopsWhenNothingIsReadableLikeRust(t *testing.T) {
	messages := make(chan responsesWebsocketReadResult, 4)
	messages <- responsesWebsocketReadResult{data: []byte(`{"type":"response.created","response":{"id":"resp-1"}}`)}
	recovered := recoverResponsesWebsocketUploadReply(messages)
	if recovered.event != nil || recovered.oversized {
		t.Fatalf(`recovered = %#v, want no terminal reply for ordinary traffic`, recovered)
	}
	if len(messages) != 0 {
		t.Fatalf(`recovery left %d frames unread, want the readable prefix drained`, len(messages))
	}
	if recovered := recoverResponsesWebsocketUploadReply(messages); recovered.event != nil || recovered.oversized {
		t.Fatalf(`empty channel recovery = %#v, want nothing`, recovered)
	}
}

func TestResponsesWebsocketUploadRecoveryRespectsReadBudgetLikeRust(t *testing.T) {
	messages := make(chan responsesWebsocketReadResult, 32)
	filler := make([]byte, 64<<10)
	written := 0
	for written <= responsesWebsocketUploadReadAheadBytes {
		messages <- responsesWebsocketReadResult{data: filler}
		written += len(filler)
	}
	messages <- responsesWebsocketReadResult{data: []byte(`{"type":"error","error":{"code":"server_error","message":"late"}}`)}
	recovered := recoverResponsesWebsocketUploadReply(messages)
	if recovered.event != nil {
		t.Fatalf(`recovered = %#v, want the budget to stop before the late error`, recovered)
	}
	if len(messages) == 0 {
		t.Fatal(`recovery drained past its read-ahead budget`)
	}
}
