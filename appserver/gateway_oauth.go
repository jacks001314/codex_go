package appserver

// Explicit gateway sign-in RPCs.
//
// Rust parity: codex-rs/app-server/src/request_processors/account_processor/
// gateway_oauth.rs plus gateway_oauth_notifications.rs (#47170/#47207). The
// sign-in shares credentials with inference, and cancellation is bound to the
// connection that started it.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"codex_go/auth"
	"codex_go/config"
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
	// LoginID correlates a device-authorization completion with the prompt that
	// started it; other notifications leave it null (Rust #60486).
	LoginID    *string            `json:"loginId"`
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
	// SupportsDeviceAuth reports whether the provider configures a device
	// authorization endpoint (Rust #60486).
	SupportsDeviceAuth bool                `json:"supportsDeviceAuth"`
	Status             *GatewayOAuthStatus `json:"status"`
	Error              *string             `json:"error"`
}

// GatewayOAuthReadParams selects the configured provider whose gateway
// credential readiness is read. A nil provider keeps the current provider
// (Rust #59819).
type GatewayOAuthReadParams struct {
	ModelProvider *string `json:"modelProvider,omitempty"`
}

// GatewayOAuthLoginParams selects the configured provider to authorize. The
// default provider and any thread's provider stay unchanged.
type GatewayOAuthLoginParams struct {
	ModelProvider *string `json:"modelProvider,omitempty"`
	// DeviceAuth asks for the device-authorization flow (Rust #60486).
	DeviceAuth bool `json:"deviceAuth,omitempty"`
}

// GatewayOAuthPrompt is the device-authorization prompt a caller must show
// before the grant is approved.
type GatewayOAuthPrompt struct {
	LoginID         string `json:"loginId"`
	VerificationURL string `json:"verificationUrl"`
	UserCode        string `json:"userCode"`
}

