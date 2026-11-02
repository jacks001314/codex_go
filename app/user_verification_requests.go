package app

import (
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Rust parity: #43708 (95327467c3)
// (codex-rs/tui/src/app/user_verification_requests.rs). The approved request
// payload stays separate from the view, and every started verification carries
// an attempt id plus a cancellation signal so a late result can be rejected
// after the request was cancelled, resolved by the server, or dropped by a
// reconnect.

// UserVerificationParams is the request payload the verifier needs. It never
// carries a proof.
type UserVerificationParams struct {
	Title       string
	Description string
	Challenge   string
}

type userVerificationRequestKey struct {
	serverName string
	requestID  string
}

type pendingUserVerification struct {
	params    UserVerificationParams
	attemptID string
	cancelled chan struct{}
}

// UserVerificationAttempt identifies one started verification.
type UserVerificationAttempt struct {
	ID        string
	Params    UserVerificationParams
	Cancelled <-chan struct{}
}

// UserVerificationRequests tracks the pending user-verification elicitations of
// one connection. The store is safe for concurrent use because app-server
// requests arrive from several goroutines.
type UserVerificationRequests struct {
	mu      sync.Mutex
	pending map[userVerificationRequestKey]*pendingUserVerification
}

// NoteRequest records the parameters of a user-verification elicitation so the
// resolution path no longer depends on the view's copy.
func (r *UserVerificationRequests) NoteRequest(serverName, requestID string, params UserVerificationParams) {
	if r == nil {
		return
	}
	key := userVerificationRequestKey{serverName: strings.TrimSpace(serverName), requestID: strings.TrimSpace(requestID)}
	if key.requestID == "" && key.serverName == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = map[userVerificationRequestKey]*pendingUserVerification{}
	}
	r.pending[key] = &pendingUserVerification{params: params, cancelled: make(chan struct{})}
}

// Begin starts the one verification attempt a request may have. A request that
// is unknown, already started, or already resolved reports false so duplicate
// attempts can never run (Rust `UserVerificationRequests::begin`).
func (r *UserVerificationRequests) Begin(serverName, requestID string) (UserVerificationAttempt, bool) {
	if r == nil {
		return UserVerificationAttempt{}, false
	}
	key := userVerificationRequestKey{serverName: strings.TrimSpace(serverName), requestID: strings.TrimSpace(requestID)}
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.pending[key]
	if !ok || pending.attemptID != "" {
		return UserVerificationAttempt{}, false
	}
	pending.attemptID = uuid.NewString()
	return UserVerificationAttempt{ID: pending.attemptID, Params: pending.params, Cancelled: pending.cancelled}, true
}

// IsPending reports whether attempt still owns the request. A resolved,
// cancelled, or replaced request makes earlier results stale.
func (r *UserVerificationRequests) IsPending(serverName, requestID, attemptID string) bool {
	if r == nil || strings.TrimSpace(attemptID) == "" {
		return false
	}
	key := userVerificationRequestKey{serverName: strings.TrimSpace(serverName), requestID: strings.TrimSpace(requestID)}
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.pending[key]
	return ok && pending.attemptID == attemptID
}

// Remove forgets a resolved request and cancels any verification still running
// for it (Rust's `Drop for PendingVerification`).
func (r *UserVerificationRequests) Remove(serverName, requestID string) {
	if r == nil {
		return
	}
	key := userVerificationRequestKey{serverName: strings.TrimSpace(serverName), requestID: strings.TrimSpace(requestID)}
	r.mu.Lock()
	pending, ok := r.pending[key]
	delete(r.pending, key)
	r.mu.Unlock()
	if ok {
		close(pending.cancelled)
	}
}

// Clear drops every pending request, cancelling their attempts. A reconnect
// must not let an in-flight verification answer a dropped prompt.
func (r *UserVerificationRequests) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	pending := r.pending
	r.pending = nil
	r.mu.Unlock()
	for _, request := range pending {
		close(request.cancelled)
	}
}
