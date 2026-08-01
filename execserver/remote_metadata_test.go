package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type metadataWireRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func newMetadataTestClient(t *testing.T) (*Client, *networkPolicyTestConnection) {
	t.Helper()
	conn := newNetworkPolicyTestConnection(32)
	client := newNetworkPolicyTestClient(conn)
	go client.readLoop(conn)
	t.Cleanup(func() { _ = client.Close() })
	return client, conn
}

func nextMetadataWireRequest(t *testing.T, conn *networkPolicyTestConnection) metadataWireRequest {
	t.Helper()
	select {
	case data := <-conn.writes:
		var request metadataWireRequest
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exec-server request")
		return metadataWireRequest{}
	}
}

func respondMetadataWire(t *testing.T, conn *networkPolicyTestConnection, id int64, result any, rpcError string) {
	t.Helper()
	response := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcError != "" {
		response["error"] = map[string]any{"code": -32004, "message": rpcError}
	} else {
		response["result"] = result
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case conn.reads <- data:
	case <-time.After(time.Second):
		t.Fatal("timed out sending exec-server response")
	}
}

func metadataResult(size int64) FSGetMetadataResponse {
	return FSGetMetadataResponse{IsFile: true, Size: size, CreatedAtMS: 10, ModifiedAtMS: 20}
}

func TestClientCoalescesOnlyConcurrentUnsandboxedMetadataLikeRust(t *testing.T) {
	client, conn := newMetadataTestClient(t)
	path := "file:///workspace/project/AGENTS.md"
	type outcome struct {
		response *FSGetMetadataResponse
		err      error
	}
	firstDone := make(chan outcome, 1)
	secondDone := make(chan outcome, 1)
	go func() {
		response, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		firstDone <- outcome{response: response, err: err}
	}()
	request := nextMetadataWireRequest(t, conn)
	go func() {
		response, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		secondDone <- outcome{response: response, err: err}
	}()
	select {
	case duplicate := <-conn.writes:
		t.Fatalf("concurrent metadata request was not coalesced: %s", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
	respondMetadataWire(t, conn, request.ID, metadataResult(42), "")
	for name, done := range map[string]<-chan outcome{"first": firstDone, "second": secondDone} {
		result := <-done
		if result.err != nil || result.response == nil || result.response.Size != 42 {
			t.Fatalf("%s result = %#v, %v", name, result.response, result.err)
		}
	}

	retryDone := make(chan outcome, 1)
	go func() {
		response, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		retryDone <- outcome{response: response, err: err}
	}()
	retry := nextMetadataWireRequest(t, conn)
	respondMetadataWire(t, conn, retry.ID, metadataResult(43), "")
	if result := <-retryDone; result.err != nil || result.response.Size != 43 {
		t.Fatalf("post-completion metadata result = %#v, %v", result.response, result.err)
	}

	sandbox := &FileSystemSandboxContext{CWD: "file:///workspace/project"}
	for i := 0; i < 2; i++ {
		go func() {
			response, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path, Sandbox: sandbox})
			secondDone <- outcome{response: response, err: err}
		}()
	}
	for i := 0; i < 2; i++ {
		sandboxed := nextMetadataWireRequest(t, conn)
		respondMetadataWire(t, conn, sandboxed.ID, metadataResult(44), "")
	}
	for i := 0; i < 2; i++ {
		if result := <-secondDone; result.err != nil || result.response.Size != 44 {
			t.Fatalf("sandboxed metadata result = %#v, %v", result.response, result.err)
		}
	}
}

