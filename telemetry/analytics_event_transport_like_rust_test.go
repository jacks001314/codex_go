package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type recordingAnalyticsTransport struct {
	mu     sync.Mutex
	events []any
	skus   []*string
	handle func(event any) bool
}

func (t *recordingAnalyticsTransport) HandleAnalyticsEvent(_ context.Context, event any, productSKU *string) bool {
	t.mu.Lock()
	t.events = append(t.events, event)
	t.skus = append(t.skus, productSKU)
	handle := t.handle
	t.mu.Unlock()
	return handle != nil && handle(event)
}

func (t *recordingAnalyticsTransport) snapshot() ([]any, []*string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]any(nil), t.events...), append([]*string(nil), t.skus...)
}

// Rust #62684: events selected by the host stay off the HTTP path, while the
// rest of the batch is still delivered with product attribution.
func TestAnalyticsEventTransportHandlesSelectedEventsLikeRust(t *testing.T) {
	bodies := make(chan TrackEventsRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload error = %v", err)
		}
		w.WriteHeader(http.StatusOK)
		bodies <- payload
	}))
	defer server.Close()

	transport := &recordingAnalyticsTransport{handle: func(event any) bool {
		request, ok := event.(CodexTurnEventRequest)
		return ok && request.EventParams.ThreadID == "thread-handled"
	}}
	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		EventTransport: transport,
	})
	defer client.Close()
	client.UpdateThreadProductSKU("thread-handled", stringPtrTelemetry("plus"))

	client.TrackCodexTurnEvent(context.Background(), NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID: "thread-handled", TurnID: "turn-handled", Status: stringPtrTelemetry("completed"),
	}))
	client.TrackCodexTurnEvent(context.Background(), NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID: "thread-http", TurnID: "turn-http", Status: stringPtrTelemetry("completed"),
	}))

	var payload TrackEventsRequest
	select {
	case payload = <-bodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the unhandled analytics event")
	}
	if len(payload.Events) != 1 {
		t.Fatalf("HTTP events = %#v, want only the unhandled event", payload.Events)
	}
	var delivered CodexTurnEventRequest
	if err := json.Unmarshal(payload.Events[0], &delivered); err != nil {
		t.Fatalf("decode analytics event error = %v", err)
	}
	if delivered.EventParams.ThreadID != "thread-http" {
		t.Fatalf("HTTP event thread = %q, want thread-http", delivered.EventParams.ThreadID)
	}

	events, skus := transport.snapshot()
	if len(events) != 2 {
		t.Fatalf("transport events = %d, want both events observed", len(events))
	}
	if skus[0] == nil || *skus[0] != "plus" {
		t.Fatalf("handled event product SKU = %v, want plus", skus[0])
	}
	if skus[1] != nil {
		t.Fatalf("unattributed event product SKU = %v, want nil", *skus[1])
	}
	select {
	case extra := <-bodies:
		t.Fatalf("handled event reached HTTP: %#v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// Rust #62684: disabled analytics never constructs or invokes the transport.
func TestAnalyticsEventTransportBypassedWhenDisabledLikeRust(t *testing.T) {
	transport := &recordingAnalyticsTransport{handle: func(any) bool { return true }}
	disabled := false
	client := NewAnalyticsEventsClient(AnalyticsEventsClientOptions{
		BaseURL:          "http://127.0.0.1:1",
		AnalyticsEnabled: &disabled,
		EventTransport:   transport,
	})
	defer client.Close()
	client.TrackCodexTurnEvent(context.Background(), NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID: "thread-disabled", TurnID: "turn-disabled",
	}))
	events, _ := transport.snapshot()
	if len(events) != 0 {
		t.Fatalf("transport events with analytics disabled = %#v", events)
	}
}
