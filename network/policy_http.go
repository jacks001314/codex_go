package network

// Application-policy enforcement for HTTP transports.
//
// Rust parity: codex-rs/http-client's managed request execution (#47389): a
// managed client checks each destination before route resolution, retains a
// permit while the response body is consumed, and reports a deterministic
// policy denial that callers must not retry.

import (
	"context"
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
	request, release := beginPermitRequest(permit, request)
	response, err := next.Do(request)
	if err != nil {
		release()
		permit.Release()
		// An abort caused by revocation is the policy denial, not a transport
		// failure the caller might retry.
		if policyErr := permit.Check(); policyErr != nil {
			return nil, &PolicyError{Err: policyErr}
		}
		return nil, err
	}
	if response == nil {
		release()
		permit.Release()
		return nil, nil
	}
	wrapResponseBodyWithPermit(permit, response, release)
	return response, nil
}

// beginPermitRequest derives the request's context from the permit's lifetime.
//
// Revocation must stop the work already in flight, not only the next operation:
// the derived context aborts the dial, the request and the response as soon as
// the permit is revoked (Rust #47408's cancellation token, which the shared
// client threads through every request). The watcher lives until the caller
// releases the operation, so a revocation while the response streams aborts that
// too; the returned release must run when the body or stream is closed.
func beginPermitRequest(permit *NetworkPermit, request *http.Request) (*http.Request, func()) {
	ctx, cancelRequest := context.WithCancel(request.Context())
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-permit.Revoked():
			cancelRequest()
		case <-stopWatch:
		}
	}()
	return request.WithContext(ctx), func() {
		close(stopWatch)
		cancelRequest()
	}
}

// wrapResponseBodyWithPermit keeps a permit alive for the response body, or, for
// a hijacked stream (an upgraded WebSocket connection), for as long as the
// caller owns the connection.
func wrapResponseBodyWithPermit(permit *NetworkPermit, response *http.Response, release func()) {
	if response == nil || response.Body == nil {
		release()
		permit.Release()
		return
	}
	if stream, hijacked := response.Body.(io.ReadWriteCloser); hijacked {
		// An upgraded connection outlives the response, so it keeps the permit
		// itself (Rust's WebSocketConnection retains the NetworkPermit).
		response.Body = newPermitStream(permit, stream, release)
		return
	}
	response.Body = newPermitBody(permit, response.Body, release)
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
	// release ends the request watcher and cancels the derived context.
	release func()
	once    sync.Once
}

func newPermitStream(permit *NetworkPermit, stream io.ReadWriteCloser, release func()) *permitStream {
	guarded := &permitStream{permit: permit, stream: stream, done: make(chan struct{}), release: release}
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
	err := s.stream.Close()
	s.once.Do(func() {
		close(s.done)
		if s.release != nil {
			s.release()
		}
		s.permit.Release()
	})
	return err
}

// permitBody keeps a request's permit alive for the whole response body and
// aborts the body when the permit is revoked. Rust does the same by racing the
// permit's revocation against the body future.
type permitBody struct {
	permit *NetworkPermit
	body   io.ReadCloser
	done   chan struct{}
	// release ends the request watcher and cancels the derived context.
	release func()
	once    sync.Once
}

func newPermitBody(permit *NetworkPermit, body io.ReadCloser, release func()) *permitBody {
	wrapped := &permitBody{permit: permit, body: body, done: make(chan struct{}), release: release}
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
	// Close the body first: a complete read lets the transport keep the
	// connection alive, and only then is the request's derived context released.
	err := b.body.Close()
	b.once.Do(func() {
		close(b.done)
		if b.release != nil {
			b.release()
		}
		b.permit.Release()
	})
	return err
}

var _ HTTPDoer = (*PolicyHTTPDoer)(nil)
