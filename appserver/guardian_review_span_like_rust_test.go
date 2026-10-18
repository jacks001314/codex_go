package appserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/state"
	"codex_go/telemetry"
)

// Rust #52274: every approval review opens a debug `guardian_review` span with
// the review, thread, turn, and tool-call identifiers.
func TestGuardianReviewEmitsIdentifiedSpanLikeRust(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	traces := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
	})
	reviewer := newModelGuardianReviewer(&guardianRecordingAgent{}).(*modelGuardianReviewer)
	reviewer.tracer = traces.Tracer
	_, _, _ = reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type:     "mcp_tool_call",
		Server:   "codex_apps",
		ToolName: "webmcp:example.com:write_record",
	})
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	attributes := map[string]string{}
	var name string
	select {
	case body := <-bodies:
		for _, scopeSpans := range listOfMaps(body["resourceSpans"]) {
			for _, scoped := range listOfMaps(scopeSpans["scopeSpans"]) {
				for _, span := range listOfMaps(scoped["spans"]) {
					if span["name"] != "guardian_review" {
						continue
					}
					name = "guardian_review"
					for _, attribute := range listOfMaps(span["attributes"]) {
						key, _ := attribute["key"].(string)
						value, _ := attribute["value"].(map[string]any)
						if text, ok := value["stringValue"].(string); ok {
							attributes[key] = text
						}
					}
				}
			}
		}
	default:
		t.Fatal("the traces endpoint received no span batch")
	}
	if name != "guardian_review" {
		t.Fatalf("exported spans are missing guardian_review: %#v", attributes)
	}
	if attributes["thread_id"] != "thread-1" || attributes["turn_id"] != "turn-1" ||
		attributes["tool_call_id"] != "call-1" || attributes["review_id"] == "" {
		t.Fatalf("guardian_review span attributes = %#v", attributes)
	}
}

func listOfMaps(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if values, ok := item.(map[string]any); ok {
			out = append(out, values)
		}
	}
	return out
}
