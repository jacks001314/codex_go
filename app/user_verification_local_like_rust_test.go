package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/mcp"
	codextea "codex_go/tui/tea"
)

// stubLocalUserVerificationProvider records signed attempts and lets a test hold
// the verifier open so the request can be resolved underneath it.
type stubLocalUserVerificationProvider struct {
	available bool
	proof     appserver.UserVerificationProof
	err       error

	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (p *stubLocalUserVerificationProvider) Available() bool {
	return p != nil && p.available
}

func (p *stubLocalUserVerificationProvider) Verify(ctx context.Context, request *mcp.MCPElicitationRequest) (appserver.UserVerificationProof, error) {
	p.mu.Lock()
	p.calls++
	started, release := p.started, p.release
	p.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return appserver.UserVerificationProof{}, ctx.Err()
		}
	}
	return p.proof, p.err
}

func (p *stubLocalUserVerificationProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func localUserVerificationRequest() *mcp.MCPElicitationRequest {
	return &mcp.MCPElicitationRequest{
		ServerName:  "server-1",
		Mode:        mcp.UserVerificationMode,
		Title:       "Approve the payment",
		Description: "Sign the challenge",
		Challenge:   "AQ",
		ID:          json.RawMessage(`"elicitation-1"`),
	}
}

func acceptElicitation(broker *interactiveElicitationBroker, id string) {
	broker.respond(codextea.ModalResponse{
		ID:          id,
		Kind:        codextea.ModalKindElicitation,
		Elicitation: &codextea.ElicitationDecision{Action: string(mcp.MCPElicitationActionAccept)},
	})
}

// publishUserVerification drives the broker entry point the local MCP runtime
// uses and returns the published prompt plus the response channel.
func publishUserVerification(t *testing.T, broker *interactiveElicitationBroker, sent chan bubbletea.Msg) (codextea.ElicitationRequestMsg, chan *mcp.MCPElicitationResponse) {
	t.Helper()
	results := make(chan *mcp.MCPElicitationResponse, 1)
	handler := broker.mcpElicitationFunc(func(message bubbletea.Msg) { sent <- message })
	go func() {
		response, err := handler(context.Background(), localUserVerificationRequest())
		if err != nil {
			results <- &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel, Meta: map[string]any{"error": err.Error()}}
			return
		}
		results <- response
	}()
	select {
	case message := <-sent:
		prompt, ok := message.(codextea.ElicitationRequestMsg)
		if !ok {
			t.Fatalf("published message = %#v, want an elicitation prompt", message)
		}
		return prompt, results
	case <-time.After(5 * time.Second):
		t.Fatal("the user-verification prompt was never published")
	}
	return codextea.ElicitationRequestMsg{}, nil
}

// Rust #43708: a local request may only run one verification, and the accepted
// proof is returned as the elicitation content.
func TestLocalUserVerificationSignsOnceLikeRust(t *testing.T) {
	provider := &stubLocalUserVerificationProvider{
		available: true,
		proof:     appserver.UserVerificationProof{CredentialID: "credential-1", Signature: "sig-1"},
	}
	broker := newInteractiveElicitationBroker()
	broker.setUserVerificationProvider(provider)
	sent := make(chan bubbletea.Msg, 4)
	prompt, results := publishUserVerification(t, broker, sent)

	acceptElicitation(broker, prompt.ID)
	select {
	case response := <-results:
		if response.Action != mcp.MCPElicitationActionAccept {
			t.Fatalf("response action = %q, want accept", response.Action)
		}
		content, _ := response.Content.(map[string]any)
		if got := content["credentialId"]; got != "credential-1" {
			t.Fatalf("credentialId = %#v", got)
		}
		if got := content["signature"]; got != "sig-1" {
			t.Fatalf("signature = %#v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the accepted verification never resolved")
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}

	// A duplicate resolution must not sign a second time (Rust
	// UserVerificationRequests::begin).
	duplicate := broker.userVerificationAccept(context.Background(), localUserVerificationRequest(), "server-1", "elicitation-1")
	if duplicate.Action != mcp.MCPElicitationActionCancel {
		t.Fatalf("duplicate action = %q, want cancel", duplicate.Action)
	}
	if reason, ok := mcp.UserVerificationReasonFromMeta(duplicate.Meta); !ok || reason != mcp.UserVerificationReasonInterrupted {
		t.Fatalf("duplicate reason = %#v", duplicate.Meta)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("verifier calls after the duplicate = %d, want 1", got)
	}
}

// Rust #43708: a proof that finishes after the prompt was resolved is stale and
// must not be handed back to the MCP server.
func TestLocalUserVerificationDropsStaleProofLikeRust(t *testing.T) {
	provider := &stubLocalUserVerificationProvider{
		available: true,
		proof:     appserver.UserVerificationProof{CredentialID: "credential-1", Signature: "sig-1"},
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	broker := newInteractiveElicitationBroker()
	broker.setUserVerificationProvider(provider)
	sent := make(chan bubbletea.Msg, 4)
	prompt, results := publishUserVerification(t, broker, sent)

	acceptElicitation(broker, prompt.ID)
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the verifier never started")
	}
	// The router closes (or the request is resolved) while the native worker is
	// still running, so its proof no longer belongs to a live prompt.
	broker.userVerification.Remove("server-1", "elicitation-1")
	close(provider.release)

	select {
	case response := <-results:
		if response.Action != mcp.MCPElicitationActionCancel {
			t.Fatalf("stale response action = %q, want cancel", response.Action)
		}
		if content, _ := response.Content.(map[string]any); len(content) != 0 {
			t.Fatalf("stale response leaked proof content: %#v", response.Content)
		}
		if reason, ok := mcp.UserVerificationReasonFromMeta(response.Meta); !ok || reason != mcp.UserVerificationReasonInterrupted {
			t.Fatalf("stale reason = %#v", response.Meta)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stale verification never resolved")
	}
}

// Rust #43708: a build without a native provider never publishes a prompt it
// could not answer; the request is cancelled as providerUnavailable.
func TestLocalUserVerificationUnavailableProviderDoesNotPublishLikeRust(t *testing.T) {
	broker := newInteractiveElicitationBroker()
	broker.setUserVerificationProvider(&stubLocalUserVerificationProvider{available: false})
	sent := make(chan bubbletea.Msg, 2)
	response, err := broker.mcpElicitationFunc(func(message bubbletea.Msg) { sent <- message })(context.Background(), localUserVerificationRequest())
	if err != nil {
		t.Fatalf("mcpElicitationFunc: %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionCancel {
		t.Fatalf("response = %#v, want cancel", response)
	}
	reason, ok := mcp.UserVerificationReasonFromMeta(response.Meta)
	if !ok || reason != mcp.UserVerificationReasonProviderUnavailable {
		t.Fatalf("reason = %#v, want providerUnavailable", response.Meta)
	}
	select {
	case message := <-sent:
		t.Fatalf("an unavailable provider published a prompt: %#v", message)
	default:
	}
}
