package appserver

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/session"
)

// blockingSearchNotificationSink holds the first thread/search/partial
// notification until the test releases it, so cancellation and disconnect run
// while the search is provably in flight.
type blockingSearchNotificationSink struct {
	blocked chan struct{}
	release chan struct{}
	once    sync.Once

	mu            sync.Mutex
	notifications []*Notification
}

func newBlockingSearchNotificationSink() *blockingSearchNotificationSink {
	return &blockingSearchNotificationSink{blocked: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingSearchNotificationSink) Notify(notification *Notification) {
	if s == nil || notification == nil {
		return
	}
	s.mu.Lock()
	s.notifications = append(s.notifications, notification)
	s.mu.Unlock()
	if notification.Method != NotificationThreadSearchPartial {
		return
	}
	first := false
	s.once.Do(func() { first = true })
	if first {
		close(s.blocked)
		<-s.release
	}
}

func (s *blockingSearchNotificationSink) observed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.notifications)
}

func searchStreamingRecords(t *testing.T, titles ...string) (string, *session.Store) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(home)
	now := time.Unix(1_700_000_000, 0).UTC()
	if len(titles) == 0 {
		titles = []string{"needle one", "unrelated", "needle three", "needle four", "unrelated too", "needle six"}
	}
	for index, title := range titles {
		id := fmt.Sprintf("0198f00a-0000-7000-8000-%012d", index+1)
		record := &session.Record{
			ID:        session.ThreadID(id),
			SessionID: id,
			Title:     title,
			Items:     []session.Item{{Role: "user", Text: title}},
			CreatedAt: now.Add(time.Duration(index) * time.Second),
			UpdatedAt: now.Add(time.Duration(index) * time.Second),
			RecencyAt: now.Add(time.Duration(index) * time.Second),
			Metadata: session.Metadata{
				CWD:           home,
				ModelProvider: "openai",
				Source:        string(SessionSourceCli),
			},
		}
		if err := store.Create(record); err != nil {
			t.Fatalf("store.Create(%s) error = %v", record.ID, err)
		}
	}
	return home, store
}

func searchStreamingRouter(t *testing.T, store *session.Store, sink NotificationSink) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()})
	if sink != nil {
		router.SetNotificationSink(sink)
	}
	initializeThreadSampleConnection(t, router, "conn-1")
	initializeThreadSampleConnection(t, router, "conn-2")
	return router
}

func searchStreamingParams() ThreadSearchParams {
	limit := 4
	archived := false
	return ThreadSearchParams{
		Limit:         &limit,
		SortKey:       SortCreatedAt,
		SortDirection: SortAsc,
		Archived:      &archived,
		SearchTerm:    "needle",
	}
}

func requestIDText(id RequestID) string {
	return id.String()
}

// Rust #62132: stream partials are unique provisional rows for this request,
// and the final page is authoritative.
func TestThreadSearchStreamEmitsPartialRowsForItsOwnRequestLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	buffer := NewNotificationBuffer()
	router := searchStreamingRouter(t, store, buffer)
	response := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
		Params: mustJSON(t, searchStreamingParams()),
	})
	if response.Error != nil {
		t.Fatalf("thread/search/stream error = %+v", response.Error)
	}
	final := response.Result.(*ThreadSearchResponse)
	if len(final.Data) != 4 {
		t.Fatalf("final rows = %d, want the page size", len(final.Data))
	}

	seen := map[string]bool{}
	partialRows := 0
	for _, notification := range buffer.List() {
		if notification.Method != NotificationThreadSearchPartial {
			continue
		}
		partial, ok := notification.Params.(*ThreadSearchPartialNotification)
		if !ok {
			t.Fatalf("partial params = %T", notification.Params)
		}
		if requestIDText(partial.RequestID) != requestIDText(IntID(7)) {
			t.Fatalf("partial requestId = %q, want the streaming request id", requestIDText(partial.RequestID))
		}
		if len(partial.Data) == 0 {
			t.Fatal("partial batch is empty")
		}
		for _, row := range partial.Data {
			if !strings.Contains(strings.ToLower(row.Snippet), "needle") {
				t.Fatalf("partial row snippet %q is not a match", row.Snippet)
			}
			if seen[row.Thread.ID] {
				t.Fatalf("partial row %q repeated across batches", row.Thread.ID)
			}
			seen[row.Thread.ID] = true
			partialRows++
		}
	}
	if partialRows == 0 {
		t.Fatal("streaming search emitted no partial rows")
	}
	if partialRows > len(final.Data) {
		t.Fatalf("partial rows = %d, want at most the %d final rows", partialRows, len(final.Data))
	}
}

