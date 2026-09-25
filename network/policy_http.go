package network

// Application-policy enforcement for HTTP transports.
//
// Rust parity: codex-rs/http-client's managed request execution (#47389): a
// managed client checks each destination before route resolution, retains a
// permit while the response body is consumed, and reports a deterministic
// policy denial that callers must not retry.

import (
	"errors"
	"io"
	"net/http"
	"sync"
)

// PolicyError reports a deterministic application-network-policy denial. A
// transport must surface it without retrying and without reporting the
// operation as successful, mirroring Rust's `TransportError::Policy`.
type PolicyError struct {
	Err error
}

func (e *PolicyError) Error() string {
	if e == nil || e.Err == nil {
		return ErrNetworkPolicyUnavailable.Error()
	}
	return e.Err.Error()
}

func (e *PolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsPolicyError reports whether err is (or wraps) a policy denial.
func IsPolicyError(err error) bool {
	var policyErr *PolicyError
	return errors.As(err, &policyErr)
}

// HTTPDoer is the transport seam the policy decorator wraps.
type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// PolicyHTTPDoer executes requests under an application network policy.
//
// An unmanaged policy passes every request through unchanged. A managed policy
// acquires one permit for the request, re-checks each redirect destination
// before it is followed, and keeps the permit valid until the response body is
// closed, so a revocation aborts an in-flight body read.
type PolicyHTTPDoer struct {
	Policy NetworkPolicy
	Next   HTTPDoer
}

func (d *PolicyHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	if d == nil || d.Next == nil {
		return nil, errors.New("network policy transport is unavailable")
	}
	if request == nil || !d.Policy.IsScoped() {
		return d.Next.Do(request)
	}
	permit, err := d.Policy.Acquire(request.URL)
	if err != nil {
		return nil, &PolicyError{Err: err}
	}
	next := d.Next
	if client, ok := d.Next.(*http.Client); ok {
		clone := *client
		previous := clone.CheckRedirect
		clone.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
			permit, err := d.Policy.Acquire(redirect.URL)
			if err != nil {
				return &PolicyError{Err: err}
			}
			// The check authorizes the hop; the request's own permit stays the
			// authorization retained for the operation, so this one is released.
			permit.Release()
			if previous != nil {
				return previous(redirect, via)
			}
			return nil
		}
		next = &clone
	}
	response, err := next.Do(request)
	if err != nil {
		permit.Release()
		return nil, err
	}
	if response == nil {
		permit.Release()
		return nil, nil
	}
	wrapResponseBodyWithPermit(permit, response)
	return response, nil
}

// wrapResponseBodyWithPermit keeps a permit alive for the response body, or, for
// a hijacked stream (an upgraded WebSocket connection), for as long as the
// caller owns the connection.
func wrapResponseBodyWithPermit(permit *NetworkPermit, response *http.Response) {
	if response == nil || response.Body == nil {
		permit.Release()
		return
	}
	if stream, hijacked := response.Body.(io.ReadWriteCloser); hijacked {
		// An upgraded connection outlives the response, so it keeps the permit
		// itself (Rust's WebSocketConnection retains the NetworkPermit).
		response.Body = newPermitStream(permit, stream)
		return
	}
	response.Body = newPermitBody(permit, response.Body)
}

// permitStream guards an upgraded connection with the request's permit.
//
// Rust's WebSocketConnection::poll_policy checks the permit before every read
// and write, with a revocation future per direction, and fails the operation
// with the policy denial. A Go read or write cannot poll while it blocks, so
// revocation also closes the connection to wake it; the resulting failure is
// reported as the same policy denial.
type permitStream struct {
	permit *NetworkPermit
	stream io.ReadWriteCloser
	done   chan struct{}
	once   sync.Once
}

func newPermitStream(permit *NetworkPermit, stream io.ReadWriteCloser) *permitStream {
	guarded := &permitStream{permit: permit, stream: stream, done: make(chan struct{})}
	go func() {
		select {
		case <-permit.Revoked():
			_ = stream.Close()
		case <-guarded.done:
		}
	}()
	return guarded
}

func (s *permitStream) Read(buffer []byte) (int, error) {
	if err := s.permit.Check(); err != nil {
		return 0, &PolicyError{Err: err}
	}
	count, err := s.stream.Read(buffer)
	if err != nil {
		// A revoked connection must not look like a clean end of stream, so the
		// revocation is reported even when the read ended.
		if checkErr := s.permit.Check(); checkErr != nil {
			return count, &PolicyError{Err: checkErr}
		}
	}
	return count, err
}

func (s *permitStream) Write(buffer []byte) (int, error) {
	if err := s.permit.Check(); err != nil {
		return 0, &PolicyError{Err: err}
	}
	count, err := s.stream.Write(buffer)
	if err != nil {
		if checkErr := s.permit.Check(); checkErr != nil {
			return count, &PolicyError{Err: checkErr}
		}
	}
	return count, err
}

// Close ends the guarded operation and releases the permit.
func (s *permitStream) Close() error {
	s.once.Do(func() { close(s.done) })
	s.permit.Release()
	return s.stream.Close()
}

// permitBody keeps a request's permit alive for the whole response body and
// aborts the body when the permit is revoked. Rust does the same by racing the
// permit's revocation against the body future.
type permitBody struct {
	permit *NetworkPermit
	body   io.ReadCloser
	done   chan struct{}
	once   sync.Once
}

func newPermitBody(permit *NetworkPermit, body io.ReadCloser) *permitBody {
	wrapped := &permitBody{permit: permit, body: body, done: make(chan struct{})}
	go func() {
		select {
		case <-permit.Revoked():
			_ = body.Close()
		case <-wrapped.done:
		}
	}()
	return wrapped
}

func (b *permitBody) Read(buffer []byte) (int, error) {
	if err := b.permit.Check(); err != nil {
		return 0, &PolicyError{Err: err}
	}
	count, err := b.body.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		// A read failure caused by revocation is reported as the policy denial,
		// not as a transient transport error (Rust preserves TransportError::Policy).
		if checkErr := b.permit.Check(); checkErr != nil {
			return count, &PolicyError{Err: checkErr}
		}
	}
	return count, err
}

func (b *permitBody) Close() error {
	b.once.Do(func() { close(b.done) })
	b.permit.Release()
	return b.body.Close()
}

var _ HTTPDoer = (*PolicyHTTPDoer)(nil)
