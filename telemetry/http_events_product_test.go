package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestAnalyticsRequestsUsePerThreadProductSKULikeRust(t *testing.T) {
	var mu sync.Mutex
	requests := []TrackEventsRequest{}
	headers := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload TrackEventsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		mu.Lock()
		requests = append(requests, payload)
		headers = append(headers, r.Header.Get(productSKUHeader))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporter := NewHTTPAnalyticsExporter(AnalyticsEventsClientOptions{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	productA, productB := "codex", "tpp"
	exporter.UpdateThreadProductSKU("thread-a", &productA)
	exporter.UpdateThreadProductSKU("thread-b", &productB)
	turn := func(threadID, turnID string) CodexTurnEventRequest {
		return NewCodexTurnEvent(CodexTurnEventInput{ThreadID: threadID, SessionID: threadID, TurnID: turnID})
	}
	if err := exporter.SendTrackEvents(context.Background(), []any{
		turn("thread-a", "a-1"),
		turn("thread-b", "b-1"),
		turn("thread-a", "a-2"),
	}); err != nil {
		t.Fatalf("SendTrackEvents() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want one per product", len(requests))
	}
	byHeader := map[string]TrackEventsRequest{}
	for index, header := range headers {
		byHeader[header] = requests[index]
	}
	if len(byHeader[productA].Events) != 2 || len(byHeader[productB].Events) != 1 {
		t.Fatalf("product batches = %#v", byHeader)
	}
	var first ThreadTurnEventEnvelope
	if err := json.Unmarshal(byHeader[productA].Events[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.EventParams.TurnID != "a-1" {
		t.Fatalf("first product event turn = %q", first.EventParams.TurnID)
	}

	exporter.UpdateThreadProductSKU("thread-a", nil)
	requests = nil
	headers = nil
	if err := exporter.SendTrackEvents(context.Background(), []any{turn("thread-a", "a-3")}); err != nil {
		t.Fatalf("SendTrackEvents(cleared) error = %v", err)
	}
	if len(headers) != 1 || headers[0] != "" {
		t.Fatalf("cleared product headers = %#v", headers)
	}
}

type ThreadTurnEventEnvelope struct {
	EventParams struct {
		TurnID string `json:"turn_id"`
	} `json:"event_params"`
}
