package model

// Composes primary provider credentials with independently managed gateway OAuth
// credentials.
//
// Rust parity: model-provider/src/combined_auth.rs plus
// model-provider/src/shared_state.rs::gateway_auth (#46490).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"weak"

	"golang.org/x/net/http/httpguts"

	"codex_go/auth"
)

// gatewayAuthTokenProvider resolves the gateway access token for a provider.
type gatewayAuthTokenProvider interface {
	ResolveAccessToken(ctx context.Context) (string, error)
}

// gatewayAuthConfigFromInfo maps the provider's gateway configuration onto the
// OAuth runtime configuration.
func gatewayAuthConfigFromInfo(config *GatewayOAuthConfig) auth.GatewayAuthConfig {
	resource := ""
	if config.Resource != nil {
		resource = strings.TrimSpace(*config.Resource)
	}
	return auth.GatewayAuthConfig{
		AuthorizationURL: strings.TrimSpace(config.AuthorizationURL),
		TokenURL:         strings.TrimSpace(config.TokenURL),
		ClientID:         strings.TrimSpace(config.ClientID),
		Resource:         resource,
		Scopes:           append([]string(nil), config.Scopes...),
		RedirectPort:     config.RedirectPort,
	}
}

type gatewayManagerEntry struct {
	config    auth.GatewayAuthConfig
	codexHome string
	manager   weak.Pointer[auth.GatewayAuthManager]
}

var (
	gatewayManagersMu sync.Mutex
	gatewayManagers   []gatewayManagerEntry
)

// sharedGatewayAuthManager mirrors Rust `process_shared_state().gateway_auth`:
// matching provider instances and model discovery share one credential manager,
// so they observe the same refreshed tokens. The registry keeps weak references
// and drops dead entries, so a manager nobody holds is recreated on demand.
func sharedGatewayAuthManager(config *GatewayOAuthConfig, codexHome string) (*auth.GatewayAuthManager, error) {
	if config == nil {
		return nil, nil
	}
	oauth := gatewayAuthConfigFromInfo(config)
	runtimeHome := strings.TrimSpace(codexHome)
	gatewayManagersMu.Lock()
	defer gatewayManagersMu.Unlock()
	live := gatewayManagers[:0]
	for _, entry := range gatewayManagers {
		if entry.manager.Value() != nil {
			live = append(live, entry)
		}
	}
	gatewayManagers = live
	for _, entry := range gatewayManagers {
		if entry.codexHome != runtimeHome || !gatewayAuthConfigEqual(entry.config, oauth) {
			continue
		}
		if manager := entry.manager.Value(); manager != nil {
			return manager, nil
		}
	}
	manager := auth.NewGatewayAuthManager(oauth, runtimeHome, nil, nil)
	gatewayManagers = append(gatewayManagers, gatewayManagerEntry{
		config:    oauth,
		codexHome: runtimeHome,
		manager:   weak.Make(manager),
	})
	return manager, nil
}

func gatewayAuthConfigEqual(left auth.GatewayAuthConfig, right auth.GatewayAuthConfig) bool {
	if left.AuthorizationURL != right.AuthorizationURL ||
		left.TokenURL != right.TokenURL ||
		left.ClientID != right.ClientID ||
		left.Resource != right.Resource {
		return false
	}
	if len(left.Scopes) != len(right.Scopes) {
		return false
	}
	for index := range left.Scopes {
		if left.Scopes[index] != right.Scopes[index] {
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

// composeGatewayAuth mirrors Rust `compose_auth`: the gateway token is attached
// through the configured header or cookie while primary authentication stays
// authoritative. Issuer errors are replaced with a bounded message because they
// can echo credentials from configured URLs.
func composeGatewayAuth(ctx context.Context, config *GatewayOAuthConfig, provider gatewayAuthTokenProvider, primary AuthHeaders) (AuthHeaders, error) {
	if config == nil {
		return primary, nil
	}
	if provider == nil {
		return AuthHeaders{}, errors.New("gateway_oauth requires auth runtime configuration")
	}
	token, err := provider.ResolveAccessToken(ctx)
	if err != nil {
		return AuthHeaders{}, errors.New("Gateway OAuth authentication failed; check the gateway configuration and credential store.")
	}
	name, value, err := gatewayAuthHeader(config, token)
	if err != nil {
		return AuthHeaders{}, err
	}
	if headerExists(primary.Headers, name) {
		return AuthHeaders{}, errors.New("gateway OAuth conflicts with primary auth headers")
	}
	headers := http.Header{}
	for key, values := range primary.Headers {
		headers[key] = append([]string(nil), values...)
	}
	headers.Set(name, value)
	primary.Headers = headers
	return primary, nil
}

// gatewayAuthHeader mirrors Rust `gateway_header`: the delivery selects the
// header (or the Cookie header) and the token must be a valid value for it.
func gatewayAuthHeader(config *GatewayOAuthConfig, token string) (string, string, error) {
	if token == "" {
		return "", "", errors.New("gateway OAuth returned an empty token")
	}
	switch config.Delivery.Kind {
	case "header":
		name := strings.TrimSpace(config.Delivery.Name)
		if !httpguts.ValidHeaderFieldName(name) {
			return "", "", errors.New("invalid gateway header")
		}
		value := config.Delivery.HeaderScheme() + " " + token
		if !httpguts.ValidHeaderFieldValue(value) {
			return "", "", errors.New("invalid gateway OAuth token header")
		}
		return http.CanonicalHeaderKey(name), value, nil
	case "cookie":
		// RFC 6265 cookie-octet excludes separators, quotes, whitespace and non-ASCII.
		if !validCookieOAuthToken(token) {
			return "", "", errors.New("gateway OAuth token is not a valid cookie value")
		}
		value := strings.TrimSpace(config.Delivery.Name) + "=" + token
		if !httpguts.ValidHeaderFieldValue(value) {
			return "", "", errors.New("invalid gateway OAuth token header")
		}
		return "Cookie", value, nil
	default:
		return "", "", errors.New("invalid gateway header")
	}
}

func validCookieOAuthToken(token string) bool {
	for index := 0; index < len(token); index++ {
		value := token[index]
		switch {
		case value == 0x21, value >= 0x23 && value <= 0x2b, value >= 0x2d && value <= 0x3a,
			value >= 0x3c && value <= 0x5b, value >= 0x5d && value <= 0x7e:
			continue
		default:
			return false
		}
	}
	return true
}

