package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const remoteTestToken = "0123456789abcdef0123456789abcdef"

// recordedRemoteRequest is one request a mock board service received.
type recordedRemoteRequest struct {
	method string
	path   string
	auth   string
	body   []byte
}

// rawNotificationBoardHandler streams an exact SSE body.
func rawNotificationBoardHandler(body string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(body))
		writer.(http.Flusher).Flush()
	}
}

// Rust #48190: the frame bound is per frame, not per connection, and LF, CR and
// CRLF line endings all end a frame, so a long heartbeat sequence still delivers.
func TestRemoteBoardNotificationFrameBoundIsPerFrameLikeRust(t *testing.T) {
	heartbeats := strings.Repeat(": heartbeat\r\n\r\n: heartbeat\r\r: heartbeat\n\n", 16*1024)
	notice := `{"recipient":"thread-1","turn_id":"turn-1","post":{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}}`
	httpServer := httptest.NewServer(rawNotificationBoardHandler(
		heartbeats + "event: ready\ndata: {}\n\n" + "event: notification\ndata: " + notice + "\n\n"))
	defer httpServer.Close()

	receiver, err := newTestRemoteBoard(t, httpServer.URL, nil).Notifications(context.Background(), "thread-1", "turn-1")
	if err != nil {
		t.Fatalf("Notifications() error = %v", err)
	}
	defer receiver.Close()
	got, err := receiver.Next(context.Background())
	if err != nil || got == nil || got.Post.MessageID != "m1" {
		t.Fatalf("Next() = %#v/%v, want the notification after the heartbeats", got, err)
	}
}

// Rust #48190: an oversized readiness field, an unterminated trailing field and
// an oversized multiline data frame are all refused before parsing, so the
// parser never buffers the payload.
func TestRemoteBoardNotificationFrameOversizeLikeRust(t *testing.T) {
	oversized := strings.Repeat("x", remoteBoardMaxBody)
	cases := []struct {
		name string
		body string
	}{
		{"oversized readiness field", "event: ready\nid: " + oversized + "\ndata: {}\n\n"},
		{"unterminated field", "event: ready\ndata: {}\n\nid: " + oversized},
		{"multiline data frame", "event: ready\ndata: {}\n\n" + strings.Repeat("data: x\n", 64*1024+1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			httpServer := httptest.NewServer(rawNotificationBoardHandler(testCase.body))
			defer httpServer.Close()
			receiver, err := newTestRemoteBoard(t, httpServer.URL, nil).Notifications(context.Background(), "thread-1", "turn-1")
			if err == nil {
				_, err = receiver.Next(context.Background())
				receiver.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "board SSE frame exceeds the service limit") {
				t.Fatalf("oversized frame error = %v, want the service-limit failure", err)
			}
		})
	}
}

// Rust #48190: malformed UTF-8 is rejected incrementally instead of being
// retained by the decoder awaiting more input.
func TestRemoteBoardNotificationRejectsMalformedUTF8LikeRust(t *testing.T) {
	for _, body := range []string{
		"event: ready\ndata: \xff\xfe\n\n",
		"event: ready\ndata: {}\n\n" + "event: notification\ndata: \xc3\x28\n\n",
	} {
		httpServer := httptest.NewServer(rawNotificationBoardHandler(body))
		receiver, err := newTestRemoteBoard(t, httpServer.URL, nil).Notifications(context.Background(), "thread-1", "turn-1")
		if err == nil {
			_, err = receiver.Next(context.Background())
			receiver.Close()
		}
		httpServer.Close()
		if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
			t.Fatalf("malformed UTF-8 error = %v, want an encoding failure", err)
		}
	}
}

// remoteBoardRecorder records the requests a mock service saw.
type remoteBoardRecorder struct {
	mu       sync.Mutex
	requests []recordedRemoteRequest
}

func (r *remoteBoardRecorder) record(request *http.Request) []byte {
	body, _ := io.ReadAll(request.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, recordedRemoteRequest{
		method: request.Method,
		path:   request.URL.Path,
		auth:   request.Header.Get("Authorization"),
		body:   append([]byte(nil), body...),
	})
	return body
}

