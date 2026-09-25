package auth

// Gateway sign-in status and login control.
//
// Rust parity: codex-rs/login/src/gateway_auth_login.rs (#47170) and the
// app-server gateway sign-in RPCs (#47207). One control object is shared by
// every manager for the same CODEX_HOME, so a login started for one provider
// configuration stays visible to readers created after a configuration change.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"weak"
)

// ErrGatewayLoginInProgress mirrors Rust GatewayAuthError::LoginInProgress: only
// one browser sign-in may run at a time.
var ErrGatewayLoginInProgress = errors.New("provider OAuth login is already in progress")

// ErrGatewayLoginCanceled mirrors Rust's canceled sign-in error text.
var ErrGatewayLoginCanceled = errors.New("Gateway sign-in was canceled")

type GatewayAuthStatusKind string

const (
	GatewayAuthStatusNotReady  GatewayAuthStatusKind = "notReady"
	GatewayAuthStatusStarted   GatewayAuthStatusKind = "started"
	GatewayAuthStatusSucceeded GatewayAuthStatusKind = "succeeded"
	GatewayAuthStatusFailed    GatewayAuthStatusKind = "failed"
)

// GatewayAuthStatus is the readiness and browser-authorization progress for one
// gateway credential (Rust login::GatewayAuthStatus).
type GatewayAuthStatus struct {
	Kind    GatewayAuthStatusKind
	Message string
}

// GatewayAuthStatusChange is a credential status change scoped to the
// configured OAuth account (Rust GatewayAuthStatusChange).
type GatewayAuthStatusChange struct {
	Config GatewayAuthConfig
	Status GatewayAuthStatus
}

// terminal reports whether the status replaces a stored credential observation
// (Rust's NotReady | Failed terminal arm).
func (s GatewayAuthStatus) terminal() bool {
	return s.Kind == GatewayAuthStatusNotReady || s.Kind == GatewayAuthStatusFailed
}

// GatewayAuthConfigEqual compares two gateway configurations field by field;
// the struct carries a slice, so it is not directly comparable.
func GatewayAuthConfigEqual(left, right GatewayAuthConfig) bool {
	if left.AuthorizationURL != right.AuthorizationURL ||
		left.TokenURL != right.TokenURL ||
		left.ClientID != right.ClientID ||
		left.Resource != right.Resource {
		return false
	}
	if len(left.Scopes) != len(right.Scopes) {
		return false
	}
	for i := range left.Scopes {
		if left.Scopes[i] != right.Scopes[i] {
			return false
		}
	}
	switch {
	case left.RedirectPort == nil && right.RedirectPort == nil:
		return true
	case left.RedirectPort == nil || right.RedirectPort == nil:
		return false
	default:
		return *left.RedirectPort == *right.RedirectPort
	}
}

type gatewayLoginControl struct {
	mu sync.Mutex
	// status is the last observed status; hasStatus distinguishes the initial
	// (never observed) state from an explicit NotReady.
	status    GatewayAuthStatus
	hasStatus bool
	// credential fingerprints the stored credential the last status refers to,
	// so a persisted replacement supersedes a failed-save observation.
	credential string

	subscribers    map[int]chan GatewayAuthStatusChange
	nextSubscriber int
}

var (
	gatewayLoginControlsMu sync.Mutex
	gatewayLoginControls   []gatewayLoginControlEntry
)

type gatewayLoginControlEntry struct {
	codexHome string
	control   *gatewayLoginControl
	// manager keeps the entry alive only while some manager or subscriber uses
	// it; the control is otherwise recreated on demand (Rust's retain).
	manager weak.Pointer[GatewayAuthManager]
}

// gatewayLoginControlFor returns the control shared by every manager for this
// codex home, creating it on first use.
func gatewayLoginControlFor(codexHome string, manager *GatewayAuthManager) *gatewayLoginControl {
	home := strings.TrimSpace(codexHome)
	gatewayLoginControlsMu.Lock()
	defer gatewayLoginControlsMu.Unlock()
	live := gatewayLoginControls[:0]
	for _, entry := range gatewayLoginControls {
		useful := entry.manager.Value() != nil || entry.control.subscriberCount() > 0
		if useful {
			live = append(live, entry)
		}
	}
	gatewayLoginControls = live
	for _, entry := range gatewayLoginControls {
		if entry.codexHome == home {
			return entry.control
		}
	}
	control := &gatewayLoginControl{subscribers: map[int]chan GatewayAuthStatusChange{}}
	var pointer weak.Pointer[GatewayAuthManager]
	if manager != nil {
		pointer = weak.Make(manager)
	}
	gatewayLoginControls = append(gatewayLoginControls, gatewayLoginControlEntry{
		codexHome: home,
		control:   control,
		manager:   pointer,
	})
	return control
}

func (c *gatewayLoginControl) subscriberCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subscribers)
}

// SubscribeStatus returns a channel receiving status changes and a function
// releasing the subscription.
func (m *GatewayAuthManager) SubscribeStatus() (<-chan GatewayAuthStatusChange, func()) {
	if m == nil || m.control == nil {
		closed := make(chan GatewayAuthStatusChange)
		close(closed)
		return closed, func() {}
	}
	control := m.control
	control.mu.Lock()
	id := control.nextSubscriber
	control.nextSubscriber++
	channel := make(chan GatewayAuthStatusChange, 16)
	control.subscribers[id] = channel
	control.mu.Unlock()
	return channel, func() {
		control.mu.Lock()
		if existing, ok := control.subscribers[id]; ok {
			delete(control.subscribers, id)
			close(existing)
		}
		control.mu.Unlock()
	}
}

