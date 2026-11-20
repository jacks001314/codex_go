package app

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"codex_go/appserver"
)

// scriptedAppServerTransport answers typed requests from a script so a test can
// interleave replies, keep a search in flight, and observe cancellation.
type scriptedAppServerTransport struct {
	mu        sync.Mutex
	requests  []scriptedAppServerRequest
	reads     chan []byte
	handle    func(request scriptedAppServerRequest, reply func(map[string]any))
	muDeliver sync.Mutex
	delivered int
	afterRead func(count int)
}

type scriptedAppServerRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func newScriptedAppServerTransport(handle func(scriptedAppServerRequest, func(map[string]any))) *scriptedAppServerTransport {
	return &scriptedAppServerTransport{reads: make(chan []byte, 8), handle: handle}
}

func (t *scriptedAppServerTransport) read(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.reads:
		t.muDeliver.Lock()
		t.delivered++
		count := t.delivered
		after := t.afterRead
		t.muDeliver.Unlock()
		if after != nil {
			after(count)
		}
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *scriptedAppServerTransport) write(_ context.Context, data []byte) error {
	var request scriptedAppServerRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	t.mu.Lock()
	t.requests = append(t.requests, request)
	handle := t.handle
	t.mu.Unlock()
	if handle != nil {
		handle(request, t.reply)
	}
	return nil
}

func (t *scriptedAppServerTransport) close() {}

func (t *scriptedAppServerTransport) reply(message map[string]any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return
	}
	t.reads <- encoded
}

func (t *scriptedAppServerTransport) observed() []scriptedAppServerRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]scriptedAppServerRequest(nil), t.requests...)
}

func (t *scriptedAppServerTransport) setAfterRead(after func(count int)) {
	t.muDeliver.Lock()
	defer t.muDeliver.Unlock()
	t.afterRead = after
}

func rpcResult(id json.RawMessage, result map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

// Rust #62132: task_mentions joins thread/search with thread/list on one
// app-server request handle, so replies may arrive in either order.
func TestRemoteSessionRequestBatchDispatchesOutOfOrderRepliesLikeRust(t *testing.T) {
	var transport *scriptedAppServerTransport
	transport = newScriptedAppServerTransport(func(request scriptedAppServerRequest, reply func(map[string]any)) {
		if request.Method != string(appserver.MethodThreadSearch) {
			return
		}
		go func() {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if len(transport.observed()) >= 2 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			requests := transport.observed()
			reply(rpcResult(requests[1].ID, map[string]any{
				"data":       []any{map[string]any{"id": "listed", "name": "Listed task"}},
				"nextCursor": nil,
			}))
			reply(rpcResult(requests[0].ID, map[string]any{
				"data": []any{map[string]any{
					"thread":  map[string]any{"id": "searched"},
					"snippet": "hit",
				}},
				"nextCursor": nil,
			}))
		}()
	})
	client := &remoteAppServerTUIClient{transport: transport}
	var search appserver.ThreadSearchResponse
	var listed appserver.ThreadListResponse
	outcomes := remoteSessionRequestBatch(context.Background(), client, []remoteRequestSpec{
		{method: appserver.MethodThreadSearch, params: appserver.ThreadSearchParams{SearchTerm: "q"}, target: &search},
		{method: appserver.MethodThreadList, params: appserver.ThreadListParams{}, target: &listed},
	})
	if len(outcomes) != 2 || !outcomes[0].settled || !outcomes[1].settled {
		t.Fatalf("outcomes = %#v, want both settled", outcomes)
	}
	if outcomes[0].err != nil || outcomes[1].err != nil {
		t.Fatalf("outcome errors = %v / %v", outcomes[0].err, outcomes[1].err)
	}
	if len(search.Data) != 1 || search.Data[0].Thread.ID != "searched" || search.Data[0].Snippet != "hit" {
		t.Fatalf("search response = %#v", search)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != "listed" {
		t.Fatalf("list response = %#v", listed)
	}
}

// Rust #62132: a superseded or timed-out remote task search is cancelled
// instead of being left to run out its own budget, and the title listing that
// already arrived is still used.
func TestRemoteTaskMentionSearchCancelsAbandonedSearchLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var searchID int64
	transport := newScriptedAppServerTransport(func(request scriptedAppServerRequest, reply func(map[string]any)) {
		switch request.Method {
		case string(appserver.MethodThreadList):
			reply(rpcResult(request.ID, map[string]any{
				"data":       []any{map[string]any{"id": "listed", "name": "Listed task", "cwd": "/repo"}},
				"nextCursor": nil,
			}))
		case string(appserver.MethodThreadSearch):
			id, _ := strconv.ParseInt(string(request.ID), 10, 64)
			mu.Lock()
			searchID = id
			mu.Unlock()
			// The search never answers: the caller abandons it.
		case string(appserver.MethodThreadSearchCancel):
			var params appserver.ThreadSearchCancelParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("cancel params = %s: %v", request.Params, err)
			}
			mu.Lock()
			want := searchID
			mu.Unlock()
			if params.RequestID.String() != strconv.FormatInt(want, 10) {
				t.Errorf("cancel requestId = %q, want %d", params.RequestID.String(), want)
			}
			reply(rpcResult(request.ID, map[string]any{}))
		}
	})
	transport.setAfterRead(func(count int) {
		if count == 1 {
			cancel()
		}
	})
	detached := false
	run := remoteTaskMentionSearchRunner(&remoteAppServerTUIClient{transport: transport}, false, func() { detached = true })
	_, searchErr, listed, listErr := run(ctx, appserver.ThreadSearchParams{SearchTerm: "q"}, appserver.ThreadListParams{})
	if searchErr == nil {
		t.Fatal("abandoned search reported success")
	}
	if listErr != nil {
		t.Fatalf("title listing error = %v", listErr)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != "listed" {
		t.Fatalf("title listing = %#v, want the already-available match", listed)
	}
	if !detached {
		t.Fatal("abandoned search kept the connection open")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, request := range transport.observed() {
			if request.Method == string(appserver.MethodThreadSearchCancel) {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("abandoned search was not cancelled")
}
