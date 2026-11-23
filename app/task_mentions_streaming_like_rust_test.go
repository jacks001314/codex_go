package app

import (
	"context"
	"encoding/json"
	"errors"
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

func taskMentionStringPtr(value string) *string { return &value }

func rpcResult(id json.RawMessage, result map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

// Rust #62691: task_mentions issues two thread/list requests on one app-server
// request handle, so replies may arrive in either order.
func TestRemoteSessionRequestBatchDispatchesOutOfOrderRepliesLikeRust(t *testing.T) {
	var transport *scriptedAppServerTransport
	transport = newScriptedAppServerTransport(func(request scriptedAppServerRequest, reply func(map[string]any)) {
		if request.Method != string(appserver.MethodThreadList) {
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
				"data":       []any{map[string]any{"id": "filtered", "name": "Filtered task"}},
				"nextCursor": nil,
			}))
			reply(rpcResult(requests[0].ID, map[string]any{
				"data":       []any{map[string]any{"id": "recent", "name": "Recent task"}},
				"nextCursor": nil,
			}))
		}()
	})
	client := &remoteAppServerTUIClient{transport: transport}
	var recent appserver.ThreadListResponse
	var filtered appserver.ThreadListResponse
	outcomes := remoteSessionRequestBatch(context.Background(), client, []remoteRequestSpec{
		{method: appserver.MethodThreadList, params: appserver.ThreadListParams{}, target: &recent},
		{method: appserver.MethodThreadList, params: appserver.ThreadListParams{SearchTerm: taskMentionStringPtr("q")}, target: &filtered},
	})
	if len(outcomes) != 2 || !outcomes[0].settled || !outcomes[1].settled {
		t.Fatalf("outcomes = %#v, want both settled", outcomes)
	}
	if outcomes[0].err != nil || outcomes[1].err != nil {
		t.Fatalf("outcome errors = %v / %v", outcomes[0].err, outcomes[1].err)
	}
	if len(recent.Data) != 1 || recent.Data[0].ID != "recent" {
		t.Fatalf("recent response = %#v", recent)
	}
	if len(filtered.Data) != 1 || filtered.Data[0].ID != "filtered" {
		t.Fatalf("filtered response = %#v", filtered)
	}
}

// Rust #62691: when the title-filtered request is abandoned, the recent list
// that already arrived still feeds the mention popup.
func TestRemoteTaskMentionSearchRunnerKeepsRecentWhenTitleFilterStallsLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := newScriptedAppServerTransport(func(request scriptedAppServerRequest, reply func(map[string]any)) {
		if request.Method != string(appserver.MethodThreadList) {
			return
		}
		var params appserver.ThreadListParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Errorf("thread/list params = %s: %v", request.Params, err)
			return
		}
		if params.SearchTerm != nil {
			// The title-filtered page never answers: the caller abandons it.
			return
		}
		reply(rpcResult(request.ID, map[string]any{
			"data":       []any{map[string]any{"id": "recent", "name": "Recent task", "cwd": "/repo"}},
			"nextCursor": nil,
		}))
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	run := remoteTaskMentionSearchRunner(&remoteAppServerTUIClient{transport: transport}, false)
	matches := searchTaskMentionsWithRunner(ctx, run, "query", "self", "/repo")
	if len(matches) != 1 || matches[0].ThreadID != "recent" {
		t.Fatalf("matches = %#v, want the recent list only", matches)
	}
}

// Rust #62691: the hosted Cloud backend has no title filter, so only the recent
// list is requested.
func TestRemoteTaskMentionSearchRunnerSkipsTitleFilterOnCloudLikeRust(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	transport := newScriptedAppServerTransport(func(request scriptedAppServerRequest, reply func(map[string]any)) {
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		reply(rpcResult(request.ID, map[string]any{
			"data":       []any{map[string]any{"id": "recent", "name": "Recent task"}},
			"nextCursor": nil,
		}))
	})
	run := remoteTaskMentionSearchRunner(&remoteAppServerTUIClient{transport: transport}, true)
	recent, recentErr, _, filteredErr := run(context.Background(), appserver.ThreadListParams{}, appserver.ThreadListParams{SearchTerm: taskMentionStringPtr("query")})
	if recentErr != nil || len(recent.Data) != 1 {
		t.Fatalf("cloud recent list = %#v / %v", recent, recentErr)
	}
	if !errors.Is(filteredErr, errTaskMentionCloudTitleFilterUnsupported) {
		t.Fatalf("cloud filtered error = %v", filteredErr)
	}
	mu.Lock()
	issued := append([]string(nil), methods...)
	mu.Unlock()
	if len(issued) != 1 || issued[0] != string(appserver.MethodThreadList) {
		t.Fatalf("cloud requests = %#v, want one recent thread/list", issued)
	}
}