func (r *remoteBoardRecorder) last() recordedRemoteRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return recordedRemoteRequest{}
	}
	return r.requests[len(r.requests)-1]
}

// fakeBoardClock is a caller-supplied board clock.
type fakeBoardClock struct {
	mu    sync.Mutex
	err   error
	value time.Time
}

func (c *fakeBoardClock) now(string) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return time.Time{}, c.err
	}
	return c.value, nil
}

func newTestRemoteBoard(t *testing.T, endpoint string, clock func(string) (time.Time, error)) *RemoteBoard {
	t.Helper()
	board, err := NewRemoteBoard(RemoteBoardOptions{
		HTTP:     http.DefaultClient,
		Endpoint: endpoint,
		Board:    "session-1",
		Token:    remoteTestToken,
		Clock:    clock,
	})
	if err != nil {
		t.Fatalf("NewRemoteBoard() error = %v", err)
	}
	return board
}

// Rust's client validates the endpoint before use: HTTP(S) only, no query, no
// fragment, and the board path appended to the endpoint's own prefix.
func TestRemoteBoardValidatesEndpointAndCredentialLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		board    string
		token    string
	}{
		{"scheme", "ftp://host/board", "session-1", remoteTestToken},
		{"missing host", "https:///board", "session-1", remoteTestToken},
		{"query", "https://host/board?x=1", "session-1", remoteTestToken},
		{"fragment", "https://host/board#x", "session-1", remoteTestToken},
		{"short credential", "https://host/board", "session-1", "too-short"},
		{"whitespace credential", "https://host/board", "session-1", strings.Repeat("a", 31) + " "},
		{"missing board", "https://host/board", "  ", remoteTestToken},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewRemoteBoard(RemoteBoardOptions{
				HTTP: http.DefaultClient, Endpoint: testCase.endpoint, Board: testCase.board, Token: testCase.token,
			}); err == nil {
				t.Fatal("NewRemoteBoard() = nil error")
			}
		})
	}
	board := newTestRemoteBoard(t, "https://host.example/prefix/", nil)
	if board.Identity() != "session-1" {
		t.Fatalf("Identity() = %q", board.Identity())
	}
	if got := board.boardURL.Path; got != "/prefix/v1/boards/session-1" {
		t.Fatalf("board path = %q", got)
	}
}

// Lifecycle calls carry the bearer credential, and a returned session token is
// validated the way Rust's AccessToken deserialization does.
func TestRemoteBoardLifecycleCallsLikeRust(t *testing.T) {
	recorder := &remoteBoardRecorder{}
	handler := func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPut:
			_, _ = writer.Write([]byte(`"session-1"`))
		case http.MethodPost:
			_, _ = writer.Write([]byte(`"` + remoteTestToken + `"`))
		default:
			_, _ = writer.Write([]byte(`{}`))
		}
	}
	httpServer := httptest.NewServer(http.HandlerFunc(handler))
	defer httpServer.Close()
	board := newTestRemoteBoard(t, httpServer.URL, nil)

	identity, err := board.CreateBoard(context.Background())
	if err != nil || identity != "session-1" {
		t.Fatalf("CreateBoard() = %q/%v", identity, err)
	}
	if got := recorder.last(); got.method != http.MethodPut || got.auth != "Bearer "+remoteTestToken ||
		got.path != "/v1/boards/session-1" {
		t.Fatalf("create request = %#v", got)
	}
	token, err := board.RegisterMembers(context.Background(), map[string]string{"thread-1": "/root"})
	if err != nil || token != remoteTestToken {
		t.Fatalf("RegisterMembers() = %q/%v", token, err)
	}
	if got := recorder.last(); got.method != http.MethodPost || !strings.Contains(string(got.body), `"members":{"thread-1":"/root"}`) {
		t.Fatalf("members request = %#v", got)
	}
	if err := board.DeleteBoard(context.Background()); err != nil {
		t.Fatalf("DeleteBoard() error = %v", err)
	}
	if got := recorder.last(); got.method != http.MethodDelete {
		t.Fatalf("delete request = %#v", got)
	}

	// A malformed returned credential is rejected.
	malformed := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`"short"`))
	}))
	defer malformed.Close()
	if _, err := newTestRemoteBoard(t, malformed.URL, nil).RegisterMembers(context.Background(), nil); err == nil {
		t.Fatal("RegisterMembers() accepted a malformed session token")
	}
}

