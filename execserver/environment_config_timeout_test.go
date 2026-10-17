package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func readEnvironmentConfigRequest(t *testing.T, conn *networkPolicyTestConnection) int64 {
	t.Helper()
	select {
	case data := <-conn.writes:
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatalf("Unmarshal request error = %v", err)
		}
		if request.Method != MethodEnvironmentConfigRead {
			t.Fatalf("method = %q, want %q", request.Method, MethodEnvironmentConfigRead)
		}
		return request.ID
	case <-time.After(time.Second):
		t.Fatal("environmentConfig/read request was not written")
		return 0
	}
}

func writeEnvironmentConfigResponse(t *testing.T, conn *networkPolicyTestConnection, id int64, hostname string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"codexHomeDir": "/executor",
			"hostname":     hostname,
			"config": map[string]any{
				"layers":              []any{},
				"cloudInsertionIndex": 0,
			},
			"requirements": map[string]any{
				"layers":              []any{},
				"cloudInsertionIndex": 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal response error = %v", err)
	}
	conn.reads <- data
}

func TestEnvironmentConfigReadTimeoutKeepsStdioConnectionUsableLikeRust(t *testing.T) {
	conn := newNetworkPolicyTestConnection(16)
	client := newNetworkPolicyTestClient(conn)
	client.sessionID = "stdio-config-timeout"
	client.reconnectDisabled = true
	go client.readLoop(conn)
	defer client.Close()

	params := &EnvironmentConfigReadParams{CWD: "file:///workspace"}
	original := make(chan struct {
		response *EnvironmentConfigReadResponse
		err      error
	}, 1)
	go func() {
		response, err := client.ReadEnvironmentConfig(context.Background(), params)
		original <- struct {
			response *EnvironmentConfigReadResponse
			err      error
		}{response: response, err: err}
	}()
	originalID := readEnvironmentConfigRequest(t, conn)

	rpcTimeout := 20 * time.Millisecond
	timed := make(chan error, 1)
	var timedID int64
	go func() {
		_, err := client.ReadEnvironmentConfigWithTimeout(context.Background(), params, rpcTimeout)
		timed <- err
	}()
	timedID = readEnvironmentConfigRequest(t, conn)

	select {
	case err := <-timed:
		var timeoutErr *RPCTimedOutError
		if !errors.As(err, &timeoutErr) || timeoutErr.Method != MethodEnvironmentConfigRead || timeoutErr.Timeout != rpcTimeout {
			t.Fatalf("timed read error = %#v, want RpcTimedOut", err)
		}
		if !IsRetryableRecoveryError(err) {
			t.Fatal("RPC timeout must be retryable for read-only preparation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed configuration read did not return")
	}

	client.mu.Lock()
	current := client.conn
	client.mu.Unlock()
	if current != clientConnection(conn) {
		t.Fatal("timed-out stdio config read retired the connection")
	}
	select {
	case result := <-original:
		t.Fatalf("in-flight read finished early: %#v", result)
	default:
	}

	writeEnvironmentConfigResponse(t, conn, originalID, "original")
	select {
	case result := <-original:
		if result.err != nil || result.response == nil || result.response.Hostname == nil || *result.response.Hostname != "original" {
			t.Fatalf("original read = %#v, error = %v", result.response, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("original in-flight read did not finish")
	}

	// A late response for the expired request is ignored, and a fresh read uses
	// the same stdio connection.
	writeEnvironmentConfigResponse(t, conn, timedID, "late")
	retry := make(chan struct {
		response *EnvironmentConfigReadResponse
		err      error
	}, 1)
	go func() {
		response, err := client.ReadEnvironmentConfigWithTimeout(context.Background(), params, time.Second)
		retry <- struct {
			response *EnvironmentConfigReadResponse
			err      error
		}{response: response, err: err}
	}()
	retryID := readEnvironmentConfigRequest(t, conn)
	writeEnvironmentConfigResponse(t, conn, retryID, "fresh")
	select {
	case result := <-retry:
		if result.err != nil || result.response == nil || result.response.Hostname == nil || *result.response.Hostname != "fresh" {
			t.Fatalf("retry read = %#v, error = %v", result.response, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry read did not finish")
	}
}
