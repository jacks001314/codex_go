package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/appserver"
)

// legacyDaemonTransport answers the first thread/list request with the error an
// older daemon returns for an array-valued cwd filter, then succeeds.
type legacyDaemonTransport struct {
	requests []threadListRequestProbe
	reads    chan []byte
}

type threadListRequestProbe struct {
	method string
	cwd    any
}

func newLegacyDaemonTransport() *legacyDaemonTransport {
	return &legacyDaemonTransport{reads: make(chan []byte, 4)}
}

func (t *legacyDaemonTransport) read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.reads:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *legacyDaemonTransport) write(_ context.Context, data []byte) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			CWD any `json:"cwd"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	t.requests = append(t.requests, threadListRequestProbe{method: request.Method, cwd: request.Params.CWD})
	if len(t.requests) == 1 {
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"error": map[string]any{
				"code":    appserver.JSONRPCInvalidRequestErrorCode,
				"message": "Invalid request: invalid type: sequence, expected a string",
			},
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return err
		}
		t.reads <- encoded
		return nil
	}
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(request.ID),
		"result":  map[string]any{"data": []any{}, "nextCursor": nil},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	t.reads <- encoded
	return nil
}

func (t *legacyDaemonTransport) close() {}

func TestRemoteThreadListFallsBackToSingleCWDForLegacyDaemon(t *testing.T) {
	transport := newLegacyDaemonTransport()
	client := &remoteAppServerTUIClient{transport: transport}
	params := appserver.ThreadListParams{
		CWD: &appserver.ThreadListCwdFilter{Values: []string{`D:\repo`, `D:\repo-linked`}},
	}

	if _, err := remoteThreadListWithCwdFallback(context.Background(), client, params); err != nil {
		t.Fatalf("remoteThreadListWithCwdFallback() error = %v", err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("requests = %#v, want a retry", transport.requests)
	}
	if _, isList := transport.requests[0].cwd.([]any); !isList {
		t.Fatalf("first cwd = %#v, want the array-valued filter", transport.requests[0].cwd)
	}
	single, ok := transport.requests[1].cwd.(string)
	if !ok || single != `D:\repo` {
		t.Fatalf("fallback cwd = %#v, want the originally requested directory", transport.requests[1].cwd)
	}
}

func TestRemoteThreadListDoesNotRetryUnrelatedErrors(t *testing.T) {
	transport := &legacyDaemonTransport{reads: make(chan []byte, 1)}
	client := &remoteAppServerTUIClient{transport: transport}
	// The transport answers the first request with the legacy error; the first
	// request here uses a single directory, so no fallback applies and the error
	// must surface.
	params := appserver.ThreadListParams{
		CWD: &appserver.ThreadListCwdFilter{Values: []string{`D:\repo`}},
	}
	err := func() error {
		original := transport.requests
		_, err := remoteThreadListWithCwdFallback(context.Background(), client, params)
		if len(transport.requests) != len(original)+1 {
			t.Fatalf("requests = %#v, want a single attempt", transport.requests)
		}
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "expected a string") {
		t.Fatalf("error = %v, want the legacy daemon failure", err)
	}
}
