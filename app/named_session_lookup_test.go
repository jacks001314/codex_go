package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
)

// namedLookupServer serves scripted thread/list pages and thread/read results.
type namedLookupServer struct {
	listPages  map[string][]any // cursor ("": first) -> thread list data
	nextBy     map[string]any   // cursor -> nextCursor (nil for the last page)
	listErrs   map[string]string
	listParams *[]appserver.ThreadListParams
	reads      map[string]any // thread id -> thread/read result (thread map)
	readErrs   map[string]string
}

func newNamedLookupEndpoint(t *testing.T, srv namedLookupServer) (*appserverdaemon.RemoteAppServerEndpoint, func()) {
	t.Helper()
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadList):
				var params appserver.ThreadListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Errorf("thread/list params: %v", err)
					return
				}
				cursor := ""
				if params.Cursor != nil {
					cursor = *params.Cursor
				}
				if srv.listParams != nil {
					*srv.listParams = append(*srv.listParams, params)
				}
				if message, ok := srv.listErrs[cursor]; ok {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error":   map[string]any{"code": -32603, "message": message},
					})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"data":       srv.listPages[cursor],
						"nextCursor": srv.nextBy[cursor],
					},
				})
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					t.Errorf("thread/read params: %v", err)
					return
				}
				if message, ok := srv.readErrs[params.ThreadID]; ok {
					remoteTUITestWrite(ctx, conn, map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error":   map[string]any{"code": -32603, "message": message},
					})
					continue
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": srv.reads[params.ThreadID]},
				})
			default:
				t.Errorf("unexpected method %s", req.Method)
				return
			}
		}
	}))
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	return endpoint, server.Close
}

func lookupNamedSession(t *testing.T, srv namedLookupServer, name string, collections []sessionCollection) (string, error) {
	t.Helper()
	endpoint, closeServer := newNamedLookupEndpoint(t, srv)
	defer closeServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.close()
	thread, err := lookupRemoteSessionByName(ctx, client, name, collections, &cli.SessionOptions{})
	if err != nil {
		return "", err
	}
	if thread == nil {
		return "", nil
	}
	return thread.ID, nil
}

func TestLookupRemoteSessionByNameMatchesPreviewText(t *testing.T) {
	srv := namedLookupServer{
		listPages: map[string][]any{"": {
			map[string]any{"id": "thread-1", "preview": "fix the parser", "recencyAt": 10},
		}},
		nextBy: map[string]any{"": nil},
		reads: map[string]any{
			"thread-1": map[string]any{"id": "thread-1", "preview": "fix the parser"},
		},
	}
	threadID, err := lookupNamedSession(t, srv, "fix the parser", []sessionCollection{sessionCollectionActive})
	if err != nil || threadID != "thread-1" {
		t.Fatalf("lookup = %q err=%v", threadID, err)
	}
}

func TestLookupRemoteSessionByNameRejectsDistinctMatches(t *testing.T) {
	srv := namedLookupServer{
		listPages: map[string][]any{"": {
			map[string]any{"id": "thread-a", "name": "Duplicate", "recencyAt": 20},
			map[string]any{"id": "thread-b", "name": "Duplicate", "recencyAt": 10},
		}},
		nextBy: map[string]any{"": nil},
		reads: map[string]any{
			"thread-a": map[string]any{"id": "thread-a", "name": "Duplicate"},
			"thread-b": map[string]any{"id": "thread-b", "name": "Duplicate"},
		},
	}
	_, err := lookupNamedSession(t, srv, "Duplicate", []sessionCollection{sessionCollectionActive})
	if err == nil || err.Error() != "Multiple sessions match 'Duplicate' (including thread-a and thread-b); use a session UUID to disambiguate." {
		t.Fatalf("err = %v", err)
	}
}

func TestLookupRemoteSessionByNameRequiresUUIDWhenPaginated(t *testing.T) {
	srv := namedLookupServer{
		listPages: map[string][]any{
			"":   {map[string]any{"id": "thread-a", "name": "Solo", "recencyAt": 20}},
			"c1": {},
		},
		nextBy: map[string]any{"": "c1", "c1": nil},
		reads: map[string]any{
			"thread-a": map[string]any{"id": "thread-a", "name": "Solo"},
		},
	}
	_, err := lookupNamedSession(t, srv, "Solo", []sessionCollection{sessionCollectionActive})
	want := "Cannot verify a unique session label across server pages; matching session UUID: thread-a. Use it only if this is the session you want."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
}