// The versioned call envelope carries the caller, the method and its params, and
// only the operations Rust timestamps include a timestamp.
func TestRemoteBoardCallEnvelopeLikeRust(t *testing.T) {
	recorder := &remoteBoardRecorder{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		_, _ = writer.Write([]byte(`{"channel_name":"work","created_at":"2026-01-02T03:04:05Z","created_by":"/root","message_count":0}`))
	}))
	defer httpServer.Close()
	clock := &fakeBoardClock{value: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	board := newTestRemoteBoard(t, httpServer.URL, clock.now)
	if _, err := board.CreateChannel(context.Background(), "thread-1", CreateChannelRequest{
		ChannelName: "work", Subscription: Subscribe,
	}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	request := recorder.last()
	if request.path != "/v1/boards/session-1/call" || request.auth != "Bearer "+remoteTestToken {
		t.Fatalf("call request = %#v", request)
	}
	if request.method != http.MethodPost {
		t.Fatalf("call method = %q", request.method)
	}
	var envelope map[string]any
	if err := json.Unmarshal(request.body, &envelope); err != nil {
		t.Fatalf("Unmarshal(call) error = %v", err)
	}
	if envelope["caller"] != "thread-1" || envelope["method"] != "create_channel" {
		t.Fatalf("call envelope = %#v", envelope)
	}
	params, _ := envelope["params"].(map[string]any)
	if params["channel_name"] != "work" || params["subscription"] != "Subscribe" {
		t.Fatalf("call params = %#v", params)
	}
	if _, ok := envelope["timestamp"]; !ok {
		t.Fatalf("create_channel did not carry a timestamp: %#v", envelope)
	}

	// A read carries no timestamp, and the page limit uses Rust's default.
	if _, err := board.ListChannels(context.Background(), "thread-1", ChannelQuery{Direction: OldestFirst}); err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	var readEnvelope map[string]any
	if err := json.Unmarshal(recorder.last().body, &readEnvelope); err != nil {
		t.Fatalf("Unmarshal(call) error = %v", err)
	}
	if _, ok := readEnvelope["timestamp"]; ok {
		t.Fatalf("list_channels carried a timestamp: %#v", readEnvelope)
	}
	readParams, _ := readEnvelope["params"].(map[string]any)
	if readParams["direction"] != "OldestFirst" {
		t.Fatalf("read params = %#v", readParams)
	}
	page, _ := readParams["page"].(map[string]any)
	if page["limit"] != float64(DefaultPageLimit) {
		t.Fatalf("page = %#v", page)
	}
}

// A post retries without a fresh timestamp when the clock is unavailable, so the
// service can replay an already committed post.
func TestRemoteBoardPostRetriesWithoutAClockLikeRust(t *testing.T) {
	recorder := &remoteBoardRecorder{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		_, _ = writer.Write([]byte(`{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z"}`))
	}))
	defer httpServer.Close()
	clock := &fakeBoardClock{err: errors.New("clock unavailable")}
	board := newTestRemoteBoard(t, httpServer.URL, clock.now)
	posted, err := board.Post(context.Background(), "thread-1", PostRequest{
		RequestID:   "req-1",
		Destination: PostDestination{Kind: "new_channel", Name: "work"},
		Text:        "hello",
	})
	if err != nil || posted == nil || posted.MessageID != "m1" {
		t.Fatalf("Post() = %#v/%v", posted, err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.last().body, &envelope); err != nil {
		t.Fatalf("Unmarshal(call) error = %v", err)
	}
	if _, ok := envelope["timestamp"]; ok {
		t.Fatalf("a post with an unavailable clock carried a timestamp: %#v", envelope)
	}
	params, _ := envelope["params"].(map[string]any)
	if params["request_id"] != "req-1" || params["text"] != "hello" {
		t.Fatalf("post params = %#v", params)
	}
	destination, _ := params["destination"].(map[string]any)
	if destination["NewChannel"] != "work" {
		t.Fatalf("destination = %#v", destination)
	}
}

