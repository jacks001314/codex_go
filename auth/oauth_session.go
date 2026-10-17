package auth

import (
	"net/http"

	"codex_go/keyring"
)

// The gateway credential manager is a general OAuth session manager. These
// aliases preserve the existing gateway names while exposing the reusable API
// introduced by Rust #54070.
type OAuthSessionConfig = GatewayAuthConfig
type OAuthSessionManager = GatewayAuthManager
type OAuthSessionStatus = GatewayAuthStatus
type OAuthSessionStatusKind = GatewayAuthStatusKind
type OAuthSessionStatusChange = GatewayAuthStatusChange
type OAuthLoginControl = gatewayLoginControl
type OAuthSessionStorage = gatewayAuthStorage

const (
	OAuthSessionStatusNotReady  = GatewayAuthStatusNotReady
	OAuthSessionStatusStarted   = GatewayAuthStatusStarted
	OAuthSessionStatusSucceeded = GatewayAuthStatusSucceeded
	OAuthSessionStatusFailed    = GatewayAuthStatusFailed
)

// OAuthSessionError wraps a credential state that requires user action rather
// than a request retry.
type OAuthSessionError struct {
	Err error
}

func (e *OAuthSessionError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *OAuthSessionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// OAuthSessionStatusSnapshot is a credential-free readiness snapshot.
type OAuthSessionStatusSnapshot struct {
	Status OAuthSessionStatus
}

// NewOAuthSessionManager creates a configured OAuth session using the same
// redirect-free HTTP and encrypted-store behavior as the gateway manager.
func NewOAuthSessionManager(config OAuthSessionConfig, codexHome string, client *http.Client, store keyring.Store) *OAuthSessionManager {
	return NewGatewayAuthManager(config, codexHome, client, store)
}

// SubscribeOAuthSessionStatus subscribes to status changes for one OAuth
// session manager's shared host control.
func SubscribeOAuthSessionStatus(manager *OAuthSessionManager) (<-chan OAuthSessionStatusChange, func()) {
	if manager == nil {
		closed := make(chan OAuthSessionStatusChange)
		close(closed)
		return closed, func() {}
	}
	return manager.SubscribeStatus()
}
