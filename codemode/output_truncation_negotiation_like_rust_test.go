package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"codex_go/tool"
)

// scriptedHandshakeTransport answers the connection hello and session/open so
// tests can inspect the exact frames the client writes (Rust #58125).
type scriptedHandshakeTransport struct {
	mu           sync.Mutex
	capabilities CapabilitySet
	requests     []HostRequest
	responses    []HostToClient
	closed       bool
	readable     chan struct{}
}

// wakeReader must be called with t.mu held.
func (t *scriptedHandshakeTransport) wakeReader() {
	if t.readable == nil {
		t.readable = make(chan struct{}, 1)
	}
	select {
	case t.readable <- struct{}{}:
	default:
	}
}

func (t *scriptedHandshakeTransport) Read(ctx context.Context, target any) (bool, error) {
	for {
		t.mu.Lock()
		if len(t.responses) > 0 {
			response := t.responses[0]
			t.responses = t.responses[1:]
			t.mu.Unlock()
			*target.(*HostToClient) = response
			return true, nil
		}
		if t.readable == nil {
			t.readable = make(chan struct{}, 1)
		}
		readable := t.readable
		t.mu.Unlock()
		select {
		case <-readable:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func (t *scriptedHandshakeTransport) Write(_ context.Context, message any) error {
	envelope, ok := message.(ClientToHost)
	if !ok {
		return nil
	}
	t.mu.Lock()
	switch envelope.Type {
	case "connection/hello":
		t.responses = append(t.responses, HostToClient{Type: "connection/ready", Hello: &HostHello{SelectedVersion: ProtocolV1, Capabilities: t.capabilities}})
	case "operation/request":
		if envelope.Request == nil {
			t.mu.Unlock()
			return nil
		}
		t.requests = append(t.requests, *envelope.Request)
		response := SessionReady(envelope.Request.SessionID)
		t.responses = append(t.responses, HostOperationResponse(envelope.ID, ResultOK(response)))
	}
	t.wakeReader()
	t.mu.Unlock()
	return nil
}

func (t *scriptedHandshakeTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *scriptedHandshakeTransport) openRequests() []HostRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]HostRequest, 0, len(t.requests))
	for _, request := range t.requests {
		if request.Method == "session/open" {
			out = append(out, request)
		}
	}
	return out
}

type scriptedSessionProvider struct {
	mu           sync.Mutex
	transports   []*scriptedHandshakeTransport
	handshakes   int
	capabilities CapabilitySet
}

func (p *scriptedSessionProvider) connect(context.Context) (*remoteConnection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handshakes++
	transport := &scriptedHandshakeTransport{capabilities: p.capabilities}
	p.transports = append(p.transports, transport)
	return connectRemoteTransport(context.Background(), transport)
}

func (p *scriptedSessionProvider) nextGeneration() uint64 { return 1 }

func TestRemoteSessionNegotiatesServerOutputTruncationLikeRust(t *testing.T) {
	capability, _ := NewCapability(BoundedOutputCapability)
	provider := &scriptedSessionProvider{capabilities: CapabilitySet{capability}}
	session := &remoteSession{
		provider: provider,
		id:       SessionID("session-1"),
		options:  SessionOptions{OutputTruncation: OutputTruncationServer},
	}
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	if got := session.OutputTruncation(); got != tool.CodeModeOutputTruncationServer {
		t.Fatalf("OutputTruncation() = %q, want server", got)
	}
	opens := provider.transports[0].openRequests()
	if len(opens) != 1 || opens[0].OutputTruncation == nil || *opens[0].OutputTruncation != OutputTruncationServer {
		t.Fatalf("session/open requests = %#v", opens)
	}

	// The negotiated mode survives a reconnect to a capable host.
	session.invalidateConnection(nil)
	session.openedOn = nil
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("re-admit() error = %v", err)
	}
	if got := session.OutputTruncation(); got != tool.CodeModeOutputTruncationServer {
		t.Fatalf("OutputTruncation() after reconnect = %q, want server", got)
	}
}

