package appserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/mcp"
)

// ownershipVerificationSink records targeted deliveries without answering, so
// the broker keeps a pending user-verification request alive.
type ownershipVerificationSink struct {
	targeted chan string
	requests chan *ServerRequest
}

func (s *ownershipVerificationSink) SendServerRequest(*ServerRequest) {}

func (s *ownershipVerificationSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	s.targeted <- connectionID
	s.requests <- request
}

func newOwnershipVerificationBroker(t *testing.T, revision *atomic.Uint64) (*ServerRequestBroker, *ownershipVerificationSink) {
	t.Helper()
	broker := NewServerRequestBroker()
	broker.SetVerificationAuth(func() uint64 { return revision.Load() })
	sink := &ownershipVerificationSink{
		targeted: make(chan string, 4),
		requests: make(chan *ServerRequest, 4),
	}
	broker.SetSink(sink)
	return broker, sink
}

func startOwnershipVerification(t *testing.T, broker *ServerRequestBroker, connectionID string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		var response MCPElicitationRequestResponse
		done <- broker.RequestToConnection(context.Background(), connectionID, ServerRequestMCPElicitation, &MCPElicitationRequestParams{
			ThreadID:   "thread-verification",
			ServerName: "codex_apps",
			Mode:       mcp.UserVerificationMode,
			Title:      "Approve",
			Challenge:  "AQ",
		}, &response)
	}()
	return done
}

func verificationProof() *MCPElicitationRequestResponse {
	return &MCPElicitationRequestResponse{
		Action:  MCPElicitationActionAccept,
		Content: map[string]any{"credentialId": "credential-1", "signature": "c2ln"},
	}
}

// Mirrors Rust #43715: only the connection the ceremony was delivered to may
// answer it, and replaying the thread never hands the proof to another peer.
func TestUserVerificationAcceptsOnlyOwnerResponseLikeRust(t *testing.T) {
	var revision atomic.Uint64
	revision.Store(11)
	broker, sink := newOwnershipVerificationBroker(t, &revision)
	done := startOwnershipVerification(t, broker, "conn-owner")

	var request *ServerRequest
	select {
	case request = <-sink.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("verification request was not delivered")
	}
	if got := <-sink.targeted; got != "conn-owner" {
		t.Fatalf("targeted connection = %q, want conn-owner", got)
	}
	if sent := broker.ReplayThreadToConnection("thread-verification", "conn-intruder"); sent != 0 {
		t.Fatalf("replayed %d verification requests to another connection", sent)
	}
	if resolved, _ := broker.ResolveFromConnection("conn-intruder", OK(request.ID, verificationProof())); resolved {
		t.Fatal("non-owning connection resolved the verification request")
	}
	select {
	case err := <-done:
		t.Fatalf("verification resolved by a non-owner: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if resolved, _ := broker.ResolveFromConnection("conn-owner", OK(request.ID, verificationProof())); !resolved {
		t.Fatal("owning connection could not resolve the verification request")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("owner response error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("verification request did not complete")
	}
}

// Mirrors Rust watch_user_verification_auth: a credential change cancels a
// pending device ceremony and drops any late response.
func TestUserVerificationAuthChangeCancelsPendingLikeRust(t *testing.T) {
	var revision atomic.Uint64
	revision.Store(21)
	broker, sink := newOwnershipVerificationBroker(t, &revision)
	done := startOwnershipVerification(t, broker, "conn-owner")

	var request *ServerRequest
	select {
	case request = <-sink.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("verification request was not delivered")
	}
	revision.Add(1)
	if canceled := broker.CancelStaleVerificationPending(); canceled != 1 {
		t.Fatalf("CancelStaleVerificationPending() = %d, want 1", canceled)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled verification error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending verification was not canceled after the authentication change")
	}
	if resolved, _ := broker.ResolveFromConnection("conn-owner", OK(request.ID, verificationProof())); resolved {
		t.Fatal("late response resolved a canceled verification request")
	}
}

func initializeUserVerificationConnection(t *testing.T, router *RuntimeRouter, connectionID string) {
	t.Helper()
	request := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	request.ConnectionID = connectionID
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("initialize %s error = %+v", connectionID, response.Error)
	}
}

func startRouterVerification(t *testing.T, router *RuntimeRouter) <-chan *mcp.MCPElicitationResponse {
	t.Helper()
	handler, ok := router.requireMCP().ElicitationHandler().(*appserverMCPElicitationHandler)
	if !ok {
		t.Fatalf("elicitation handler = %T", router.requireMCP().ElicitationHandler())
	}
	done := make(chan *mcp.MCPElicitationResponse, 1)
	go func() {
		response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
			ServerName:  "codex_apps",
			ThreadID:    "thread-verification",
			Method:      mcp.UserVerificationMode,
			Title:       "Approve",
			Description: "Sign the challenge",
			Challenge:   "AQ",
		})
		if err != nil {
			done <- &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel, Meta: map[string]any{"error": err.Error()}}
			return
		}
		done <- response
	}()
	return done
}

