package cloudclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestThreadListRequestPreservesPaginationAndOrderOnEachRequestLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	responses := [][]byte{
		[]byte("\x0a\x00\x12\x04next\x1a\x04back"),
		[]byte("\x0a\x00"),
	}
	var mu sync.Mutex
	var requests [][]byte
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "oaiproto.codex_app_server_backend.ThreadService",
		HandlerType: (*testThreadService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "ListThreads",
			Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				var message rawMessage
				if err := decode(&message); err != nil {
					return nil, err
				}
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, append([]byte(nil), message...))
				index := len(requests) - 1
				if index >= len(responses) {
					return nil, errors.New("unexpected ListThreads request")
				}
				return rawMessage(responses[index]), nil
			},
		}},
	}, &struct{}{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	client, err := New("http://"+listener.Addr().String(), Credentials{BearerToken: "token", AccountID: "account"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor := "cursor"
	limit := uint32(7)
	query := ThreadListQuery{
		Cursor: &cursor, Limit: &limit, SortKey: ThreadSortUpdatedAt,
		SortDirection: ThreadSortAscending, Originators: []string{"tui"},
	}
	page, err := client.ListThreads(ctx, query)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(page.Data) != 0 || page.NextCursor == nil || *page.NextCursor != "next" ||
		page.BackwardsCursor == nil || *page.BackwardsCursor != "back" || page.ReadStates != nil {
		t.Fatalf("page = %#v", page)
	}
	query.Cursor = page.NextCursor
	last, err := client.ListThreads(ctx, query)
	if err != nil {
		t.Fatalf("ListThreads(next) error = %v", err)
	}
	if len(last.Data) != 0 || last.NextCursor != nil {
		t.Fatalf("next page = %#v", last)
	}

	wantRequests := [][]byte{
		[]byte("\x0a\x06cursor\x10\x07\x18\x02\x20\x01\x28\x00\x32\x05\x0a\x03tui"),
		[]byte("\x0a\x04next\x10\x07\x18\x02\x20\x01\x28\x00\x32\x05\x0a\x03tui"),
	}
	mu.Lock()
	gotRequests := append([][]byte(nil), requests...)
	mu.Unlock()
	if !reflect.DeepEqual(gotRequests, wantRequests) {
		t.Fatalf("requests = %x, want %x", gotRequests, wantRequests)
	}
}

func TestThreadListDefaultQueryEncodesExplicitDefaultsLikeRust(t *testing.T) {
	want := []byte("\x18\x01\x20\x02\x28\x00\x32\x00")
	if got := encodeThreadListRequest(ThreadListQuery{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("default ListThreads request = %x, want %x", got, want)
	}
}

func TestDecodeThreadListPreservesNativeSnapshotsAndReadStateLikeRust(t *testing.T) {
	thread := []byte("\x22\x01v\x28\x01\x32\x01/\x48\x00\x72\x01t\x82\x01\x01p\xaa\x01\x00\xda\x01\x01s\xe2\x01\x02\x08\x63\xea\x01\x02\x12\x00\xfa\x01\x00\x80\x02\x02\x98\x06\x01")
	legacy := []byte("\x22\x00\x28\x00\x32\x01/\x48\x00\x72\x06legacy\x82\x01\x00\xaa\x01\x00\xda\x01\x00\xe2\x01\x02\x08\x01\xea\x01\x02\x12\x00\xfa\x01\x00\x80\x02\x00")

	var data []byte
	for _, row := range [][]byte{thread, legacy, thread} {
		data = appendActivationBytes(data, 1, row)
	}
	payload := appendActivationBytes(nil, 1, data)
	payload = append(payload, []byte("\x22\x0e\x0a\x0c\x0a\x01t\x12\x07\x0a\x02\x0a\x00\x12\x01r")...)

	page, err := decodeThreadListPage(payload)
	if err != nil {
		t.Fatalf("decodeThreadListPage() error = %v", err)
	}
	got := make([][]byte, 0, len(page.Data))
	for _, snapshot := range page.Data {
		got = append(got, snapshot.Proto())
	}
	if !reflect.DeepEqual(got, [][]byte{thread, legacy, thread}) {
		t.Fatalf("snapshot protobufs = %x", got)
	}
	legacyThread := page.Data[1]
	if legacyThread.ID != "legacy" || legacyThread.SessionID != "" ||
		legacyThread.ModelProvider != "" || legacyThread.CLIVersion != "" {
		t.Fatalf("legacy snapshot = %#v", legacyThread)
	}
	if !reflect.DeepEqual(page.Data[0].Status, map[string]any{"type": "idle"}) {
		t.Fatalf("status = %#v", page.Data[0].Status)
	}
	wantStates := map[string]ThreadReadState{
		"t": {FirstUnread: &ThreadUnreadPosition{Type: ThreadUnreadThreadStart}, Revision: "r"},
	}
	if !reflect.DeepEqual(page.ReadStates, wantStates) {
		t.Fatalf("read states = %#v, want %#v", page.ReadStates, wantStates)
	}

	data, err = json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	state := decoded["readStates"].(map[string]any)["t"].(map[string]any)
	unread := state["firstUnread"].(map[string]any)
	if unread["type"] != ThreadUnreadThreadStart || state["revision"] != "r" {
		t.Fatalf("page JSON = %s", data)
	}
}

func TestDecodeThreadListRejectsInvalidResponsesLikeRust(t *testing.T) {
	invalid := [][]byte{
		nil,
		[]byte("\x0a\x02\x0a\x00"),
		[]byte("\x0a\x00\x22\x07\x0a\x05\x0a\x01t\x12\x00"),
	}
	for index, payload := range invalid {
		if _, err := decodeThreadListPage(payload); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("case %d error = %v, want ErrInvalidResponse", index, err)
		}
	}
}

func TestDecodeThreadReadStateRejectsIncompleteUnreadPositionLikeRust(t *testing.T) {
	var missingVariant []byte
	missingVariant = appendActivationString(missingVariant, 2, "r")
	missingVariant = appendActivationBytes(missingVariant, 1, nil)

	position := appendActivationBytes(nil, 2, appendActivationString(nil, 1, ""))
	var emptyTurn []byte
	emptyTurn = appendActivationString(emptyTurn, 2, "r")
	emptyTurn = appendActivationBytes(emptyTurn, 1, position)

	for index, state := range [][]byte{missingVariant, emptyTurn} {
		var entry []byte
		entry = appendActivationString(entry, 1, "t")
		entry = appendActivationBytes(entry, 2, state)
		mapBytes := appendActivationBytes(nil, 1, entry)
		payload := appendActivationBytes(nil, 1, nil)
		payload = appendActivationBytes(payload, 4, mapBytes)
		if _, err := decodeThreadListPage(payload); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("case %d error = %v, want ErrInvalidResponse", index, err)
		}
	}
}

func TestDecodeThreadReadStateAcceptsTurnPositionLikeRust(t *testing.T) {
	position := appendActivationBytes(nil, 2, appendActivationString(nil, 1, "turn-1"))
	var state []byte
	state = appendActivationString(state, 2, "revision-1")
	state = appendActivationBytes(state, 1, position)

	var entry []byte
	entry = appendActivationString(entry, 1, "thread-1")
	entry = appendActivationBytes(entry, 2, state)
	mapBytes := appendActivationBytes(nil, 1, entry)
	payload := appendActivationBytes(nil, 1, nil)
	payload = appendActivationBytes(payload, 4, mapBytes)

	page, err := decodeThreadListPage(payload)
	if err != nil {
		t.Fatalf("decodeThreadListPage() error = %v", err)
	}
	want := &ThreadUnreadPosition{Type: ThreadUnreadTurn, TurnID: "turn-1"}
	if state := page.ReadStates["thread-1"]; !reflect.DeepEqual(state.FirstUnread, want) || state.Revision != "revision-1" {
		t.Fatalf("read state = %#v", state)
	}
}

func TestThreadPageJSONUsesRustCamelCase(t *testing.T) {
	data, err := json.Marshal(ThreadPage{Data: []ThreadSnapshot{}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data":[],"readStates":null,"nextCursor":null,"backwardsCursor":null}`
	if string(data) != want {
		t.Fatalf("ThreadPage JSON = %s, want %s", data, want)
	}
}

func TestThreadSortDirectionDefaultsToDescendingLikeRust(t *testing.T) {
	if got := threadSortDirectionValue(ThreadSortDirection(99)); got != 2 {
		t.Fatalf("unknown direction = %d, want descending", got)
	}
	if got := threadSortKeyValue(ThreadSortKey(99)); got != 1 {
		t.Fatalf("unknown sort key = %d, want created-at", got)
	}
}
