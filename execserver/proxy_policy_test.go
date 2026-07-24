package execserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type recordingRoundTripper struct {
	mu       sync.Mutex
	requests []*http.Request
	response *http.Response
	err      error
}

func (r *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request.Clone(request.Context()))
	r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return r.response, nil
}

func (r *recordingRoundTripper) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func TestDelegatedHTTPRequestUsesConfiguredFinalTransport(t *testing.T) {
	t.Setenv("CODEX_CA_CERTIFICATE", "")
	t.Setenv("SSL_CERT_FILE", "")
	transport := &recordingRoundTripper{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Test": []string{"proxied"}},
		Body:       io.NopCloser(strings.NewReader("through-policy")),
	}}
	server := NewServerWithHTTPClient(&http.Client{Transport: transport})

	response, err := doHTTPRequest(context.Background(), &HTTPRequestParams{
		Method: "GET",
		URL:    "https://target.invalid/private",
	}, server.httpClient)
	if err != nil {
		t.Fatalf("doHTTPRequest() error = %v", err)
	}
	if transport.count() != 1 || response.Status != http.StatusOK {
		t.Fatalf("configured transport calls = %d, response = %+v", transport.count(), response)
	}
}

func TestExecServerWebSocketDialUsesConfiguredFinalTransport(t *testing.T) {
	sentinel := errors.New("configured websocket transport")
	transport := &recordingRoundTripper{err: sentinel}
	_, err := DialClientWithOptions(context.Background(), "ws://executor.invalid/rpc", DialClientOptions{
		ClientName: "proxy-policy-test",
		HTTPClient: &http.Client{Transport: transport},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("DialClientWithOptions() error = %v, want sentinel", err)
	}
	if transport.count() != 1 {
		t.Fatalf("configured transport calls = %d, want 1", transport.count())
	}
}