func TestRemoteSessionFallsBackToClientTruncationLikeRust(t *testing.T) {
	provider := &scriptedSessionProvider{}
	session := &remoteSession{
		provider: provider,
		id:       SessionID("session-1"),
		options:  SessionOptions{OutputTruncation: OutputTruncationServer},
	}
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	if got := session.OutputTruncation(); got != tool.CodeModeOutputTruncationClient {
		t.Fatalf("OutputTruncation() = %q, want client fallback", got)
	}
	opens := provider.transports[0].openRequests()
	if len(opens) != 1 || opens[0].OutputTruncation != nil {
		t.Fatalf("session/open requests = %#v", opens)
	}
}

func TestRemoteSessionRejectsReconnectWithoutServerTruncationLikeRust(t *testing.T) {
	capability, _ := NewCapability(BoundedOutputCapability)
	provider := &scriptedSessionProvider{capabilities: CapabilitySet{capability}}
	session := &remoteSession{
		provider: provider,
		id:       SessionID("session-1"),
		options:  SessionOptions{OutputTruncation: OutputTruncationServer},
	}
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	// The replacement host no longer advertises bounded-output-v1.
	provider.capabilities = CapabilitySet{}
	session.mu.Lock()
	session.openedOn = nil
	session.mu.Unlock()
	_, err := session.admit(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot preserve server output truncation") {
		t.Fatalf("re-admit() error = %v", err)
	}
}

func TestLocalServerOutputProviderForcesServerForBuiltInSessionsLikeRust(t *testing.T) {
	inner := &recordingSessionProvider{}
	wrapped := NewLocalServerOutputProvider(inner)
	if _, ok := wrapped.(tool.CodeModeRemoteSessionOptionsProvider); !ok {
		t.Fatal("wrapper does not expose session options")
	}
	wrapped.NewSession(nil)
	if got := inner.options; got.OutputTruncation != tool.CodeModeOutputTruncationServer {
		t.Fatalf("default session options = %#v", got)
	}
	// Explicit options pass through unchanged.
	wrapped.(tool.CodeModeRemoteSessionOptionsProvider).NewSessionWithOptions(nil, tool.CodeModeSessionOptions{OutputTruncation: tool.CodeModeOutputTruncationClient})
	if got := inner.options; got.OutputTruncation != tool.CodeModeOutputTruncationClient {
		t.Fatalf("explicit session options = %#v", got)
	}
}

func TestServerOutputBudgetOnlyAppliesToServerSessionsLikeRust(t *testing.T) {
	session := &remoteSession{}
	budget := 42
	if got := session.serverOutputBudget(&budget); got != nil {
		t.Fatalf("client-mode budget = %v, want nil", *got)
	}
	session.outputTruncation = OutputTruncationServer
	if got := session.serverOutputBudget(&budget); got == nil || *got != 42 {
		t.Fatalf("server-mode budget = %v", got)
	}
	if got := session.serverOutputBudget(nil); got != nil {
		t.Fatalf("nil budget = %v", got)
	}
}

// OperationRequest must keep the budget off the wire for client sessions.
func TestWaitBudgetFrameShapeLikeRust(t *testing.T) {
	sessionID, _ := NewSessionID("session-1")
	cellID := NewCellID("cell-1")
	budget := 128
	frame := WaitSessionRequestWithBudget(sessionID, WaitRequest{CellID: cellID, YieldTimeMS: 5}, &budget)
	encoded, err := json.Marshal(OperationRequest(3, frame))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"maxOutputTokens":128`) {
		t.Fatalf("bounded wait frame = %s", encoded)
	}
}

// Rust #57782: established sessions reuse their host channel; only session
// admission asks the provider for (and may replace) a host.
func TestRemoteSessionReusesLiveHostChannelLikeRust(t *testing.T) {
	provider := &scriptedSessionProvider{}
	session := &remoteSession{provider: provider, id: SessionID("session-1")}
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	if provider.handshakes != 1 {
		t.Fatalf("handshakes after first admit = %d, want 1", provider.handshakes)
	}
	if _, err := session.admit(context.Background()); err != nil {
		t.Fatalf("second admit() error = %v", err)
	}
	if provider.handshakes != 1 {
		t.Fatalf("a live binding asked for a replacement host: handshakes = %d", provider.handshakes)
	}
}
