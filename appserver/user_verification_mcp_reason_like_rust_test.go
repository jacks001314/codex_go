package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
)

func TestAppserverUserVerificationFailureReasonsLikeRust(t *testing.T) {
	request := &mcp.MCPElicitationRequest{
		Method: "openai/userVerification",
		Mode:   "openai/userVerification",
	}
	handler := &appserverMCPElicitationHandler{broker: NewServerRequestBroker()}

	response, err := handler.HandleMCPElicitation(context.Background(), request)
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	assertAppserverUserVerificationReason(t, response, mcp.UserVerificationReasonResponseError)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledBroker := NewServerRequestBroker()
	cancelledBroker.SetSink(ServerRequestSinkFunc(func(*ServerRequest) {}))
	response, err = (&appserverMCPElicitationHandler{broker: cancelledBroker}).HandleMCPElicitation(cancelled, request)
	if err != nil {
		t.Fatalf("HandleMCPElicitation(cancelled) error = %v", err)
	}
	assertAppserverUserVerificationReason(t, response, mcp.UserVerificationReasonInterrupted)

	closedBroker := NewServerRequestBroker()
	requestSeen := make(chan struct{})
	closedBroker.SetSink(ServerRequestSinkFunc(func(*ServerRequest) { close(requestSeen) }))
	result := make(chan *mcp.MCPElicitationResponse, 1)
	go func() {
		response, _ := (&appserverMCPElicitationHandler{broker: closedBroker}).HandleMCPElicitation(context.Background(), request)
		result <- response
	}()
	<-requestSeen
	closedBroker.RejectPending("", ErrServerRequestConnectionClosed)
	assertAppserverUserVerificationReason(t, <-result, mcp.UserVerificationReasonResponseChannelClosed)

	response, err = (&appserverMCPElicitationHandler{}).HandleMCPElicitation(context.Background(), request)
	if err != nil {
		t.Fatalf("HandleMCPElicitation(unavailable) error = %v", err)
	}
	assertAppserverUserVerificationReason(t, response, mcp.UserVerificationReasonApprovalUnavailable)
}

func assertAppserverUserVerificationReason(t *testing.T, response *mcp.MCPElicitationResponse, want mcp.UserVerificationReason) {
	t.Helper()
	if response == nil || response.Action != mcp.MCPElicitationActionCancel || response.Content != nil {
		t.Fatalf("response = %#v, want cancel without proof content", response)
	}
	if got, ok := mcp.UserVerificationReasonFromMeta(response.Meta); !ok || got != want {
		t.Fatalf("verification reason = %q, %v; want %q", got, ok, want)
	}
}