// GatewayOAuthLoginResponse carries a device prompt when one is needed and
// stays null when saved credentials already satisfy the gateway.
type GatewayOAuthLoginResponse struct {
	Prompt *GatewayOAuthPrompt `json:"prompt"`
}

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
func (r *RuntimeRouter) gatewayOAuthProvider(modelProvider string) (string, string, *auth.GatewayAuthManager, error) {
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{})
	if err != nil {
		return "", "", nil, errors.New("failed to load gateway configuration: " + err.Error())
	}
	explicit := strings.TrimSpace(modelProvider)
	providerID := firstNonEmpty(explicit, stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	if explicit != "" {
		// Rust #59819 validates an explicit selector against current managed
		// policy without changing the default or any thread's provider.
		if err := r.validateGatewayOAuthProviderSelection(cfg, explicit); err != nil {
			return "", "", nil, err
		}
	}
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

// validateGatewayOAuthProviderSelection applies the managed provider
// requirement and the configured-provider catalog to an explicit selector.
func (r *RuntimeRouter) validateGatewayOAuthProviderSelection(cfg *config.Config, providerID string) error {
	requirements, err := r.managedModelProviderRequirements()
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if requirements != nil && requirements.ModelProvider != nil && *requirements.ModelProvider != providerID {
		return configLoadInvalidRequest(&ModelProviderError{
			Kind:                  ModelProviderPolicyConflict,
			ModelProvider:         providerID,
			RequiredModelProvider: *requirements.ModelProvider,
		})
	}
	providers, err := model.ProvidersFromConfigForCWD(configValues(cfg), stringConfigValue(cfg, "openai_base_url"), "")
	if err != nil {
		return configLoadInvalidRequest(err)
	}
	if _, ok := providers[providerID]; !ok {
		return configLoadInvalidRequest(&ModelProviderError{Kind: ModelProviderNotConfigured, ModelProvider: providerID})
	}
	return nil
}

// gatewayOAuthProviderSelection decodes the optional modelProvider parameter.
// An absent, null, or empty selector keeps the current provider.
func gatewayOAuthProviderSelection(request *Request) (string, error) {
	if request == nil {
		return "", nil
	}
	raw := strings.TrimSpace(string(request.Params))
	if raw == "" || raw == "null" {
		return "", nil
	}
	var params struct {
		ModelProvider *string `json:"modelProvider"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return "", jsonRPCInvalidRequest("Invalid params: " + err.Error())
	}
	if params.ModelProvider == nil {
		return "", nil
	}
	return strings.TrimSpace(*params.ModelProvider), nil
}

// gatewayOAuthLoginDeviceAuth decodes the optional deviceAuth flag.
func gatewayOAuthLoginDeviceAuth(request *Request) (bool, error) {
	if request == nil {
		return false, nil
	}
	raw := strings.TrimSpace(string(request.Params))
	if raw == "" || raw == "null" {
		return false, nil
	}
	var params GatewayOAuthLoginParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return false, jsonRPCInvalidRequest("Invalid params: " + err.Error())
	}
	return params.DeviceAuth, nil
}

// gatewayOAuthCredentialProviderIDs lists every configured provider whose
// gateway OAuth configuration shares the selected provider's credential, so a
// sign-in reports readiness for all of them (Rust #59819).
func gatewayOAuthCredentialProviderIDs(values map[string]any, openAIBaseURL string, providerID string) []string {
	providers, err := model.ProvidersFromConfigForCWD(values, openAIBaseURL, "")
	if err != nil {
		return nil
	}
	base, ok := providers[providerID]
	if !ok || base.GatewayOAuth == nil {
		return nil
	}
	ids := make([]string, 0, len(providers))
	for id, info := range providers {
		if info.GatewayOAuth == nil || !reflect.DeepEqual(info.GatewayOAuth, base.GatewayOAuth) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *RuntimeRouter) handleGatewayOAuthRead(request *Request) (*GatewayOAuthReadResponse, error) {
	selection, err := gatewayOAuthProviderSelection(request)
	if err != nil {
		return nil, err
	}
	providerID, providerName, manager, err := r.gatewayOAuthProvider(selection)
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
		ProviderID:         providerID,
		ProviderName:       providerName,
		Required:           true,
		SupportsDeviceAuth: manager.SupportsDeviceAuthorization(),
		Status:             &wireStatus,
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
	selection, err := gatewayOAuthProviderSelection(request)
	if err != nil {
		return nil, err
	}
	deviceAuth, err := gatewayOAuthLoginDeviceAuth(request)
	if err != nil {
		return nil, err
	}
	providerID, _, manager, err := r.gatewayOAuthProvider(selection)
	if err != nil {
		return nil, err
	}
	if manager == nil {
		if selection != "" {
			return nil, jsonRPCInvalidRequest("The selected provider does not use gateway OAuth")
		}
		return nil, jsonRPCInvalidRequest("The current provider does not use gateway OAuth")
	}
	// Rust #59819 reports readiness for every configured provider sharing this
	// credential, so a client watching another provider learns it is ready too.
	credentialProviders := []string{providerID}
	if cfg, cfgErr := r.effectiveConfigForTurn(&turn.TurnStartParams{}); cfgErr == nil && cfg != nil {
		if shared := gatewayOAuthCredentialProviderIDs(configValues(cfg), stringConfigValue(cfg, "openai_base_url"), providerID); len(shared) > 0 {
			credentialProviders = shared
		}
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
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			r.gatewayLoginMu.Lock()
			if r.gatewayLogin == active {
				r.gatewayLogin = nil
			}
			r.gatewayLoginMu.Unlock()
			close(active.done)
		})
	}
	if deviceAuth {
		// Rust #60486 answers with the device prompt before approval and keeps
		// the sign-in slot owned until the grant completes.
		return r.loginGatewayWithDeviceCode(ctx, cancel, release, manager, providerID, connectionID, credentialProviders)
	}
	defer release()

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
		for _, id := range credentialProviders {
			r.notify(NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
				ProviderID: id,
				Status:     GatewayOAuthStatusFailed,
				Error:      &message,
			})
		}
		return nil, errors.New(message)
	}
	for _, id := range credentialProviders {
		r.notify(NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
			ProviderID: id,
			Status:     GatewayOAuthStatusSucceeded,
		})
	}
	return &GatewayOAuthLoginResponse{}, nil
}

// loginGatewayWithDeviceCode starts the device flow, answers with its prompt,
// and reports the outcome to the initiating connection under the same login id
// (Rust #60486). Saved credentials are reused before another grant is asked
// for.
func (r *RuntimeRouter) loginGatewayWithDeviceCode(
	ctx context.Context,
	cancel context.CancelFunc,
	release func(),
	manager *auth.GatewayAuthManager,
	providerID string,
	connectionID string,
	credentialProviders []string,
) (*GatewayOAuthLoginResponse, error) {
	if !manager.SupportsDeviceAuthorization() {
		cancel()
		release()
		return nil, jsonRPCInvalidRequest("The gateway does not support device authorization")
	}
	if _, err := manager.ResolveAccessTokenWithoutLogin(ctx); err == nil {
		cancel()
		release()
		return &GatewayOAuthLoginResponse{}, nil
	} else if !errors.Is(err, auth.ErrGatewayLoginRequired) {
		cancel()
		release()
		return nil, errors.New(err.Error())
	}
	loginID := uuid.NewString()
	prompts := make(chan GatewayOAuthPrompt, 1)
	go func() {
		defer release()
		loginErr := manager.LoginWithDeviceCode(ctx, func(prompt auth.GatewayDeviceCodePrompt) {
			select {
			case prompts <- GatewayOAuthPrompt{LoginID: loginID, VerificationURL: prompt.VerificationURL, UserCode: prompt.UserCode}:
			default:
			}
		})
		status := GatewayOAuthStatusSucceeded
		var message *string
		if loginErr != nil {
			status = GatewayOAuthStatusFailed
			text := loginErr.Error()
			message = &text
		}
		r.notifyToConnection(connectionID, NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
			LoginID:    &loginID,
			ProviderID: providerID,
			Status:     status,
			Error:      message,
		})
		for _, id := range credentialProviders {
			if id == providerID {
				continue
			}
			r.notify(NotificationGatewayOAuthChanged, &GatewayOAuthChangedNotification{
				ProviderID: id,
				Status:     status,
				Error:      message,
			})
		}
	}()
	select {
	case prompt := <-prompts:
		return &GatewayOAuthLoginResponse{Prompt: &prompt}, nil
	case <-ctx.Done():
		return nil, jsonRPCInvalidRequest("Gateway sign-in cancelled")
	}
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