// Reads decode Rust's shapes, including Unicode post content and the flattened
// thread page.
func TestRemoteBoardReadsDecodeRustShapesLikeRust(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)
		switch envelope.Method {
		case "read_post":
			_, _ = writer.Write([]byte(`{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text":"héllo","n_chars":5,"next_offset_chars":3}`))
		case "read_thread":
			_, _ = writer.Write([]byte(`{"root_post":{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hé","n_chars":5,"truncated":false},"results":[{"message_id":"m2","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}],"n_returned":1,"has_more":false,"next_cursor":null}`))
		default:
			_, _ = writer.Write([]byte(`{}`))
		}
	}))
	defer httpServer.Close()
	board := newTestRemoteBoard(t, httpServer.URL, nil)
	content, err := board.ReadPost(context.Background(), "thread-1", ReadPostRequest{MessageID: "m1", OffsetChars: 1, LimitChars: 2})
	if err != nil || content.Text != "héllo" || content.NextOffsetChars != 3 {
		t.Fatalf("ReadPost() = %#v/%v", content, err)
	}
	thread, err := board.ReadThread(context.Background(), "thread-1", ReadThreadRequest{ThreadID: "m1"})
	if err != nil || thread.RootPost.TextPreview != "hé" || len(thread.Replies.Results) != 1 || thread.Replies.Results[0].MessageID != "m2" {
		t.Fatalf("ReadThread() = %#v/%v", thread, err)
	}
}

// notifyBoardHandler streams a readiness event followed by one notification.
func notifyBoardHandler(body string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("event: ready\ndata: {}\n\n"))
		writer.(http.Flusher).Flush()
		_, _ = writer.Write([]byte("event: notification\ndata: " + body + "\n\n"))
		writer.(http.Flusher).Flush()
	}
}

// The notification receiver waits for readiness, then delivers notices for this
// caller and turn.
func TestRemoteBoardNotificationsLikeRust(t *testing.T) {
	recorder := &remoteBoardRecorder{}
	notice := `{"recipient":"thread-1","turn_id":"turn-1","post":{"message_id":"m1","channel_name":"work","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}}`
	inner := notifyBoardHandler(notice)
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		inner(writer, request)
	}))
	defer httpServer.Close()
	board := newTestRemoteBoard(t, httpServer.URL, nil)
	receiver, err := board.Notifications(context.Background(), "thread-1", "turn-1")
	if err != nil {
		t.Fatalf("Notifications() error = %v", err)
	}
	defer receiver.Close()
	got, err := receiver.Next(context.Background())
	if err != nil || got == nil || got.Post.MessageID != "m1" {
		t.Fatalf("Next() = %#v/%v", got, err)
	}
	if end, err := receiver.Next(context.Background()); err != nil || end != nil {
		t.Fatalf("stream end = %#v/%v", end, err)
	}
	if last := recorder.last(); last.path != "/v1/boards/session-1/notifications" || last.auth != "Bearer "+remoteTestToken ||
		last.method != http.MethodPost {
		t.Fatalf("notification request = %#v", last)
	}

	// A stream that never acknowledges readiness fails setup.
	unready := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("event: other\ndata: {}\n\n"))
		writer.(http.Flusher).Flush()
	}))
	defer unready.Close()
	if _, err := newTestRemoteBoard(t, unready.URL, nil).Notifications(context.Background(), "thread-1", "turn-1"); err == nil {
		t.Fatal("Notifications() accepted a stream without readiness")
	}
}

