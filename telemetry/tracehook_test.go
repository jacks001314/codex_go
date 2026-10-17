package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/tracehook"
)

func TestWithSpanCarriesTracehookAdapter(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("decode trace batch: %v", err)
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewTracesClient(TracesClientOptions{
		ServiceName:    "tracehook-test",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	parent := client.Tracer().StartSpan("exec_server.request", nil)
	ctx := WithSpan(context.Background(), parent)
	hook := tracehook.SpanFromContext(ctx)
	if hook == nil {
		t.Fatal("tracehook span missing from telemetry context")
	}
	hook.SetAttribute("rpc.dispatch_offset_ns", "7")
	hook.AddEvent("request_event", map[string]string{"rpc.method": "initialize"})
	child := hook.StartChildSpan("process_start", map[string]string{"process.id": "process-1"})
	if child == nil {
		t.Fatal("tracehook child span is nil")
	}
	child.AddEvent("child_event", nil)
	child.End()
	hook.End()
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	encoded, err := json.Marshal(bodies)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{
		`"name":"process_start"`,
		`"name":"child_event"`,
		`"key":"rpc.dispatch_offset_ns"`,
		`"stringValue":"7"`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("trace export missing %s: %s", want, encoded)
		}
	}
}