func TestClientMetadataErrorIsSharedOnlyInFlightLikeRust(t *testing.T) {
	client, conn := newMetadataTestClient(t)
	path := "file:///workspace/project/missing.md"
	errorsDone := make(chan error, 2)
	go func() {
		_, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		errorsDone <- err
	}()
	request := nextMetadataWireRequest(t, conn)
	go func() {
		_, err := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		errorsDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	respondMetadataWire(t, conn, request.ID, nil, "metadata not found")
	for i := 0; i < 2; i++ {
		if err := <-errorsDone; err == nil || !strings.Contains(err.Error(), "metadata not found") {
			t.Fatalf("shared metadata error = %v", err)
		}
	}

	retryDone := make(chan *FSGetMetadataResponse, 1)
	go func() {
		response, _ := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		retryDone <- response
	}()
	retry := nextMetadataWireRequest(t, conn)
	respondMetadataWire(t, conn, retry.ID, metadataResult(42), "")
	if response := <-retryDone; response == nil || response.Size != 42 {
		t.Fatalf("retried metadata response = %#v", response)
	}
}

func TestClientMetadataFollowerRetriesCancelledInitializerLikeRust(t *testing.T) {
	client, conn := newMetadataTestClient(t)
	path := "file:///workspace/project/AGENTS.md"
	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerDone := make(chan error, 1)
	go func() {
		_, err := client.FSGetMetadata(ownerCtx, &FSGetMetadataParams{Path: path})
		ownerDone <- err
	}()
	_ = nextMetadataWireRequest(t, conn)

	followerDone := make(chan *FSGetMetadataResponse, 1)
	go func() {
		response, _ := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
		followerDone <- response
	}()
	time.Sleep(20 * time.Millisecond)
	cancelOwner()
	if err := <-ownerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("initializer error = %v", err)
	}
	retry := nextMetadataWireRequest(t, conn)
	respondMetadataWire(t, conn, retry.ID, metadataResult(43), "")
	if response := <-followerDone; response == nil || response.Size != 43 {
		t.Fatalf("follower response = %#v", response)
	}
}

func TestClientMutationInvalidatesInFlightMetadataEvenOnErrorLikeRust(t *testing.T) {
	mutations := []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{"write", func(ctx context.Context, client *Client) error {
			_, err := client.FSWriteFile(ctx, &FSWriteFileParams{Path: "file:///workspace/project/AGENTS.md"})
			return err
		}},
		{"create_directory", func(ctx context.Context, client *Client) error {
			_, err := client.FSCreateDirectory(ctx, &FSCreateDirectoryParams{Path: "file:///workspace/project"})
			return err
		}},
		{"remove", func(ctx context.Context, client *Client) error {
			_, err := client.FSRemove(ctx, &FSRemoveParams{Path: "file:///workspace/project/AGENTS.md"})
			return err
		}},
		{"copy_error", func(ctx context.Context, client *Client) error {
			_, err := client.FSCopy(ctx, &FSCopyParams{SourcePath: "file:///workspace/project/source.md", DestinationPath: "file:///workspace/project/AGENTS.md"})
			return err
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			client, conn := newMetadataTestClient(t)
			path := "file:///workspace/project/AGENTS.md"
			staleDone := make(chan *FSGetMetadataResponse, 1)
			go func() {
				response, _ := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
				staleDone <- response
			}()
			staleRequest := nextMetadataWireRequest(t, conn)

			mutationDone := make(chan error, 1)
			go func() { mutationDone <- mutation.call(context.Background(), client) }()
			mutationRequest := nextMetadataWireRequest(t, conn)
			mutationError := ""
			if mutation.name == "copy_error" {
				mutationError = "mutation partially failed"
			}
			respondMetadataWire(t, conn, mutationRequest.ID, struct{}{}, mutationError)
			if err := <-mutationDone; (mutationError == "") != (err == nil) {
				t.Fatalf("mutation error = %v", err)
			}

			freshDone := make(chan *FSGetMetadataResponse, 1)
			go func() {
				response, _ := client.FSGetMetadata(context.Background(), &FSGetMetadataParams{Path: path})
				freshDone <- response
			}()
			freshRequest := nextMetadataWireRequest(t, conn)
			respondMetadataWire(t, conn, staleRequest.ID, metadataResult(42), "")
			respondMetadataWire(t, conn, freshRequest.ID, metadataResult(43), "")
			if stale := <-staleDone; stale == nil || stale.Size != 42 {
				t.Fatalf("stale response = %#v", stale)
			}
			if fresh := <-freshDone; fresh == nil || fresh.Size != 43 {
				t.Fatalf("fresh response = %#v", fresh)
			}
		})
	}
}