// Rust #62132: provisional rows must reach the client while the scan runs, not
// after the authoritative page exists.
func TestThreadSearchStreamEmitsPartialsBeforeTheFinalPageLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := NewRouter(store)
	emitted := make(chan []ThreadSearchResult, 1)
	release := make(chan struct{})
	progress := &threadSearchProgress{
		Streaming: true,
		Emit: func(rows []ThreadSearchResult) bool {
			emitted <- rows
			<-release
			return true
		},
	}
	done := make(chan *Response, 1)
	go func() {
		result, err := router.handleThreadSearchStream(&Request{
			JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream,
			Params: mustJSON(t, searchStreamingParams()),
		}, progress)
		if err != nil {
			done <- ErrorResponse(IntID(7), runtimeErrorCode(err), err.Error(), nil)
			return
		}
		done <- OK(IntID(7), result)
	}()
	select {
	case rows := <-emitted:
		if len(rows) == 0 {
			t.Fatal("first partial batch is empty")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no partial rows were emitted")
	}
	select {
	case response := <-done:
		t.Fatalf("search finished before its partial rows were delivered: %+v", response)
	default:
	}
	close(release)
	select {
	case response := <-done:
		if response.Error != nil {
			t.Fatalf("streaming search error = %+v", response.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streaming search did not finish")
	}
}

// Rust #62132: thread/search/stream must equal thread/search for the same
// params, including cursors.
func TestThreadSearchStreamMatchesPlainSearchPageLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := searchStreamingRouter(t, store, NewNotificationBuffer())
	streamed := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
		Params: mustJSON(t, searchStreamingParams()),
	})
	plain := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(8), Method: MethodThreadSearch, ConnectionID: "conn-1",
		Params: mustJSON(t, searchStreamingParams()),
	})
	if streamed.Error != nil || plain.Error != nil {
		t.Fatalf("streamed = %+v plain = %+v", streamed.Error, plain.Error)
	}
	streamedPage := streamed.Result.(*ThreadSearchResponse)
	plainPage := plain.Result.(*ThreadSearchResponse)
	if len(streamedPage.Data) != len(plainPage.Data) {
		t.Fatalf("streamed rows = %d, plain rows = %d", len(streamedPage.Data), len(plainPage.Data))
	}
	for index := range plainPage.Data {
		if streamedPage.Data[index].Thread.ID != plainPage.Data[index].Thread.ID ||
			streamedPage.Data[index].Snippet != plainPage.Data[index].Snippet {
			t.Fatalf("streamed row %d = %+v, plain = %+v", index, streamedPage.Data[index], plainPage.Data[index])
		}
	}
	if stringOrEmpty(streamedPage.NextCursor) != stringOrEmpty(plainPage.NextCursor) ||
		stringOrEmpty(streamedPage.BackwardsCursor) != stringOrEmpty(plainPage.BackwardsCursor) {
		t.Fatalf("streamed cursors = %#v/%#v, plain = %#v/%#v",
			streamedPage.NextCursor, streamedPage.BackwardsCursor, plainPage.NextCursor, plainPage.BackwardsCursor)
	}
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// Rust #62132: the streaming method rejects oversized search terms as invalid
// params while plain thread/search keeps accepting them.
func TestThreadSearchStreamRejectsOversizedTermLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := searchStreamingRouter(t, store, NewNotificationBuffer())
	oversized := map[string]string{
		"one byte over":       strings.Repeat("s", maxThreadSearchStreamTermBytes+1),
		"multi-byte overruns": strings.Repeat("茅", maxThreadSearchStreamTermBytes/3+1),
	}
	for name, term := range oversized {
		params := searchStreamingParams()
		params.SearchTerm = term
		response := router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(9), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
			Params: mustJSON(t, params),
		})
		if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
			t.Fatalf("%s: error = %+v, want invalid params", name, response.Error)
		}
		if !strings.Contains(response.Error.Message, "thread/search/stream searchTerm exceeds") {
			t.Fatalf("%s: message = %q", name, response.Error.Message)
		}
	}

	// A term exactly at the limit is still accepted by the streaming method.
	atLimit := searchStreamingParams()
	atLimit.SearchTerm = strings.Repeat("s", maxThreadSearchStreamTermBytes)
	accepted := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(10), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
		Params: mustJSON(t, atLimit),
	})
	if accepted.Error != nil && accepted.Error.Code == JSONRPCInvalidParamsErrorCode {
		t.Fatalf("term at the limit was rejected: %+v", accepted.Error)
	}

	params := searchStreamingParams()
	params.SearchTerm = strings.Repeat("s", maxThreadSearchStreamTermBytes+1)
	plain := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(11), Method: MethodThreadSearch, ConnectionID: "conn-1",
		Params: mustJSON(t, params),
	})
	if plain.Error != nil && plain.Error.Code == JSONRPCInvalidParamsErrorCode {
		t.Fatalf("thread/search rejected an oversized term: %+v", plain.Error)
	}
}

