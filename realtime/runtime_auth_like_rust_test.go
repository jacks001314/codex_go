package realtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// runtimeAuthRecorder is a fake sideband endpoint that records the actor
// credential headers of every handshake.
type runtimeAuthRecorder struct {
	mu            sync.Mutex
	authorization []string
	assertion     []string
	dials         int
}

func (r *runtimeAuthRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		r.mu.Lock()
		r.dials++
		r.authorization = append(r.authorization, request.Header.Get("Authorization"))
		r.assertion = append(r.assertion, request.Header.Get("x-openai-agent-assertion"))
		r.mu.Unlock()
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *runtimeAuthRecorder) snapshot() (int, []string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dials, append([]string{}, r.authorization...), append([]string{}, r.assertion...)
}

func (r *runtimeAuthRecorder) waitFor(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		dials, _, _ := r.snapshot()
		if dials >= want || time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runtimeAuthStub rotates one runtime credential per handshake and records how
// often it was asked.
type runtimeAuthStub struct {
	mu    sync.Mutex
	calls int
}

func (s *runtimeAuthStub) headers(_ context.Context, endpoint string) (http.Header, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	return http.Header{
		"Authorization":            []string{fmt.Sprintf("AgentAssertion 1.%d.sig", call)},
		"X-OpenAI-Agent-Assertion": nil,
	}, nil
}

func (s *runtimeAuthStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestRealtimeAuthResolvesRuntimeAuthPerConnectLikeRust pins Rust #60003: the
// runtime credential is resolved for the handshake and replaces the actor
// credential, so the static bearer snapshot never reaches the wire.
func TestRealtimeAuthResolvesRuntimeAuthPerConnectLikeRust(t *testing.T) {
	recorder := &runtimeAuthRecorder{}
	server := recorder.server(t)
	stub := &runtimeAuthStub{}

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		SidebandBaseURL: server.URL,
		Headers:         http.Header{"Authorization": []string{"Bearer actor-token"}},
		RuntimeAuthHeaders: func(ctx context.Context, endpoint string) (http.Header, error) {
			return stub.headers(ctx, endpoint)
		},
	})
	manager.SetNotificationSink(func(Notification) {})
	includeStartupContext := false
	const threadID = "thread-runtime-auth-per-connect"
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              threadID,
		OutputModality:        OutputAudio,
		Transport:             ExistingCallTransport("rtc_runtime_auth"),
		IncludeStartupContext: &includeStartupContext,
	}); err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: threadID}, "requested") }()

	recorder.waitFor(t, 1)
	dials, authorizations, assertions := recorder.snapshot()
	if dials < 1 {
		t.Fatal("the sideband never connected")
	}
	if stub.count() < 1 {
		t.Fatal("the runtime auth hook was never called for the handshake")
	}
	if got := authorizations[0]; got != "AgentAssertion 1.1.sig" {
		t.Fatalf("handshake Authorization = %q, want the runtime assertion", got)
	}
	if assertions[0] != "" {
		t.Fatalf("a stale actor assertion header survived: %q", assertions[0])
	}
}

// stableRuntimeAuthStub keeps the same actor credential across handshakes but
// signs a fresh assertion for each one, mirroring a runtime whose credential
// revision did not change between two requests.
type stableRuntimeAuthStub struct {
	mu    sync.Mutex
	calls int
}

func (s *stableRuntimeAuthStub) headers(_ context.Context, endpoint string) (http.Header, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	return http.Header{
		"Authorization": []string{fmt.Sprintf("AgentAssertion 1.%d.sig", call)},
	}, nil
}

