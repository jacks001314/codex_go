package network

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
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

// hijackedBody stands in for an upgraded connection, whose concrete type is what
// lets a WebSocket client take over the stream.
type hijackedBody struct{}

func (h *hijackedBody) Read([]byte) (int, error)    { return 0, io.EOF }
func (h *hijackedBody) Write(p []byte) (int, error) { return len(p), nil }
func (h *hijackedBody) Close() error                { return nil }

type hijackDoer struct{ body io.ReadWriteCloser }

func (d hijackDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: d.body}, nil
}

// Rust parity: an upgraded connection keeps the request's authorization for as
// long as the caller owns it, so its reads and writes are guarded by the permit
// (codex-websocket-client's WebSocketConnection::poll_policy, #47389).
func TestPolicyHTTPDoerGuardsHijackedStreamsLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	server, client := net.Pipe()
	defer server.Close()
	doer := &PolicyHTTPDoer{Policy: policy, Next: hijackDoer{body: client}}
	response, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/socket")})
	if err != nil {
		t.Fatalf("upgrade request error = %v", err)
	}
	stream, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatalf("the upgraded connection lost its writable stream: %#v", response.Body)
	}

	// While the permit is live the stream carries data in both directions.
	go func() { _, _ = server.Write([]byte("hello")) }()
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(stream, buffer); err != nil || string(buffer) != "hello" {
		t.Fatalf("read = %q/%v", buffer, err)
	}
	go func() {
		read := make([]byte, 5)
		_, _ = io.ReadFull(server, read)
	}()
	if _, err := stream.Write([]byte("world")); err != nil {
		t.Fatalf("write error = %v", err)
	}

	// Revocation fails the next operation with the policy denial and wakes a
	// blocked read by closing the connection.
	blocked := make(chan error, 1)
	go func() {
		_, err := stream.Read(make([]byte, 1))
		blocked <- err
	}()
	policy.Invalidate()
	select {
	case err := <-blocked:
		if !IsPolicyError(err) || !errors.Is(err, ErrNetworkPolicyRevoked) {
			t.Fatalf("blocked read error = %v, want the revocation denial", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked read was not woken by revocation")
	}
	if _, err := stream.Write([]byte("more")); !IsPolicyError(err) || !errors.Is(err, ErrNetworkPolicyRevoked) {
		t.Fatalf("write after revocation error = %v, want the revocation denial", err)
	}

	// Closing the connection closes the transport. The permit the stream owned is
	// released, which TestPolicyHTTPDoerReleasesHijackedStreamPermitsLikeRust
	// pins directly.
	if err := stream.Close(); err != nil {
		t.Fatalf("close error = %v", err)
	}
	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Fatal("closing the guarded stream left the transport open")
	}
}

// A guarded stream owns the request's permit and drops it when the caller closes
// the connection, mirroring Rust dropping the NetworkPermit with the connection.
func TestPolicyHTTPDoerReleasesHijackedStreamPermitsLikeRust(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	permit, err := policy.Acquire(mustURL(t, "https://example.com/socket"))
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	server, client := net.Pipe()
	defer server.Close()
	response := &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: client}
	wrapResponseBodyWithPermit(permit, response, func() {})
	if permit.isReleased() {
		t.Fatal("the guarded stream released the permit before the connection closed")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close error = %v", err)
	}
	if !permit.isReleased() {
		t.Fatal("closing the guarded stream did not release the permit")
	}
}

// An unmanaged policy never touches the response, so an upgrade that needs no
// authorization keeps the transport's own body.
func TestPolicyHTTPDoerLeavesHijackedStreamsAloneWithoutAPolicy(t *testing.T) {
	body := &hijackedBody{}
	doer := &PolicyHTTPDoer{Policy: UnmanagedNetworkPolicy(), Next: hijackDoer{body: body}}
	response, err := doer.Do(&http.Request{URL: mustURL(t, "https://example.com/socket")})
	if err != nil {
		t.Fatalf("upgrade request error = %v", err)
	}
	if response.Body != io.ReadCloser(body) {
		t.Fatalf("an unmanaged policy replaced the hijacked body: %#v", response.Body)
	}
}

