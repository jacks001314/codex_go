package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryCleanupTranscript struct {
	mu     sync.Mutex
	events []string
}

func (t *recoveryCleanupTranscript) add(event string) {
	t.mu.Lock()
	t.events = append(t.events, event)
	t.mu.Unlock()
}

func (t *recoveryCleanupTranscript) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.events...)
}

func TestHTTPMCPAbandonedInitializeSessionDeletedBeforeRetry(t *testing.T) {
	var initializeCount int
	transcript := &recoveryCleanupTranscript{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			sessionID := r.Header.Get(mcpHTTPSessionIDHeader)
			transcript.add("delete:" + sessionID)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		transcript.add(request.Method)
		switch request.Method {
		case "initialize":
			initializeCount++
			if initializeCount == 1 {
				w.Header().Set(mcpHTTPSessionIDHeader, "abandoned-session")
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":`, request.ID)
				return
			}
			w.Header().Set(mcpHTTPSessionIDHeader, "healthy-session")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get(mcpHTTPSessionIDHeader); got != "healthy-session" {
				t.Errorf("tools/list session = %q, want healthy-session", got)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	client.retrySleep = func(time.Duration) {}
	if err := client.Call("tools/list", map[string]any{}, &struct {
		Tools []MCPToolInfo `json:"tools"`
	}{}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	got := strings.Join(transcript.snapshot(), ",")
	want := "initialize,delete:abandoned-session,initialize,notifications/initialized,tools/list"
	if got != want {
		t.Fatalf("request sequence = %q, want %q", got, want)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	got = strings.Join(transcript.snapshot(), ",")
	if got != want+",delete:healthy-session" {
		t.Fatalf("request sequence after Close = %q", got)
	}
}

func TestHTTPMCPFailedInitializedNotificationDeletesSessionBeforeRetry(t *testing.T) {
	var initializeCount int
	transcript := &recoveryCleanupTranscript{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			transcript.add("delete:" + r.Header.Get(mcpHTTPSessionIDHeader))
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		transcript.add(request.Method)
		switch request.Method {
		case "initialize":
			initializeCount++
			sessionID := "session-1"
			if initializeCount == 2 {
				sessionID = "session-2"
			}
			w.Header().Set(mcpHTTPSessionIDHeader, sessionID)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			if initializeCount == 1 {
				http.Error(w, "temporary failure", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	client.retrySleep = func(time.Duration) {}
	if err := client.Call("tools/list", map[string]any{}, &struct {
		Tools []MCPToolInfo `json:"tools"`
	}{}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	got := strings.Join(transcript.snapshot(), ",")
	want := "initialize,notifications/initialized,delete:session-1,initialize,notifications/initialized,tools/list"
	if got != want {
		t.Fatalf("request sequence = %q, want %q", got, want)
	}
}

func TestHTTPMCPHandshakeCleanupContinuesAfterCallerDeadline(t *testing.T) {
	deleted := make(chan string, 1)
	var deleteCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCount.Add(1)
			deleted <- r.Header.Get(mcpHTTPSessionIDHeader)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		if request.Method != "initialize" {
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
			return
		}
		w.Header().Set(mcpHTTPSessionIDHeader, "timed-out-session")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":`, request.ID)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	client.retrySleep = func(time.Duration) {}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := client.CallWithOptionsContext(ctx, nil, "tools/list", map[string]any{}, &struct {
		Tools []MCPToolInfo `json:"tools"`
	}{})
	if err == nil {
		t.Fatal("CallWithOptionsContext() error = nil, want caller deadline")
	}
	select {
	case sessionID := <-deleted:
		if sessionID != "timed-out-session" {
			t.Fatalf("deleted session = %q", sessionID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned session DELETE did not finish after caller deadline")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := deleteCount.Load(); got != 1 {
		t.Fatalf("DELETE count = %d, want 1", got)
	}
}

func TestHTTPMCPAbandonedSessionDeleteIsCoordinated(t *testing.T) {
	deleteStarted := make(chan struct{})
	deleteRelease := make(chan struct{})
	var deleteOnce sync.Once
	var deleteCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		deleteCount.Add(1)
		deleteOnce.Do(func() { close(deleteStarted) })
		<-deleteRelease
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	client.trackHandshakeSession("shared-session")
	errs := make(chan error, 2)
	go func() {
		errs <- client.cleanupAbandonedHandshakeSession(context.Background(), "shared-session")
	}()
	go func() {
		errs <- client.cleanupAbandonedHandshakeSession(context.Background(), "shared-session")
	}()
	select {
	case <-deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("DELETE did not start")
	}
	close(deleteRelease)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("cleanup error = %v", err)
		}
	}
	if got := deleteCount.Load(); got != 1 {
		t.Fatalf("DELETE count = %d, want 1", got)
	}
}