// publishStatus records a status observation and reports whether it changed.
// The credential fingerprint lets a persisted replacement supersede a
// failed-save or rejected-token baseline (Rust publish_status).
func (m *GatewayAuthManager) publishStatus(status GatewayAuthStatus, credential string) bool {
	if m == nil || m.control == nil {
		return false
	}
	control := m.control
	control.mu.Lock()
	changed := !control.hasStatus || control.status != status
	control.status = status
	control.credential = credential
	control.hasStatus = true
	if changed {
		control.mu.Unlock()
		control.broadcast(GatewayAuthStatusChange{Config: m.config, Status: status})
		return true
	}
	control.mu.Unlock()
	return false
}

func (c *gatewayLoginControl) broadcast(change GatewayAuthStatusChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, channel := range c.subscribers {
		select {
		case channel <- change:
		default:
		}
	}
}

// snapshotStatus returns the last observed status, downgrading an expired
// success so a stale observation cannot hide a missing credential (Rust
// snapshot_status).
func (m *GatewayAuthManager) snapshotStatus() GatewayAuthStatus {
	if m == nil || m.control == nil {
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}
	}
	control := m.control
	control.mu.Lock()
	if !control.hasStatus {
		control.mu.Unlock()
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}
	}
	status := control.status
	expired := false
	if status.Kind == GatewayAuthStatusSucceeded {
		usable := false
		if token, err := m.loadToken(); err == nil && token != nil {
			usable = gatewayTokenIsUsable(*token)
		}
		if !usable {
			status = GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}
			control.status = status
			expired = true
		}
	}
	control.mu.Unlock()
	if expired {
		control.broadcast(GatewayAuthStatusChange{Config: m.config, Status: status})
	}
	return status
}

// Status reads credential readiness without refreshing credentials, opening a
// browser, or waiting for a login (Rust GatewayAuthManager::status).
func (m *GatewayAuthManager) Status() (GatewayAuthStatus, error) {
	if m == nil {
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, errors.New("provider OAuth manager is unavailable")
	}
	if err := validateGatewayAuthConfig(m.config); err != nil {
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, err
	}
	if m.control != nil {
		m.control.mu.Lock()
		started := m.control.hasStatus && m.control.status.Kind == GatewayAuthStatusStarted
		terminal := GatewayAuthStatus{}
		hasTerminal := m.control.hasStatus && m.control.status.terminal()
		terminalCredential := m.control.credential
		if hasTerminal {
			terminal = m.control.status
		}
		m.control.mu.Unlock()
		if started {
			return GatewayAuthStatus{Kind: GatewayAuthStatusStarted}, nil
		}
		stored, err := m.loadToken()
		if err != nil {
			return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, err
		}
		fingerprint := gatewayTokenFingerprint(stored)
		usable := stored != nil && gatewayTokenIsUsable(*stored)
		if usable && (!hasTerminal || fingerprint != terminalCredential) {
			// Only a persisted replacement supersedes a failed-save baseline or
			// a rejected token.
			m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusSucceeded}, fingerprint)
			return GatewayAuthStatus{Kind: GatewayAuthStatusSucceeded}, nil
		}
		if hasTerminal {
			return terminal, nil
		}
		m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, fingerprint)
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, nil
	}
	stored, err := m.loadToken()
	if err != nil {
		return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, err
	}
	if stored != nil && gatewayTokenIsUsable(*stored) {
		return GatewayAuthStatus{Kind: GatewayAuthStatusSucceeded}, nil
	}
	return GatewayAuthStatus{Kind: GatewayAuthStatusNotReady}, nil
}

// gatewayTokenFingerprint identifies a stored credential in status
// observations without retaining the secret itself.
func gatewayTokenFingerprint(token *gatewayStoredToken) string {
	if token == nil {
		return ""
	}
	return token.AccessToken
}

// LoginWithBrowser runs the caller-initiated browser sign-in. onURL receives the
// authorization URL for the caller to hand to the client; completion means the
// credential was saved and is visible to existing request consumers (Rust
// GatewayAuthManager::login_with_browser).
func (m *GatewayAuthManager) LoginWithBrowser(ctx context.Context, onURL func(string)) error {
	if m == nil {
		return errors.New("provider OAuth manager is unavailable")
	}
	if err := validateGatewayAuthConfig(m.config); err != nil {
		return err
	}
	if !m.beginLoginAttempt() {
		return ErrGatewayLoginInProgress
	}
	defer m.endLoginAttempt()
	if ctx == nil {
		ctx = context.Background()
	}
	m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusStarted}, "")
	// The cache mutex is held for the whole sign-in, so concurrent inference
	// resolves wait for the credential instead of racing a second browser flow
	// (Rust holds cached_token.lock_owned() across login_with_browser).
	m.mu.Lock()
	defer m.mu.Unlock()
	accessToken, err := m.authorizeWithURL(ctx, onURL)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			err = ErrGatewayLoginCanceled
		}
		m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusFailed, Message: err.Error()}, "")
		return err
	}
	m.publishStatus(GatewayAuthStatus{Kind: GatewayAuthStatusSucceeded}, accessToken)
	return nil
}

func (m *GatewayAuthManager) beginLoginAttempt() bool {
	return m.loginAttempt.CompareAndSwap(false, true)
}

func (m *GatewayAuthManager) endLoginAttempt() {
	m.loginAttempt.Store(false)
}
