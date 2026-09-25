package network

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// recordingRoundTripper records whether a request reached the transport.
type recordingRoundTripper struct {
	mu       sync.Mutex
	requests []*url.URL
	body     string
}

func (t *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests = append(t.requests, request.URL)
	body := t.body
	t.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (t *recordingRoundTripper) attempted() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.requests)
}

// Rust parity: a concrete client carries the policy in its transport, so
// WebSocket dialing and HTTP requests are both checked before connecting.
func TestPolicyHTTPClientEnforcesPolicyInTransportLikeRust(t *testing.T) {
	next := &recordingRoundTripper{body: "ok"}
	client := &http.Client{Transport: next}

	// An unscoped policy leaves the caller's client untouched.
	if unchanged := PolicyHTTPClient(UnmanagedNetworkPolicy(), client); unchanged != client {
		t.Fatal("an unscoped policy replaced the caller's client")
	}

	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), RestrictedDestinationPolicy([]string{"allowed.example"}))
	scoped := PolicyHTTPClient(policy, client)
	if scoped == client {
		t.Fatal("a restricted policy returned the caller's client unwrapped")
	}
	if _, ok := scoped.Transport.(*PolicyRoundTripper); !ok {
		t.Fatalf("transport = %#v, want a policy round tripper", scoped.Transport)
	}

	_, err := scoped.Do(&http.Request{URL: mustURL(t, "https://denied.example/path")})
	if !errors.Is(err, ErrNetworkPolicyDestination) || !IsPolicyError(err) {
		t.Fatalf("denied request error = %v", err)
	}
	if next.attempted() != 0 {
		t.Fatalf("attempted %d requests, want the denial before the transport", next.attempted())
	}
	response, err := scoped.Do(&http.Request{URL: mustURL(t, "https://allowed.example/path")})
	if err != nil {
		t.Fatalf("allowed request error = %v", err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read allowed body: %v", err)
	}
	_ = response.Body.Close()
	if next.attempted() != 1 {
		t.Fatalf("attempted %d requests, want the allowed one", next.attempted())
	}
}

// A revocation aborts an in-flight response body for the transport-bound client
// too.
func TestPolicyHTTPClientRevocationAbortsBodyReadsLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	client := PolicyHTTPClient(policy, &http.Client{Transport: &recordingRoundTripper{body: strings.Repeat("x", 32)}})

	response, err := client.Do(&http.Request{URL: mustURL(t, "https://example.com/path")})
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	controller.Policy().Invalidate()
	if _, err := io.ReadAll(response.Body); !errors.Is(err, ErrNetworkPolicyRevoked) {
		t.Fatalf("revoked body read error = %v", err)
	}
	_ = response.Body.Close()
}
