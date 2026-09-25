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

// recordingDoer stands in for a transport so a test can prove whether a
// request was attempted at all.
type recordingDoer struct {
	mu       sync.Mutex
	requests []*url.URL
	body     string
	err      error
}

func (d *recordingDoer) Do(request *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.requests = append(d.requests, request.URL)
	body, err := d.body, d.err
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (d *recordingDoer) attempted() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.requests)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// An endpoint scope is enforced even without a policy owner, so a bootstrap
// client can only reach the URLs it was scoped to.
func TestPolicyHTTPDoerEnforcesEndpointScopeLikeRust(t *testing.T) {
	allowed := mustURL(t, "https://cloud.example/backend-api/wham/config/bundle")
	next := &recordingDoer{body: "ok"}
	doer := &PolicyHTTPDoer{Policy: UnmanagedNetworkPolicy().RestrictToEndpoints([]*url.URL{allowed}), Next: next}

	if _, err := doer.Do(&http.Request{URL: mustURL(t, "https://cloud.example/backend-api/other")}); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("unscoped URL error = %v, want a destination denial", err)
	}
	if next.attempted() != 0 {
		t.Fatalf("attempted %d requests outside the endpoint scope", next.attempted())
	}
	response, err := doer.Do(&http.Request{URL: allowed})
	if err != nil {
		t.Fatalf("scoped request error = %v", err)
	}
	_ = response.Body.Close()
	if next.attempted() != 1 {
		t.Fatalf("attempted %d requests, want the scoped one to pass", next.attempted())
	}
}

// Rust parity: the managed http-client rejects a denied destination before the
// request is attempted, and an unmanaged policy changes nothing.
func TestPolicyHTTPDoerChecksDestinationBeforeConnectingLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	next := &recordingDoer{body: "ok"}
	doer := &PolicyHTTPDoer{Policy: policy, Next: next}

	// Nothing published yet: a managed transport fails closed.
	if _, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/path")}); !errors.Is(err, ErrNetworkPolicyUnavailable) {
		t.Fatalf("unpublished policy error = %v", err)
	}
	if next.attempted() != 0 {
		t.Fatalf("attempted %d requests under an unavailable policy", next.attempted())
	}

	controller.Publish(policy.Revision(), RestrictedDestinationPolicy([]string{"example.com"}))
	response, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/path")})
	if err != nil {
		t.Fatalf("allowed request error = %v", err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("read allowed body: %v", err)
	}
	_ = response.Body.Close()

	_, deniedErr := doer.Do(&http.Request{URL: mustURL(t, "https://denied.example/path")})
	if !errors.Is(deniedErr, ErrNetworkPolicyDestination) {
		t.Fatalf("denied destination error = %v", deniedErr)
	}
	if !IsPolicyError(deniedErr) {
		t.Fatalf("denied destination error %v is not classified as a policy denial", deniedErr)
	}
	if next.attempted() != 1 {
		t.Fatalf("attempted %d requests, want only the allowed one", next.attempted())
	}

	// An unmanaged policy is a pass-through, including for a denied host.
	unmanaged := &PolicyHTTPDoer{Policy: UnmanagedNetworkPolicy(), Next: next}
	if response, err := unmanaged.Do(&http.Request{URL: mustURL(t, "https://denied.example/path")}); err != nil {
		t.Fatalf("unmanaged request error = %v", err)
	} else {
		_ = response.Body.Close()
	}
	if next.attempted() != 2 {
		t.Fatalf("attempted %d requests, want the unmanaged one to pass through", next.attempted())
	}
}

// Rust parity: revocation aborts an in-flight response body instead of letting
// the operation finish successfully.
func TestPolicyHTTPDoerRevocationAbortsBodyReadsLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	next := &recordingDoer{body: strings.Repeat("x", 64)}
	doer := &PolicyHTTPDoer{Policy: policy, Next: next}

	response, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/path")})
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	controller.Policy().Invalidate()
	if _, err := io.ReadAll(response.Body); !errors.Is(err, ErrNetworkPolicyRevoked) {
		t.Fatalf("revoked body read error = %v", err)
	}
	_ = response.Body.Close()
}

// The policy also guards every redirect destination before it is followed.
func TestPolicyHTTPDoerChecksRedirectDestinationsLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), RestrictedDestinationPolicy([]string{"example.com"}))
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "example.com" {
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"https://denied.example/final"}},
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		}),
	}
	doer := &PolicyHTTPDoer{Policy: policy, Next: client}
	if _, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/start")}); !errors.Is(err, ErrNetworkPolicyDestination) {
		t.Fatalf("redirect error = %v, want a destination denial", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
