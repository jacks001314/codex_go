package appserver

import (
	"sync"
	"testing"
	"time"

	"codex_go/turn"
)

// gatedServerRequestSink blocks the first write so a test can observe a replay
// that is still handing a request to a slow transport.
type gatedServerRequestSink struct {
	mu      sync.Mutex
	sent    []string
	first   chan *ServerRequest
	release chan struct{}
	wait    sync.Once
}

func newGatedServerRequestSink() *gatedServerRequestSink {
	return &gatedServerRequestSink{first: make(chan *ServerRequest, 1), release: make(chan struct{})}
}

func (s *gatedServerRequestSink) SendServerRequest(request *ServerRequest) {
	s.mu.Lock()
	s.sent = append(s.sent, request.ID.String())
	s.mu.Unlock()
	block := false
	s.wait.Do(func() { block = true })
	if block {
		s.first <- request
		<-s.release
	}
}

func (s *gatedServerRequestSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func newReplayTestRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func registerReplayRequest(broker *ServerRequestBroker, id int64, threadID string) {
	entry := &pendingServerRequest{
		ch: make(chan *serverRequestResult, 1),
		request: &ServerRequest{
			ID: IntID(id), Method: ServerRequestCommandExecutionApproval,
			Params: CommandExecutionRequestApprovalParams{ThreadID: threadID},
		},
		threadID: threadID,
	}
	broker.register(entry.request.ID.String(), entry)
}

func waitForReplay(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background replay did not finish in time")
}

// Mirrors Rust #62609: the resume response is not held by the replay, so a slow
// client cannot delay the resume while the requests are written.
func TestThreadResumeReplayRunsAfterTheResponseLikeRust(t *testing.T) {
	router := newReplayTestRouter(t)
	broker := router.requireServerRequests()
	sink := newGatedServerRequestSink()
	broker.SetSink(sink)
	registerReplayRequest(broker, 1, "thread-1")
	registerReplayRequest(broker, 2, "thread-1")

	started := time.Now()
	router.replayPendingServerRequestsForThread(&ThreadResumeResponse{Thread: &Thread{ID: "thread-1"}}, defaultRequestConnectionID)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("resume replay blocked for %s while the transport was busy", elapsed)
	}
	select {
	case <-sink.first:
	case <-time.After(2 * time.Second):
		t.Fatal("background replay never reached the transport")
	}
	if got := sink.snapshot(); len(got) != 1 {
		t.Fatalf("sent before release = %v, want only the in-flight request", got)
	}
	close(sink.release)
	waitForReplay(t, func() bool { return len(sink.snapshot()) == 2 })
}

// Mirrors Rust #62609: a newer resume replaces the replay still running for the
// connection, so the superseded task stops after the write it already started.
func TestNewerResumeReplacesRunningReplayLikeRust(t *testing.T) {
	router := newReplayTestRouter(t)
	broker := router.requireServerRequests()
	sink := newGatedServerRequestSink()
	broker.SetSink(sink)
	for id := int64(1); id <= 3; id++ {
		registerReplayRequest(broker, id, "thread-1")
	}
	resume := &ThreadResumeResponse{Thread: &Thread{ID: "thread-1"}}
	router.replayPendingServerRequestsForThread(resume, defaultRequestConnectionID)
	select {
	case <-sink.first:
	case <-time.After(2 * time.Second):
		t.Fatal("first replay never reached the transport")
	}
	router.replayPendingServerRequestsForThread(resume, defaultRequestConnectionID)
	close(sink.release)
	waitForReplay(t, func() bool { return len(sink.snapshot()) == 4 })

	sent := sink.snapshot()
	counts := map[string]int{}
	for _, id := range sent {
		counts[id]++
	}
	if counts["1"] != 2 || counts["2"] != 1 || counts["3"] != 1 {
		t.Fatalf("sent = %v, want the superseded replay to stop after request 1", sent)
	}
}
