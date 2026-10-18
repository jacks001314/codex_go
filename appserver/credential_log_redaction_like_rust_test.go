package appserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCredentialResponsesRedactDiagnosticTokensLikeRust(t *testing.T) {
	const (
		attestation = "synthetic-attestation-token"
		accessToken = "synthetic-refreshed-access-token"
	)
	values := []any{
		AttestationGenerateResponse{Token: attestation},
		ChatGPTAuthTokensRefreshResponse{AccessToken: accessToken, ChatGPTAccountID: "account-1"},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			got := fmt.Sprintf(format, value)
			if strings.Contains(got, attestation) || strings.Contains(got, accessToken) {
				t.Fatalf("credential response leaked through %s: %s", format, got)
			}
			if !strings.Contains(got, "<redacted>") {
				t.Fatalf("credential response omitted redaction through %s: %s", format, got)
			}
		}
	}
}

func TestServerRequestErrorsDoNotEchoCredentialPayloadLikeRust(t *testing.T) {
	const secret = "synthetic-client-supplied-credential"
	broker := NewServerRequestBroker()
	requests := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var response ChatGPTAuthTokensRefreshResponse
		done <- broker.Request(ctx, ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	}()
	request := <-requests
	if _, err := broker.Resolve(ErrorResponse(request.ID, -32000, "denied "+secret, nil)); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	err := <-done
	if err == nil {
		t.Fatal("server request unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "code=-32000") {
		t.Fatalf("server request error = %q, want code without credential payload", err)
	}
}

func TestMalformedCredentialResponseErrorIsGenericLikeRust(t *testing.T) {
	const secret = "synthetic-malformed-credential"
	broker := NewServerRequestBroker()
	requests := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var response ChatGPTAuthTokensRefreshResponse
		done <- broker.Request(ctx, ServerRequestChatGPTAuthTokensRefresh, nil, &response)
	}()
	request := <-requests
	if _, err := broker.Resolve(OK(request.ID, map[string]any{
		"accessToken": 42,
		"debug":       secret,
	})); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	err := <-done
	if err == nil {
		t.Fatal("malformed response unexpectedly decoded")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("malformed response error leaked parser detail: %q", err)
	}
}
