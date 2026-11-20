package auth

// Cross-process sign-in reservations for provider OAuth credentials.
//
// Rust parity: codex-rs/login/src/oauth_session_login.rs (#62079)
// OAuthLoginAttempt and start_login_if_required. A saved grant is checked while
// the credential store is locked, and a needed sign-in reserves its credential
// before the store lock is released, so two Codex processes cannot both sign in
// for the same credential while different credentials stay independent.

import (
	"context"
	"errors"
	"strings"
)

// GatewayLoginFailureMessage mirrors Rust GATEWAY_LOGIN_FAILURE: the guidance a
// failed provider gateway sign-in reports when the cause carries none of its own.
const GatewayLoginFailureMessage = "Gateway sign-in failed; check the gateway configuration and credential store."

// GatewayLoginAttempt is the exclusive right to sign in for one credential,
// held from the saved-grant check until the sign-in finishes or is canceled.
type GatewayLoginAttempt struct {
	manager *GatewayAuthManager
	lock    *gatewayCredentialLock
}

// LoginIfRequired reuses a saved sign-in, or reserves one for this process. A
// nil attempt with a nil error means the credential already holds a grant, so
// no interactive sign-in is needed.
func (m *GatewayAuthManager) LoginIfRequired() (*GatewayLoginAttempt, error) {
	if m == nil {
		return nil, errors.New("provider OAuth manager is unavailable")
	}
	storeLock, err := lockGatewayCredentials(m.codexHome)
	if err != nil {
		return nil, err
	}
	defer func() { _ = storeLock.release() }()
	token, err := m.loadToken()
	if err != nil {
		return nil, err
	}
	if token != nil && strings.TrimSpace(token.AuthorizationID) != "" {
		return nil, nil
	}
	lock, err := tryLockGatewayLogin(m.codexHome, m.CredentialID())
	if err != nil {
		return nil, err
	}
	return &GatewayLoginAttempt{manager: m, lock: lock}, nil
}

// Close releases the reservation. It is safe to call more than once and must run
// when the sign-in finishes or is canceled.
func (a *GatewayLoginAttempt) Close() error {
	if a == nil || a.lock == nil {
		return nil
	}
	lock := a.lock
	a.lock = nil
	return lock.release()
}

// LoginWithBrowser runs the browser sign-in under this reservation. failureMessage
// is the guidance a failure that carries none of its own reports, which lets a
// config-server sign-in point at config_server.oauth.
func (a *GatewayLoginAttempt) LoginWithBrowser(ctx context.Context, onURL func(string), failureMessage string) error {
	if a == nil || a.manager == nil {
		return errors.New("provider OAuth login reservation is unavailable")
	}
	return a.manager.loginWithBrowser(ctx, onURL, failureMessage)
}

// LoginWithDeviceCode runs the device sign-in under this reservation. cancel is
// checked while waiting for approval, so a canceled attempt releases the
// reservation without saving a grant.
func (a *GatewayLoginAttempt) LoginWithDeviceCode(ctx context.Context, onPrompt func(GatewayDeviceCodePrompt), failureMessage string) error {
	if a == nil || a.manager == nil {
		return errors.New("provider OAuth login reservation is unavailable")
	}
	return a.manager.loginWithDeviceCode(ctx, onPrompt, failureMessage)
}

// gatewaySignInFailure reports a failed sign-in. A canceled attempt keeps the
// canceled error, a device-flow cause keeps the guidance it already carries, and
// anything else reports the caller's message for this credential kind (Rust
// maps a non-interrupted failure to the same message).
func gatewaySignInFailure(ctx context.Context, err error, failureMessage string) error {
	if err == nil {
		return nil
	}
	if (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrGatewayLoginCanceled
	}
	switch {
	case errors.Is(err, ErrGatewayDeviceCodeDenied),
		errors.Is(err, ErrGatewayDeviceCodeExpired),
		errors.Is(err, ErrGatewayDeviceVerificationURL),
		errors.Is(err, ErrGatewayLoginRequired):
		return err
	}
	if strings.TrimSpace(failureMessage) == "" {
		return err
	}
	return errors.New(failureMessage)
}