// Mirrors Rust #43715: a transport disconnect hands verification ownership to
// the live host immediately, and the pending ceremony is canceled.
func TestUserVerificationDisconnectReleasesOwnershipLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		UserVerificationProvider: deviceSupportVerificationProvider{supported: true},
	})
	router.SetRequestTransport("in-process")
	initializeUserVerificationConnection(t, router, "conn-1")
	initializeUserVerificationConnection(t, router, "conn-2")
	if got := router.userVerificationConnection(); got != "conn-1" {
		t.Fatalf("owning connection = %q, want conn-1", got)
	}
	sink := &ownershipVerificationSink{
		targeted: make(chan string, 4),
		requests: make(chan *ServerRequest, 4),
	}
	router.SetServerRequestSink(sink)

	pending := startRouterVerification(t, router)
	select {
	case <-sink.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("verification request was not delivered")
	}
	if got := <-sink.targeted; got != "conn-1" {
		t.Fatalf("targeted connection = %q, want conn-1", got)
	}

	router.releaseUserVerificationConnection("conn-1")
	if got := router.userVerificationConnection(); got != "conn-2" {
		t.Fatalf("owning connection after release = %q, want conn-2", got)
	}
	select {
	case response := <-pending:
		if response.Action != mcp.MCPElicitationActionCancel {
			t.Fatalf("canceled response = %#v", response)
		}
		if reason, ok := mcp.UserVerificationReasonFromMeta(response.Meta); !ok || reason != mcp.UserVerificationReasonInterrupted {
			t.Fatalf("cancellation reason = %#v", response.Meta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending verification survived the disconnect")
	}

	next := startRouterVerification(t, router)
	var request *ServerRequest
	select {
	case request = <-sink.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("second verification request was not delivered")
	}
	if got := <-sink.targeted; got != "conn-2" {
		t.Fatalf("second targeted connection = %q, want conn-2", got)
	}
	if resolved, _ := router.requireServerRequests().ResolveFromConnection("conn-2", OK(request.ID, verificationProof())); !resolved {
		t.Fatal("live owner could not answer the verification request")
	}
	select {
	case response := <-next:
		if response.Action != mcp.MCPElicitationActionAccept {
			t.Fatalf("response = %#v, want accept", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second verification request did not complete")
	}
}

// Mirrors Rust #43715: an authentication change cancels the in-flight ceremony
// without waiting for a client response.
func TestUserVerificationRouterCancelsOnAuthChangeLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		UserVerificationProvider: deviceSupportVerificationProvider{supported: true},
	})
	router.SetRequestTransport("in-process")
	initializeUserVerificationConnection(t, router, "conn-1")
	sink := &ownershipVerificationSink{
		targeted: make(chan string, 4),
		requests: make(chan *ServerRequest, 4),
	}
	router.SetServerRequestSink(sink)

	pending := startRouterVerification(t, router)
	select {
	case <-sink.requests:
	case <-time.After(2 * time.Second):
		t.Fatal("verification request was not delivered")
	}
	router.noteAuthChanged()
	select {
	case response := <-pending:
		if response.Action != mcp.MCPElicitationActionCancel {
			t.Fatalf("canceled response = %#v", response)
		}
		if reason, ok := mcp.UserVerificationReasonFromMeta(response.Meta); !ok || reason != mcp.UserVerificationReasonInterrupted {
			t.Fatalf("cancellation reason = %#v", response.Meta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending verification survived the authentication change")
	}
}
