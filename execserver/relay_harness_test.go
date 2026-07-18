package execserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDialClientUsesRelayFramesForHarnessURLLikeRust(t *testing.T) {
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			done <- err
			return
		}
		if messageType != websocket.MessageBinary {
			done <- errUnexpectedMessageType(messageType)
			return
		}
		resume, err := decodeRelayMessageFrame(payload)
		if err != nil || resume.Kind != relayFrameResume {
			if err == nil {
				err = errorsForRelayTest("first frame was not resume")
			}
			done <- err
			return
		}
		streamID := resume.StreamID
		var responseSeq uint32
		for {
			messageType, payload, err = conn.Read(ctx)
			if err != nil {
				done <- err
				return
			}
			if messageType != websocket.MessageBinary {
				done <- errUnexpectedMessageType(messageType)
				return
			}
			frame, err := decodeRelayMessageFrame(payload)
			if err != nil {
				done <- err
				return
			}
			if frame.StreamID != streamID || frame.Kind != relayFrameData {
				done <- errorsForRelayTest("unexpected relay data frame")
				return
			}
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(frame.Data.Payload, &request); err != nil {
				done <- err
				return
			}
			if request.Method == MethodInitialized {
				continue
			}
			var response any
			switch request.Method {
			case MethodInitialize:
				response = map[string]any{"jsonrpc": "2.0", "id": 1, "result": InitializeResponse{SessionID: "relay-session"}}
			case MethodEnvironmentInfo:
				response = map[string]any{"jsonrpc": "2.0", "id": 1, "result": EnvironmentInfo{Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}}
			default:
				done <- errorsForRelayTest("unexpected JSON-RPC method " + request.Method)
				return
			}
			encodedResponse, err := json.Marshal(response)
			if err != nil {
				done <- err
				return
			}
			encodedFrame, err := encodeRelayMessageFrame(newRelayDataFrame(streamID, responseSeq, encodedResponse))
			if err != nil {
				done <- err
				return
			}
			responseSeq++
			if err := conn.Write(ctx, websocket.MessageBinary, encodedFrame); err != nil {
				done <- err
				return
			}
			if request.Method == MethodEnvironmentInfo {
				done <- nil
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialClient(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/relay?role=harness", "relay-harness-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		_ = client.Close()
		t.Fatalf("EnvironmentInfo() error = %v", err)
	}
	if info.Shell.Name != "bash" || info.Shell.Path != "/bin/bash" {
		_ = client.Close()
		t.Fatalf("environment info = %#v", info)
	}
	_ = client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay server error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay server did not finish")
	}
}

func TestIsRendezvousHarnessURLMatchesRustExactQueryParsing(t *testing.T) {
	tests := map[string]bool{
		"ws://example.test/relay?role=harness":               true,
		"ws://example.test/relay?a=1&role=harness&b=2":       true,
		"ws://example.test/relay?role=environment":           false,
		"ws://example.test/relay?Role=harness":               false,
		"ws://example.test/relay?role=harness%20":            false,
		"ws://example.test/relay?role=harness#fragment":      false,
		"ws://example.test/relay?role=harness&role=executor": true,
	}
	for value, want := range tests {
		if got := isRendezvousHarnessURL(value); got != want {
			t.Fatalf("isRendezvousHarnessURL(%q) = %v, want %v", value, got, want)
		}
	}
}

type relayTestError string

func (e relayTestError) Error() string { return string(e) }

func errorsForRelayTest(message string) error { return relayTestError(message) }
