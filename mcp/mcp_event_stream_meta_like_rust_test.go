package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestMCPEventStreamForwardsRequestMetaLikeRust mirrors Rust #41892's
// McpEventStream::open: the caller-supplied _meta object is copied into the
// events/stream params, and omitted entirely when the caller supplies none.
func TestMCPEventStreamForwardsRequestMetaLikeRust(t *testing.T) {
	seen := make(chan map[string]any, 2)
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
			meta, _ := params["_meta"].(map[string]any)
			seen <- meta
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"events": {Name: "events", Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	t.Cleanup(func() { _ = service.Close() })

	waitFor := func() map[string]any {
		t.Helper()
		select {
		case meta := <-seen:
			return meta
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the events/stream request")
		}
		return nil
	}
	stream, err := service.OpenMCPEventStreamWithMeta("events", "fileChanged", map[string]any{"project": "codex"}, map[string]any{"source": "desktop"})
	if err != nil {
		t.Fatalf("OpenMCPEventStreamWithMeta() error = %v", err)
	}
	if meta := waitFor(); meta["source"] != "desktop" {
		t.Fatalf("events/stream params _meta = %#v, want the caller meta", meta)
	}
	select {
	case <-stream.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not end after the server responded")
	}

	plain, err := service.OpenMCPEventStream("events", "fileChanged", map[string]any{"project": "codex"})
	if err != nil {
		t.Fatalf("OpenMCPEventStream() error = %v", err)
	}
	if meta := waitFor(); meta != nil {
		t.Fatalf("events/stream params _meta = %#v, want no _meta", meta)
	}
	select {
	case <-plain.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("plain stream did not end after the server responded")
	}
}
