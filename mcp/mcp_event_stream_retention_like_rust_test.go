package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMCPEventStreamRetainsClientUntilClose mirrors Rust #41892: the event
// stream borrows the client for the lifetime of the subscription, keeps
// delivering lifecycle notifications over the live SSE transport, and releases
// that transport when the subscription is closed.
func TestMCPEventStreamRetainsClientUntilClose(t *testing.T) {
	streamClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "events", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "events/stream":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("Unmarshal() params error = %v", err)
				return
			}
			if params["name"] != "fileChanged" {
				t.Errorf("events/stream name = %#v, want fileChanged", params["name"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, notification := range []string{
				`{"jsonrpc":"2.0","method":"notifications/events/active","params":{}}`,
				`{"jsonrpc":"2.0","method":"notifications/event","params":{"name":"fileChanged","sequence":2}}`,
			} {
				if _, err := io.WriteString(w, "data: "+notification+"\n\n"); err != nil {
					t.Errorf("WriteString() error = %v", err)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
				t.Error("event stream transport was not released after Close")
			}
			close(streamClosed)
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"events": {Name: "events", Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	t.Cleanup(func() { _ = service.Close() })

	stream, err := service.OpenMCPEventStream("events", "fileChanged", map[string]any{})
	if err != nil {
		t.Fatalf("OpenMCPEventStream() error = %v", err)
	}
	readNotification := func() *MCPEventNotification {
		t.Helper()
		select {
		case notification := <-stream.Notifications():
			if notification == nil {
				t.Fatal("stream delivered a nil notification")
			}
			return notification
		case <-stream.Done():
			t.Fatalf("stream ended before delivering a notification: %v", stream.Err())
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for an event notification")
		}
		return nil
	}
	if notification := readNotification(); notification.Method != "notifications/events/active" {
		t.Fatalf("first notification = %q, want notifications/events/active", notification.Method)
	}
	second := readNotification()
	if second.Method != "notifications/event" {
		t.Fatalf("second notification = %q, want notifications/event", second.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(second.Params, &params); err != nil {
		t.Fatalf("notification params = %q (%v)", second.Params, err)
	}
	if params["sequence"] != float64(2) {
		t.Fatalf("notification params = %#v, want sequence 2", params)
	}

	stream.Close()
	select {
	case <-stream.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not release the transport")
	}
	select {
	case <-streamClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("SSE handler kept running after Close()")
	}
}
