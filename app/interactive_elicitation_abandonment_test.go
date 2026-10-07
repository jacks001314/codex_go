package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/mcp"
	codextea "codex_go/tui/tea"
)

// Mirrors Rust codex-mcp elicitation_tests for #51611
// (Signal abandonment of unanswered MCP elicitations): a published request
// whose waiter goes away emits an ordered, exactly-once abandonment carrying
// the original server name and response token, and a late response is a no-op.
func TestInteractiveElicitationAbandonmentIsOrderedAndExactlyOnceLikeRust(t *testing.T) {
	broker := newInteractiveElicitationBroker()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstMessages := make(chan bubbletea.Msg, 8)
	firstResult := make(chan error, 1)
	go func() {
		_, err := broker.mcpElicitationFunc(sendToMessages(firstMessages))(firstCtx, interactiveElicitationFormRequest("server", "first"))
		firstResult <- err
	}()
	firstRequest, ok := awaitElicitationMessage(t, firstMessages).(codextea.ElicitationRequestMsg)
	if !ok {
		t.Fatal("first published message is not an ElicitationRequestMsg")
	}
	if firstRequest.ServerName != "server" || firstRequest.RequestID != "first" {
		t.Fatalf("first request = %#v", firstRequest)
	}

	siblingMessages := make(chan bubbletea.Msg, 8)
	siblingResult := make(chan *mcp.MCPElicitationResponse, 1)
	go func() {
		response, _ := broker.mcpElicitationFunc(sendToMessages(siblingMessages))(context.Background(), interactiveElicitationFormRequest("server", "sibling"))
		siblingResult <- response
	}()
	siblingRequest, ok := awaitElicitationMessage(t, siblingMessages).(codextea.ElicitationRequestMsg)
	if !ok {
		t.Fatal("sibling published message is not an ElicitationRequestMsg")
	}
	if siblingRequest.ID == firstRequest.ID {
		t.Fatalf("request ids collide: %q", siblingRequest.ID)
	}

	// Dropping the first waiter abandons only that request, after its request
	// event and with the original identity carried through.
	cancelFirst()
	abandoned, ok := awaitElicitationMessage(t, firstMessages).(codextea.ElicitationAbandonedMsg)
	if !ok {
		t.Fatal("abandonment message is not an ElicitationAbandonedMsg")
	}
	if abandoned.ID != firstRequest.ID || abandoned.ServerName != "server" || abandoned.RequestID != "first" {
		t.Fatalf("abandonment = %#v, want id=%q server=server token=first", abandoned, firstRequest.ID)
	}
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned waiter error = %v, want context.Canceled", err)
	}
	if got := pendingElicitationCount(broker); got != 1 {
		t.Fatalf("pending requests after abandonment = %d, want 1", got)
	}

	// A late response for the abandoned request is a silent no-op: no second
	// abandonment, and it cannot resolve the sibling.
	broker.respond(codextea.ModalResponse{
		ID:          firstRequest.ID,
		Kind:        codextea.ModalKindElicitation,
		Elicitation: &codextea.ElicitationDecision{Action: "accept"},
	})
	broker.respond(codextea.ModalResponse{
		ID:          siblingRequest.ID,
		Kind:        codextea.ModalKindElicitation,
		Elicitation: &codextea.ElicitationDecision{Action: "accept"},
	})
	if response := <-siblingResult; response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("sibling response = %#v", response)
	}
	assertNoElicitationMessage(t, firstMessages)
	assertNoElicitationMessage(t, siblingMessages)
	if got := pendingElicitationCount(broker); got != 0 {
		t.Fatalf("pending requests after resolve = %d, want 0", got)
	}
}

// Mirrors Rust codex-mcp elicitation_tests for #51611
// (abandonment_close_joins_elected_actions_and_rejects_retained_router_admission):
// closing the router abandons every pending prompt and refuses new requests.
func TestInteractiveElicitationCloseAbandonsPendingAndRejectsNewLikeRust(t *testing.T) {
	broker := newInteractiveElicitationBroker()
	messages := make(chan bubbletea.Msg, 8)
	result := make(chan error, 1)
	go func() {
		_, err := broker.mcpElicitationFunc(sendToMessages(messages))(context.Background(), interactiveElicitationFormRequest("server", "pending"))
		result <- err
	}()
	request, ok := awaitElicitationMessage(t, messages).(codextea.ElicitationRequestMsg)
	if !ok {
		t.Fatal("published message is not an ElicitationRequestMsg")
	}

	broker.close()
	abandoned, ok := awaitElicitationMessage(t, messages).(codextea.ElicitationAbandonedMsg)
	if !ok {
		t.Fatal("abandonment message is not an ElicitationAbandonedMsg")
	}
	if abandoned.ID != request.ID || abandoned.ServerName != "server" || abandoned.RequestID != "pending" {
		t.Fatalf("abandonment = %#v", abandoned)
	}
	if err := <-result; !errors.Is(err, ErrInteractiveElicitationAbandoned) {
		t.Fatalf("closed waiter error = %v, want ErrInteractiveElicitationAbandoned", err)
	}
	if got := pendingElicitationCount(broker); got != 0 {
		t.Fatalf("pending requests after close = %d, want 0", got)
	}

	// close is idempotent and never replays an abandonment.
	broker.close()
	assertNoElicitationMessage(t, messages)

	// The closed router refuses new admissions.
	if _, err := broker.mcpElicitationFunc(sendToMessages(messages))(context.Background(), interactiveElicitationFormRequest("server", "later")); !errors.Is(err, ErrInteractiveElicitationClosed) {
		t.Fatalf("closed-router error = %v, want ErrInteractiveElicitationClosed", err)
	}
	assertNoElicitationMessage(t, messages)
}

func interactiveElicitationFormRequest(serverName, requestID string) *mcp.MCPElicitationRequest {
	request := &mcp.MCPElicitationRequest{
		ServerName:      serverName,
		Message:         "Continue",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	if requestID != "" {
		raw, err := json.Marshal(requestID)
		if err != nil {
			panic(err)
		}
		request.ID = raw
	}
	return request
}

func sendToMessages(messages chan<- bubbletea.Msg) func(bubbletea.Msg) {
	return func(message bubbletea.Msg) {
		messages <- message
	}
}

func awaitElicitationMessage(t *testing.T, messages <-chan bubbletea.Msg) bubbletea.Msg {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an elicitation message")
		return nil
	}
}

func assertNoElicitationMessage(t *testing.T, messages <-chan bubbletea.Msg) {
	t.Helper()
	select {
	case message := <-messages:
		t.Fatalf("unexpected message %T: %#v", message, message)
	case <-time.After(50 * time.Millisecond):
	}
}

func pendingElicitationCount(broker *interactiveElicitationBroker) int {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return len(broker.pending)
}
