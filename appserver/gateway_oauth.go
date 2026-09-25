package appserver

// Explicit gateway sign-in RPCs.
//
// Rust parity: codex-rs/app-server/src/request_processors/account_processor/
// gateway_oauth.rs plus gateway_oauth_notifications.rs (#47170/#47207). The
// sign-in shares credentials with inference, and cancellation is bound to the
// connection that started it.

import (
	"context"
	"errors"
	"strings"

	"codex_go/auth"
	"codex_go/model"
	"codex_go/turn"
)

// GatewayOAuthStatus mirrors the app-server wire enum (camelCase).
type GatewayOAuthStatus string

const (
	GatewayOAuthStatusNotReady  GatewayOAuthStatus = "notReady"
	GatewayOAuthStatusStarted   GatewayOAuthStatus = "started"
	GatewayOAuthStatusSucceeded GatewayOAuthStatus = "succeeded"
	GatewayOAuthStatusFailed    GatewayOAuthStatus = "failed"
)

// GatewayOAuthChangedNotification forwards OAuth progress for the app's
// current gateway. auth_url is an authorization handoff and is only sent to the
// connection that started the login.
type GatewayOAuthChangedNotification struct {
	AuthURL    *string            `json:"authUrl"`
	ProviderID string             `json:"providerId"`
	Status     GatewayOAuthStatus `json:"status"`
	Error      *string            `json:"error"`
}

// GatewayOAuthReadResponse is the current effective gateway policy and
// credential readiness; it never contains credentials.
type GatewayOAuthReadResponse struct {
	ProviderID   string              `json:"providerId"`
	ProviderName string              `json:"providerName"`
	Required     bool                `json:"required"`
	Status       *GatewayOAuthStatus `json:"status"`
	Error        *string             `json:"error"`
}

type GatewayOAuthLoginResponse struct{}

type GatewayOAuthCancelResponse struct{}

// activeGatewayLogin tracks the one in-flight caller-initiated sign-in and the
// connection that owns it.
type activeGatewayLogin struct {
	connectionID string
	cancel       context.CancelFunc
	done         chan struct{}
}

// gatewayOAuthProvider resolves the effective provider's gateway credential
// manager. A nil manager means the selected provider does not use gateway OAuth.
func (r *RuntimeRouter) gatewayOAuthProvider() (string, string, *auth.GatewayAuthManager, error) {
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{})
	if err != nil {
		return "", "", nil, errors.New("failed to load gateway configuration: " + err.Error())
	}
	providerID := firstNonEmpty(stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil {
		return providerID, "", nil, errors.New("failed to load gateway configuration: unknown model provider")
	}
	provider := model.CreateRuntimeProviderWithResidency(providerID, *providerInfo, nil, managedResidencyForConfig(cfg))
	manager, err := provider.GatewayAuthManager()
	if err != nil {
		return providerID, providerInfo.Name, nil, err
	}
	return providerID, providerInfo.Name, manager, nil
}

func (r *RuntimeRouter) handleGatewayOAuthRead() (*GatewayOAuthReadResponse, error) {
	providerID, providerName, manager, err := r.gatewayOAuthProvider()
	if err != nil {
		return nil, err
	}
	if manager == nil {
		return &GatewayOAuthReadResponse{ProviderID: providerID, ProviderName: providerName}, nil
	}
	status, err := manager.Status()
	if err != nil {
		return nil, err
	}
	wireStatus := gatewayOAuthWireStatus(status)
	response := &GatewayOAuthReadResponse{
		ProviderID:   providerID,
		ProviderName: providerName,
		Required:     true,
		Status:       &wireStatus,
	}
	if strings.TrimSpace(status.Message) != "" {
		message := status.Message
		response.Error = &message
	}
	return response, nil
}

func gatewayOAuthWireStatus(status auth.GatewayAuthStatus) GatewayOAuthStatus {
	switch status.Kind {
	case auth.GatewayAuthStatusStarted:
		return GatewayOAuthStatusStarted
	case auth.GatewayAuthStatusSucceeded:
		return GatewayOAuthStatusSucceeded
	case auth.GatewayAuthStatusFailed:
		return GatewayOAuthStatusFailed
	default:
		return GatewayOAuthStatusNotReady
	}
}

func (r *RuntimeRouter) handleGatewayOAuthLogin(request *Request) (*GatewayOAuthLoginResponse, error) {
	connectionID := ""
	if request != nil {
		connectionID = strings.TrimSpace(request.ConnectionID)
	}
	providerID, _, manager, err := r.gatewayOAuthProvider()
	if err != nil {
		return nil, err
	}
	if manager == nil {
		return nil, jsonRPCInvalidRequest("The current provider does not use gateway OAuth")
	}
	ctx, cancel := context.WithCancel(context.Background())
	active := &activeGatewayLogin{connectionID: connectionID, cancel: cancel, done: make(chan struct{})}
	r.gatewayLoginMu.Lock()
	if r.gatewayLogin != nil {
		r.gatewayLoginMu.Unlock()
		cancel()
		return nil, jsonRPCInvalidRequest("Gateway sign-in is already in progress")
	}
	r.gatewayLogin = active
	r.gatewayLoginMu.Unlock()
	defer func() {
		r.gatewayLoginMu.Lock()
		if r.gatewayLogin == active {
			r.gatewayLogin = nil
		}
		r.gatewayLoginMu.Unlock()
		close(active.done)
	}()

	loginErr := manager.LoginWithBrowser(ctx, func(url string) {
		authURL := url
		r.notifyToConnection(connectionID, NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
			AuthURL:    &authURL,
			ProviderID: providerID,
			Status:     GatewayOAuthStatusStarted,
		})
	})
	if loginErr != nil {
		message := loginErr.Error()
		r.notify(NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
			ProviderID: providerID,
			Status:     GatewayOAuthStatusFailed,
			Error:      &message,
		})
		return nil, errors.New(message)
	}
	r.notify(NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
		ProviderID: providerID,
		Status:     GatewayOAuthStatusSucceeded,
	})
	return &GatewayOAuthLoginResponse{}, nil
}

func (r *RuntimeRouter) handleGatewayOAuthCancel(request *Request) (*GatewayOAuthCancelResponse, error) {
	connectionID := ""
	if request != nil {
		connectionID = strings.TrimSpace(request.ConnectionID)
	}
	r.gatewayLoginMu.Lock()
	active := r.gatewayLogin
	if active == nil {
		r.gatewayLoginMu.Unlock()
		return &GatewayOAuthCancelResponse{}, nil
	}
	if active.connectionID != connectionID {
		r.gatewayLoginMu.Unlock()
		return nil, jsonRPCInvalidRequest("Gateway sign-in belongs to another connection")
	}
	active.cancel()
	done := active.done
	r.gatewayLoginMu.Unlock()
	// Cancellation is acknowledged only after the active login releases its slot.
	<-done
	return &GatewayOAuthCancelResponse{}, nil
}

// cancelGatewayLoginForConnection cancels the in-flight sign-in owned by a
// closed connection (Rust gateway_connection_closed).
func (r *RuntimeRouter) cancelGatewayLoginForConnection(connectionID string) {
	if r == nil {
		return
	}
	connectionID = strings.TrimSpace(connectionID)
	r.gatewayLoginMu.Lock()
	active := r.gatewayLogin
	r.gatewayLoginMu.Unlock()
	if active != nil && active.connectionID == connectionID {
		active.cancel()
	}
}
