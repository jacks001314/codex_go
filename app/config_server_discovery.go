package app

// OAuth discovery for a config server that omits config_server.oauth
// (Rust #62824, cloud-config/src/discovery.rs). Explicit settings always win,
// discovered settings stay in memory, and a server without published metadata
// keeps the anonymous managed-configuration fetch.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	codexnetwork "codex_go/network"
)

const configServerOAuthDiscoveryTimeout = 5 * time.Second

// resolveConfigServerOAuth returns the config server with OAuth settings when
// its published authorization metadata offers them. policy, when set, must
// allow the discovery request, so `codex logout` still honors the local
// network policy.
func resolveConfigServerOAuth(ctx context.Context, server *config.ConfigServer, client *http.Client, policy *codexnetwork.NetworkPolicy) (*config.ConfigServer, error) {
	if server == nil || server.OAuth != nil {
		return server, nil
	}
	base, err := server.ValidatedBaseURL()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if policy != nil {
		target, parseErr := url.Parse(base)
		if parseErr != nil {
			return nil, errors.New("OAuth discovery failed")
		}
		permit, acquireErr := policy.Acquire(target)
		if acquireErr != nil {
			return nil, acquireErr
		}
		permit.Release()
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, configServerOAuthDiscoveryTimeout)
	defer cancel()
	metadata, err := mcp.DiscoverAuthorizationMetadata(discoveryCtx, base, client)
	if err != nil {
		if errors.Is(discoveryCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("OAuth discovery timed out")
		}
		return nil, errors.New("OAuth discovery failed")
	}
	if metadata == nil || strings.TrimSpace(metadata.AuthorizationEndpoint) == "" || strings.TrimSpace(metadata.TokenEndpoint) == "" {
		return server, nil
	}
	resolved := *server
	oauth := &config.ConfigServerOAuth{
		AuthorizationURL: strings.TrimSpace(metadata.AuthorizationEndpoint),
		TokenURL:         strings.TrimSpace(metadata.TokenEndpoint),
		ClientID:         "codex",
	}
	if raw, ok := metadata.AdditionalFields["device_authorization_endpoint"]; ok {
		var endpoint string
		if json.Unmarshal(raw, &endpoint) == nil && strings.TrimSpace(endpoint) != "" {
			device := strings.TrimSpace(endpoint)
			oauth.DeviceAuthorizationURL = &device
		}
	}
	resolved.OAuth = oauth
	return &resolved, nil
}