func waitForSearchInFlight(t *testing.T, sink *blockingSearchNotificationSink) {
	t.Helper()
	select {
	case <-sink.blocked:
	case <-time.After(5 * time.Second):
		t.Fatalf("search never reported progress; notifications=%d", sink.observed())
	}
}

// Rust #62132: cancellation is connection-scoped and method-family-scoped, so a
// non-owning request must leave the search running.
func TestThreadSearchCancelStopsOnlyTheOwningSearchLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	sink := newBlockingSearchNotificationSink()
	router := searchStreamingRouter(t, store, sink)
	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
			Params: mustJSON(t, searchStreamingParams()),
		})
	}()
	waitForSearchInFlight(t, sink)

	for _, cancel := range []struct {
		name         string
		connectionID string
		method       Method
	}{
		{"other connection", "conn-2", MethodThreadSearchStreamCancel},
		{"other method family", "conn-1", MethodThreadSearchCancel},
	} {
		response := router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(8), Method: cancel.method, ConnectionID: cancel.connectionID,
			Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(7)}),
		})
		if response.Error != nil {
			t.Fatalf("%s cancel error = %+v", cancel.name, response.Error)
		}
		if _, ok := response.Result.(*ThreadSearchCancelResponse); !ok {
			t.Fatalf("%s cancel result = %T", cancel.name, response.Result)
		}
	}

	close(sink.release)
	select {
	case response := <-done:
		if response.Error != nil {
			t.Fatalf("search was cancelled by a non-owning request: %+v", response.Error)
		}
		if rows := response.Result.(*ThreadSearchResponse).Data; len(rows) != 4 {
			t.Fatalf("rows = %d, want the full page", len(rows))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search did not finish")
	}
}

