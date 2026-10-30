package auth

import (
	"strings"
	"sync"
)

// AuthChangeState tracks credential changes separately from ownership changes,
// before notifications coalesce (Rust codex-login AuthChangeState, #43428).
// Revisions are opaque and local to one tracker; consumers must reset state on
// reconnect.
type AuthChangeState struct {
	// Generation advances whenever cached credentials change.
	Generation uint64 `json:"generation"`
	// OwnerGeneration advances on login, logout, or changes of user, workspace,
	// or auth mode. Credential changes with incomplete owner identity also
	// advance this revision. Losing login metadata alone preserves the account
	// owner.
	OwnerGeneration uint64 `json:"ownerGeneration"`
	// LoginGeneration advances on a new login or logout, including the loss of
	// login metadata that leaves the account owner unchanged. Ordinary refresh
	// within an identifiable OAuth session preserves it (Rust #55546).
	LoginGeneration uint64 `json:"loginGeneration"`
}

// SameAuthOwner mirrors Rust's same_owner: a credential change keeps the same
// owner only when the auth mode is unchanged and both snapshots carry the same
// non-empty ChatGPT user and workspace pair.
func SameAuthOwner(previous *AuthDotJSON, current *AuthDotJSON) bool {
	if previous == nil || current == nil {
		return false
	}
	if previous.Mode() != current.Mode() {
		return false
	}
	user := strings.TrimSpace(ChatGPTUserIDFromAuth(previous))
	workspace := strings.TrimSpace(AccountIDFromAuthForRestrictions(previous))
	if user == "" || workspace == "" {
		return false
	}
	currentLogin := LoginOwnerIDFromAuth(current)
	if currentLogin != "" && LoginOwnerIDFromAuth(previous) != currentLogin {
		return false
	}
	return strings.TrimSpace(ChatGPTUserIDFromAuth(current)) == user &&
		strings.TrimSpace(AccountIDFromAuthForRestrictions(current)) == workspace
}

// SameAuthLogin mirrors Rust's same_login: the same account owner and the same
// recorded login owner. Losing login metadata invalidates the login while
// preserving the account owner (Rust #55546).
func SameAuthLogin(previous *AuthDotJSON, current *AuthDotJSON) bool {
	return SameAuthOwner(previous, current) &&
		LoginOwnerIDFromAuth(previous) == LoginOwnerIDFromAuth(current)
}

// LoginOwnerIDFromAuth returns the OAuth login owner recorded on the credential,
// or an empty string for legacy records that predate login-scoped grants.
func LoginOwnerIDFromAuth(snapshot *AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	return strings.TrimSpace(stringFromAny(snapshot.Tokens, "login_owner_id"))
}

// AuthChangeTracker records credential and ownership revisions for one auth
// manager and lets consumers wait for the next change (Rust AuthManager's
// auth_change_state_receiver).
type AuthChangeTracker struct {
	mu       sync.Mutex
	state    AuthChangeState
	lastAuth *AuthDotJSON
	changed  chan struct{}
}

// NewAuthChangeTracker seeds the tracker with the already-cached credential
// snapshot, which does not itself advance a revision (Rust constructs the
// manager with its auth before any change is observed).
func NewAuthChangeTracker(initial *AuthDotJSON) *AuthChangeTracker {
	return &AuthChangeTracker{
		lastAuth: cloneAuthDotJSON(initial),
		changed:  make(chan struct{}),
	}
}

// NoteAuth records the current credential snapshot and returns the resulting
// change state. Generation advances only when the credentials change for
// refresh purposes; OwnerGeneration additionally advances when the credential
// owner changes. Unchanged credentials leave both revisions untouched.
func (t *AuthChangeTracker) NoteAuth(current *AuthDotJSON) AuthChangeState {
	if t == nil {
		return AuthChangeState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changedForRefresh := !AuthsEqualForRefresh(t.lastAuth, current)
	if changedForRefresh {
		t.state.Generation++
		if !SameAuthOwner(t.lastAuth, current) {
			t.state.OwnerGeneration++
		}
		if !SameAuthLogin(t.lastAuth, current) {
			t.state.LoginGeneration++
		}
	}
	t.lastAuth = cloneAuthDotJSON(current)
	state := t.state
	if changedForRefresh {
		close(t.changed)
		t.changed = make(chan struct{})
	}
	return state
}

// ForceOwnerChange advances both revisions without a credential comparison.
// Rust's logout/account-switch paths record an ownership change even when the
// resolved credential snapshot looks indistinguishable.
func (t *AuthChangeTracker) ForceOwnerChange() AuthChangeState {
	if t == nil {
		return AuthChangeState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Generation++
	t.state.OwnerGeneration++
	t.state.LoginGeneration++
	close(t.changed)
	t.changed = make(chan struct{})
	return t.state
}

// InvalidatePendingReads advances only the credential revision without a
// snapshot comparison, so an in-flight reload result can no longer be
// published. Logout uses it even when the cached credential is already empty
// (Rust #55646 invalidate_pending_auth_reads).
func (t *AuthChangeTracker) InvalidatePendingReads() AuthChangeState {
	if t == nil {
		return AuthChangeState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Generation++
	close(t.changed)
	t.changed = make(chan struct{})
	return t.state
}

// Snapshot returns the current revisions.
func (t *AuthChangeTracker) Snapshot() AuthChangeState {
	if t == nil {
		return AuthChangeState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Changed returns a channel that is closed when the next credential change is
// recorded.
func (t *AuthChangeTracker) Changed() <-chan struct{} {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.changed == nil {
		t.changed = make(chan struct{})
	}
	return t.changed
}

// SeedLastAuth records the credential snapshot without advancing a revision.
// Managers that resolve their initial credential after construction use it so
// the first observed change still counts as generation 1.
func (t *AuthChangeTracker) SeedLastAuth(current *AuthDotJSON) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastAuth = cloneAuthDotJSON(current)
}

// RequestAuthChange reports whether request preparation can use the credentials
// it selected (Rust #58121 RequestAuthChange).
type RequestAuthChange int

const (
	// RequestAuthUnchanged keeps the prepared request.
	RequestAuthUnchanged RequestAuthChange = iota
	// RequestCredentialsChanged means preparation must repeat against refreshed
	// credentials.
	RequestCredentialsChanged
	// RequestAuthOwnerChanged means the account identity changed mid-preparation
	// and the request must not be sent.
	RequestAuthOwnerChanged
)

// CompareRequestAuthChange classifies a completed preparation attempt against
// its starting state (Rust AuthChangeState::compare_for_request). Either state
// becoming absent means the provider gained or lost its auth manager during
// setup, which is an owner change.
func CompareRequestAuthChange(before, after *AuthChangeState) RequestAuthChange {
	switch {
	case before == nil && after == nil:
		return RequestAuthUnchanged
	case before == nil || after == nil:
		return RequestAuthOwnerChanged
	case after.OwnerGeneration != before.OwnerGeneration:
		return RequestAuthOwnerChanged
	case after.Generation != before.Generation:
		return RequestCredentialsChanged
	default:
		return RequestAuthUnchanged
	}
}