// TestPolicyHTTPClientGuardsWebSocketDialLikeRust proves the guard reaches a real
// upgraded connection: an echo exchange works while the policy allows the
// destination, and revoking the policy denies the connection instead of letting
// it keep reading and writing (Rust #47389's realtime read/write guarding).
func TestPolicyHTTPClientGuardsWebSocketDialLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "")
		for {
			messageType, data, readErr := connection.Read(request.Context())
			if readErr != nil {
				return
			}
			if writeErr := connection.Write(request.Context(), messageType, data); writeErr != nil {
				return
			}
		}
	}))
	defer server.Close()
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	client := PolicyHTTPClient(policy, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"
	connection, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	if err := connection.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write error = %v", err)
	}
	messageType, data, err := connection.Read(ctx)
	if err != nil || messageType != websocket.MessageText || string(data) != "ping" {
		t.Fatalf("echo = %q/%q/%v", messageType, data, err)
	}

	// Revocation ends the connection: the permit's watcher closes the transport,
	// so the next read and write fail instead of continuing under a policy the
	// account no longer has.
	policy.Invalidate()
	revokedCtx, revokeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer revokeCancel()
	if err := connection.Write(revokedCtx, websocket.MessageText, []byte("again")); err == nil {
		if _, _, readErr := connection.Read(revokedCtx); readErr == nil {
			t.Fatal("a revoked policy left the WebSocket usable")
		}
	}
	if _, _, err := connection.Read(revokedCtx); err == nil {
		t.Fatal("a revoked policy left the WebSocket readable")
	}
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

// blockingDoer stands in for a request that is still in flight: it waits for its
// context and reports how the wait ended.
type blockingDoer struct {
	contexts chan context.Context
}

func (d *blockingDoer) Do(request *http.Request) (*http.Response, error) {
	select {
	case d.contexts <- request.Context():
	default:
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

// Rust #47408 ("cancel active work when permission is revoked"): a request that
// is still in flight when the policy is revoked is aborted, and the caller sees
// the policy denial rather than a transport error it might retry.
func TestPolicyHTTPDoerCancelsInFlightRequestsOnRevocationLikeRust(t *testing.T) {
	doer := &blockingDoer{contexts: make(chan context.Context, 1)}
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	guarded := &PolicyHTTPDoer{Policy: policy, Next: doer}
	done := make(chan error, 1)
	go func() {
		_, err := guarded.Do(&http.Request{URL: mustURL(t, "https://example.com/credentials")})
		done <- err
	}()
	var requestContext context.Context
	select {
	case requestContext = <-doer.contexts:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never started")
	}
	policy.Invalidate()
	select {
	case err := <-done:
		if !IsPolicyError(err) || !errors.Is(err, ErrNetworkPolicyRevoked) {
			t.Fatalf("in-flight error = %v, want the revocation denial", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation did not abort the in-flight request")
	}
	select {
	case <-requestContext.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the aborted request kept its context alive")
	}
}

// The round-tripper variant guards the clients built with PolicyHTTPClient.
func TestPolicyRoundTripperCancelsInFlightRequestsOnRevocationLikeRust(t *testing.T) {
	doer := &blockingDoer{contexts: make(chan context.Context, 1)}
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	roundTripper := &PolicyRoundTripper{Policy: policy, Next: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return doer.Do(request)
	})}
	done := make(chan error, 1)
	go func() {
		_, err := roundTripper.RoundTrip(&http.Request{URL: mustURL(t, "https://example.com/credentials")})
		done <- err
	}()
	select {
	case <-doer.contexts:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never started")
	}
	policy.Invalidate()
	select {
	case err := <-done:
		if !IsPolicyError(err) || !errors.Is(err, ErrNetworkPolicyRevoked) {
			t.Fatalf("in-flight error = %v, want the revocation denial", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation did not abort the in-flight request")
	}
}

// RunWithNetworkPermit passes a context that revocation cancels, so the work
// already running under the permit stops (Rust #47408's cancellation token).
func TestRunWithNetworkPermitCancelsTheOperationOnRevocation(t *testing.T) {
	controller := NewNetworkPolicyController()
	policy := controller.Policy()
	controller.Publish(policy.Revision(), UnrestrictedDestinationPolicy())
	permit, err := policy.Acquire(mustURL(t, "https://example.com/credentials"))
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	cancelled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, runErr := RunWithNetworkPermit(context.Background(), permit, func(runCtx context.Context) struct{} {
			<-runCtx.Done()
			close(cancelled)
			return struct{}{}
		})
		done <- runErr
	}()
	policy.Invalidate()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, ErrNetworkPolicyRevoked) {
			t.Fatalf("RunWithNetworkPermit() error = %v, want the revocation denial", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithNetworkPermit did not return on revocation")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the operation's context was not cancelled by revocation")
	}
}