func TestThreadSearchCancelFailsTheOwningSearchLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	sink := newBlockingSearchNotificationSink()
	router := searchStreamingRouter(t, store, sink)
	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
			Params: mustJSON(t, searchStreamingParams()),
		})
	}()
	waitForSearchInFlight(t, sink)
	cancelled := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(8), Method: MethodThreadSearchStreamCancel, ConnectionID: "conn-1",
		Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(7)}),
	})
	if cancelled.Error != nil {
		t.Fatalf("cancel error = %+v", cancelled.Error)
	}
	close(sink.release)
	select {
	case response := <-done:
		if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode {
			t.Fatalf("cancelled search response = %+v", response)
		}
		if !strings.Contains(response.Error.Message, "cancelled") {
			t.Fatalf("cancelled search message = %q", response.Error.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled search did not return")
	}
}

func TestThreadSearchDisconnectStopsOwningSearchLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	sink := newBlockingSearchNotificationSink()
	router := searchStreamingRouter(t, store, sink)
	done := make(chan *Response, 1)
	go func() {
		done <- router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(7), Method: MethodThreadSearchStream, ConnectionID: "conn-1",
			Params: mustJSON(t, searchStreamingParams()),
		})
	}()
	waitForSearchInFlight(t, sink)
	router.ConnectionClosed("conn-1")
	close(sink.release)
	select {
	case response := <-done:
		if response.Error == nil || !strings.Contains(response.Error.Message, "cancelled") {
			t.Fatalf("disconnected search response = %+v", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search outlived its connection")
	}
}

// Rust #62132: the non-streaming family keeps its own cancellation channel, so
// thread/search/cancel reaches only a thread/search registration.
func TestThreadSearchCancelRuntimeTargetsItsOwnFamilyLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()})
	initializeThreadSampleConnection(t, router, "conn-1")
	initializeThreadSampleConnection(t, router, "conn-2")
	for _, testCase := range []struct {
		name      string
		streaming bool
		method    Method
	}{
		{"final only", false, MethodThreadSearchCancel},
		{"stream", true, MethodThreadSearchStreamCancel},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cancelled, release := router.registerThreadSearchCancel(&Request{ID: IntID(7), ConnectionID: "conn-1"}, testCase.streaming)
			defer release()
			for _, wrong := range []struct {
				name         string
				connectionID string
				method       Method
			}{
				{"other connection", "conn-2", testCase.method},
				{"other method family", "conn-1", MethodThreadSearchStreamCancel},
			} {
				if testCase.streaming {
					wrong.method = MethodThreadSearchCancel
				}
				response := router.Handle(&Request{
					JSONRPC: "2.0", ID: IntID(8), Method: wrong.method, ConnectionID: wrong.connectionID,
					Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(7)}),
				})
				if response.Error != nil {
					t.Fatalf("%s cancel error = %+v", wrong.name, response.Error)
				}
				if cancelled() {
					t.Fatalf("%s cancel stopped the search", wrong.name)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				response := router.Handle(&Request{
					JSONRPC: "2.0", ID: IntID(9), Method: testCase.method, ConnectionID: "conn-1",
					Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(7)}),
				})
				if response.Error != nil {
					t.Fatalf("owning cancel error = %+v", response.Error)
				}
				if !cancelled() {
					t.Fatal("owning cancel did not stop the search")
				}
			}
		})
	}
}

func TestThreadSearchCancelUnknownRequestIsNoOpLikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := searchStreamingRouter(t, store, NewNotificationBuffer())
	for _, method := range []Method{MethodThreadSearchCancel, MethodThreadSearchStreamCancel} {
		response := router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(3), Method: method, ConnectionID: "conn-1",
			Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(99)}),
		})
		if response.Error != nil {
			t.Fatalf("%s unknown cancel error = %+v", method, response.Error)
		}
	}
}

// Rust #62132: the new methods are experimental, so a connection without the
// capability is rejected with the same message Rust emits.
func TestThreadSearchStreamMethodsRequireExperimentalAPILikeRust(t *testing.T) {
	_, store := searchStreamingRecords(t)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), DefaultCWD: t.TempDir()})
	initialized := router.Handle(&Request{
		JSONRPC: "2.0", ID: IntID(0), Method: MethodInitialize, ConnectionID: "gate-off",
		Params: mustJSON(t, InitializeParams{
			ClientInfo:   ClientInfo{Name: "search-gate", Version: "1.0"},
			Capabilities: &InitializeCapabilities{},
		}),
	})
	if initialized.Error != nil {
		t.Fatalf("initialize error = %+v", initialized.Error)
	}
	for _, method := range []Method{MethodThreadSearchStream, MethodThreadSearchCancel, MethodThreadSearchStreamCancel} {
		response := router.Handle(&Request{
			JSONRPC: "2.0", ID: IntID(1), Method: method, ConnectionID: "gate-off",
			Params: mustJSON(t, ThreadSearchCancelParams{RequestID: IntID(1)}),
		})
		if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode {
			t.Fatalf("%s response = %+v, want invalid request", method, response)
		}
		want := string(method) + " requires experimentalApi capability"
		if response.Error.Message != want {
			t.Fatalf("%s message = %q, want %q", method, response.Error.Message, want)
		}
	}
}