// A notice for another agent or turn, or one whose preview exceeds the bound, is
// an error the host must not inject.
func TestRemoteBoardNotificationValidationLikeRust(t *testing.T) {
	oversized := strings.Repeat("x", remoteBoardNotificationPreviewChars+1)
	cases := []struct {
		name string
		body string
	}{
		{"another recipient", `{"recipient":"thread-2","turn_id":"turn-1","post":{"message_id":"m1","channel_name":"c","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}}`},
		{"another turn", `{"recipient":"thread-1","turn_id":"turn-2","post":{"message_id":"m1","channel_name":"c","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":"hi","n_chars":2,"truncated":false}}`},
		{"oversized preview", fmt.Sprintf(`{"recipient":"thread-1","turn_id":"turn-1","post":{"message_id":"m1","channel_name":"c","author":"/root","thread_id":"m1","created_at":"2026-01-02T03:04:05Z","text_preview":%q,"n_chars":200,"truncated":true}}`, oversized)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			httpServer := httptest.NewServer(notifyBoardHandler(testCase.body))
			defer httpServer.Close()
			receiver, err := newTestRemoteBoard(t, httpServer.URL, nil).Notifications(context.Background(), "thread-1", "turn-1")
			if err != nil {
				t.Fatalf("Notifications() error = %v", err)
			}
			defer receiver.Close()
			if _, err := receiver.Next(context.Background()); err == nil {
				t.Fatal("Next() accepted an invalid notification")
			}
		})
	}
}

// Rust bounds both directions: an oversized request is refused before it is sent,
// and an oversized response is a transport failure.
func TestRemoteBoardBoundsRequestAndResponseLikeRust(t *testing.T) {
	recorder := &remoteBoardRecorder{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder.record(request)
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer httpServer.Close()
	board := newTestRemoteBoard(t, httpServer.URL, nil)
	_, err := board.Post(context.Background(), "thread-1", PostRequest{
		RequestID:   "req-large",
		Destination: PostDestination{Kind: "new_channel", Name: "work"},
		Text:        strings.Repeat("x", remoteBoardMaxBody),
	})
	var invalidErr *InvalidRequestError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("oversized request error = %v, want an invalid request", err)
	}
	if recorder.last().path != "" {
		t.Fatal("an oversized request was sent")
	}

	huge := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"text":"` + strings.Repeat("x", remoteBoardMaxResponse) + `"}`))
	}))
	defer huge.Close()
	if _, err := newTestRemoteBoard(t, huge.URL, nil).ReadPost(context.Background(), "thread-1", ReadPostRequest{
		MessageID: "m1", LimitChars: 1,
	}); err == nil || errors.As(err, &invalidErr) {
		t.Fatalf("oversized response error = %v, want a transport failure", err)
	}
}

// Service failures map to Rust's error kinds: a client failure is an invalid
// request and a server failure is a transport error.
func TestRemoteBoardServiceErrorsLikeRust(t *testing.T) {
	status := http.StatusNotFound
	payload := `{"code":"unknown_channel","message":"no such channel"}`
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(payload))
	}))
	defer httpServer.Close()
	board := newTestRemoteBoard(t, httpServer.URL, nil)
	_, err := board.ListChannels(context.Background(), "thread-1", ChannelQuery{})
	var invalidErr *InvalidRequestError
	if !errors.As(err, &invalidErr) || !strings.Contains(err.Error(), "unknown_channel: no such channel") {
		t.Fatalf("client failure error = %v", err)
	}

	status = http.StatusInternalServerError
	payload = `{"code":"unavailable","message":"try later"}`
	_, err = board.ListChannels(context.Background(), "thread-1", ChannelQuery{})
	if err == nil || errors.As(err, &invalidErr) {
		t.Fatalf("server failure error = %v, want a transport error", err)
	}

	status = http.StatusOK
	payload = `{"results":[],"n_returned":0,"has_more":false,"next_cursor":null}`
	page, err := board.ListChannels(context.Background(), "thread-1", ChannelQuery{})
	if err != nil || page == nil || len(page.Results) != 0 {
		t.Fatalf("ListChannels() = %#v/%v", page, err)
	}
}