func TestLookupRemoteSessionByNameAcceptsVerifiedCursorPagesLikeRust(t *testing.T) {
	var listParams []appserver.ThreadListParams
	srv := namedLookupServer{
		listPages: map[string][]any{
			"": {
				map[string]any{"id": "thread-a", "name": "Solo", "createdAt": 20},
			},
			"2025-01-01T00:00:00.000Z|thread-a": {},
		},
		nextBy: map[string]any{
			"":                                  "2025-01-01T00:00:00.000Z|thread-a",
			"2025-01-01T00:00:00.000Z|thread-a": nil,
		},
		listParams: &listParams,
		reads: map[string]any{
			"thread-a": map[string]any{"id": "thread-a", "name": "Solo"},
		},
	}
	threadID, err := lookupNamedSession(t, srv, "Solo", []sessionCollection{sessionCollectionActive})
	if err != nil || threadID != "thread-a" {
		t.Fatalf("lookup = %q err=%v", threadID, err)
	}
	if len(listParams) != 2 {
		t.Fatalf("thread/list calls = %d, want 2", len(listParams))
	}
	for _, params := range listParams {
		if params.SortKey != appserver.SortCreatedAt || !params.UseStateDBOnly {
			t.Fatalf("thread/list params = %#v, want CreatedAt and state-db-only", params)
		}
	}
}

func TestLookupRemoteSessionByNamePropagatesListingFailureLikeRust(t *testing.T) {
	srv := namedLookupServer{
		listErrs: map[string]string{"": "failed to list threads from state database"},
	}
	_, err := lookupNamedSession(t, srv, "Any", []sessionCollection{sessionCollectionActive})
	if err == nil || !strings.Contains(err.Error(), "failed to list sessions while resolving session label") ||
		!strings.Contains(err.Error(), "failed to list threads from state database") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifiedNamedSessionLookupCursor(t *testing.T) {
	for _, testCase := range []struct {
		cursor string
		want   bool
	}{
		{cursor: "c1", want: false},
		{cursor: "2025-01-01T00:00:00Z", want: false},
		{cursor: "2025-01-01T00:00:00Z|thread-a", want: true},
		{cursor: "42|thread-a", want: true},
		{cursor: "not-a-time|thread-a", want: false},
		{cursor: "2025-01-01T00:00:00Z|", want: false},
	} {
		if got := verifiedNamedSessionLookupCursor(testCase.cursor); got != testCase.want {
			t.Errorf("verifiedNamedSessionLookupCursor(%q) = %v, want %v", testCase.cursor, got, testCase.want)
		}
	}
}

func TestLookupRemoteSessionByNameSkipsRenamedCandidates(t *testing.T) {
	srv := namedLookupServer{
		listPages: map[string][]any{"": {
			map[string]any{"id": "thread-a", "name": "Old label", "recencyAt": 20},
		}},
		nextBy: map[string]any{"": nil},
		reads: map[string]any{
			"thread-a": map[string]any{"id": "thread-a", "name": "New label"},
		},
	}
	threadID, err := lookupNamedSession(t, srv, "Old label", []sessionCollection{sessionCollectionActive})
	if err != nil || threadID != "" {
		t.Fatalf("lookup = %q err=%v, want no match", threadID, err)
	}
}

func TestLookupRemoteSessionByNameKeepsUnloadedThreadOnOlderServer(t *testing.T) {
	srv := namedLookupServer{
		listPages: map[string][]any{"": {
			map[string]any{"id": "thread-a", "name": "Kept", "recencyAt": 20},
		}},
		nextBy:   map[string]any{"": nil},
		readErrs: map[string]string{"thread-a": "thread not loaded: thread-a"},
	}
	threadID, err := lookupNamedSession(t, srv, "Kept", []sessionCollection{sessionCollectionActive})
	if err != nil || threadID != "thread-a" {
		t.Fatalf("lookup = %q err=%v", threadID, err)
	}
}
