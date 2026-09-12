package app

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/appserver"
	"codex_go/sandbox"
)

// readinessTransport answers windowsSandbox/readiness with a fixed status.
type readinessTransport struct {
	status string
	reads  chan []byte
	method string
}

func newReadinessTransport(status string) *readinessTransport {
	return &readinessTransport{status: status, reads: make(chan []byte, 1)}
}

func (t *readinessTransport) read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.reads:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *readinessTransport) write(_ context.Context, data []byte) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	t.method = request.Method
	encoded, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(request.ID),
		"result":  map[string]any{"status": t.status},
	})
	if err != nil {
		return err
	}
	t.reads <- encoded
	return nil
}

func (t *readinessTransport) close() {}

func TestWindowsSandboxReadyViaClient(t *testing.T) {
	ready := newReadinessTransport(string(sandbox.WindowsReadinessReady))
	if !windowsSandboxReadyViaClient(context.Background(), &remoteAppServerTUIClient{transport: ready}) {
		t.Fatal("a ready app server should report ready")
	}
	if ready.method != string(appserver.MethodWindowsSandboxReadiness) {
		t.Fatalf("request method = %q", ready.method)
	}

	notConfigured := newReadinessTransport("notConfigured")
	if windowsSandboxReadyViaClient(context.Background(), &remoteAppServerTUIClient{transport: notConfigured}) {
		t.Fatal("a not-configured app server must not report ready")
	}

	if windowsSandboxReadyViaClient(context.Background(), nil) {
		t.Fatal("a missing client must not report ready")
	}
}