func (s *stableRuntimeAuthStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestWebsocketRevalidationRevalidatesWhenCredentialUnchangedLikeRust covers the
// "credential revision unchanged" cell of Rust #60055. Upstream does not reuse an
// admitted runtime socket: connection_pool.rs opens a fresh connection whenever a
// runtime is selected, and client_websockets_runtime_auth.rs asserts a second
// handshake even when key-a never changed ("preconnect must not admit work").
// What must hold here is that the unchanged credential is resolved again instead
// of replaying the credential snapshot taken for the first handshake.
func TestWebsocketRevalidationRevalidatesWhenCredentialUnchangedLikeRust(t *testing.T) {
	recorder := &runtimeAuthRecorder{}
	server := recorder.server(t)
	stub := &stableRuntimeAuthStub{}

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		SidebandBaseURL: server.URL,
		Headers:         http.Header{"Authorization": []string{"Bearer actor-token"}},
		RuntimeAuthHeaders: func(ctx context.Context, endpoint string) (http.Header, error) {
			return stub.headers(ctx, endpoint)
		},
	})
	manager.SetNotificationSink(func(Notification) {})
	includeStartupContext := false
	version := VersionV3
	const threadID = "thread-runtime-auth-stable-credential"
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              threadID,
		OutputModality:        OutputAudio,
		Version:               &version,
		Transport:             ExistingCallTransport("rtc_runtime_auth_stable"),
		IncludeStartupContext: &includeStartupContext,
	}); err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: threadID}, "requested") }()

	recorder.waitFor(t, 1)
	manager.mu.Lock()
	connection := manager.connections[threadID]
	manager.mu.Unlock()
	if connection == nil {
		t.Fatal("the sideband connection was not recorded")
	}
	connection.closeNow()
	recorder.waitFor(t, 2)

	dials, authorizations, _ := recorder.snapshot()
	if dials < 2 {
		t.Fatalf("dials = %d, want a second handshake after the unexpected close", dials)
	}
	if stub.count() < 2 {
		t.Fatalf("runtime auth hook calls = %d, want a fresh resolution per handshake", stub.count())
	}
	if authorizations[0] == authorizations[1] {
		t.Fatalf("both handshakes reused %q; an unchanged credential must still be re-resolved", authorizations[0])
	}
}

// TestWebsocketReconnectRevalidatesRuntimeAuthLikeRust pins Rust #60055: a
// reconnect resolves the runtime credential again instead of replaying the
// snapshot taken for the first handshake.
func TestWebsocketReconnectRevalidatesRuntimeAuthLikeRust(t *testing.T) {
	recorder := &runtimeAuthRecorder{}
	server := recorder.server(t)
	stub := &runtimeAuthStub{}

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		SidebandBaseURL: server.URL,
		Headers:         http.Header{"Authorization": []string{"Bearer actor-token"}},
		RuntimeAuthHeaders: func(ctx context.Context, endpoint string) (http.Header, error) {
			return stub.headers(ctx, endpoint)
		},
	})
	manager.SetNotificationSink(func(Notification) {})
	includeStartupContext := false
	// Only a frameless (V3) sideband reconnects after an unexpected loss.
	version := VersionV3
	const threadID = "thread-runtime-auth-reconnect"
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              threadID,
		OutputModality:        OutputAudio,
		Version:               &version,
		Transport:             ExistingCallTransport("rtc_runtime_auth_reconnect"),
		IncludeStartupContext: &includeStartupContext,
	}); err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: threadID}, "requested") }()

	recorder.waitFor(t, 1)
	manager.mu.Lock()
	connection := manager.connections[threadID]
	manager.mu.Unlock()
	if connection == nil {
		t.Fatal("the sideband connection was not recorded")
	}
	connection.closeNow()
	recorder.waitFor(t, 2)

	dials, authorizations, _ := recorder.snapshot()
	if dials < 2 {
		t.Fatalf("handshakes = %d, want a reconnect", dials)
	}
	if authorizations[0] == authorizations[1] {
		t.Fatalf("reconnect replayed the first credential %q, want a fresh one", authorizations[0])
	}
	if stub.count() < 2 {
		t.Fatalf("runtime auth hook calls = %d, want one per handshake", stub.count())
	}
}
