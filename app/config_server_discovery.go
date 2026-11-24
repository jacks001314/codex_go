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
	"sync"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	codexnetwork "codex_go/network"
)

const configServerOAuthDiscoveryTimeout = 5 * time.Second

// configServerOAuthResolutionKey identifies one reusable discovery result. Rust
// #63473 reuses a ConfigServerResolution only when the configured server, the
// outbound proxy policy and the system-proxy fallback setting match.
type configServerOAuthResolutionKey struct {
	baseURL             string
	policyRevision      codexnetwork.NetworkPolicyRevision
	policyManaged       bool
	respectSystemProxy  bool
	systemProxyFallback bool
}

// configServerOAuthResolutions memoizes resolved discovery results across the
// sign-in, startup and managed-policy loaders so the same server's OAuth
// metadata is not fetched again for the same proxy policy (Rust #63473).
var configServerOAuthResolutions = struct {
	mu      sync.Mutex
	entries map[configServerOAuthResolutionKey]*config.ConfigServer
	order   []configServerOAuthResolutionKey
}{entries: map[configServerOAuthResolutionKey]*config.ConfigServer{}}

const maxConfigServerOAuthResolutions = 8

// resolveConfigServerOAuthForConfig is the reuse-aware form used by the sign-in,
// startup and managed-policy paths. Explicit OAuth settings need no discovery,
// and a cached result only matches when the server, the published network policy
// and both proxy settings agree.
func resolveConfigServerOAuthForConfig(ctx context.Context, server *config.ConfigServer, client *http.Client, policy *codexnetwork.NetworkPolicy, cfg *config.Config) (*config.ConfigServer, error) {
	if server == nil || server.OAuth != nil {
		return resolveConfigServerOAuth(ctx, server, client, policy)
	}
	key := configServerOAuthResolutionKey{
		baseURL:             strings.TrimSpace(server.BaseURL),
		respectSystemProxy:  cfg.RespectSystemProxyEnabled(),
		systemProxyFallback: cfg.SystemProxyFallbackEnabled(),
	}
	if policy != nil {
		key.policyRevision = policy.Revision()
		key.policyManaged = policy.IsManaged()
	}
	configServerOAuthResolutions.mu.Lock()
	cached := configServerOAuthResolutions.entries[key]
	configServerOAuthResolutions.mu.Unlock()
	if cached != nil {
		return cloneResolvedConfigServer(cached), nil
	}
	resolved, err := resolveConfigServerOAuth(ctx, server, client, policy)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	configServerOAuthResolutions.mu.Lock()
	if _, exists := configServerOAuthResolutions.entries[key]; !exists {
		if len(configServerOAuthResolutions.order) >= maxConfigServerOAuthResolutions {
			oldest := configServerOAuthResolutions.order[0]
			configServerOAuthResolutions.order = configServerOAuthResolutions.order[1:]
			delete(configServerOAuthResolutions.entries, oldest)
		}
		configServerOAuthResolutions.entries[key] = cloneResolvedConfigServer(resolved)
		configServerOAuthResolutions.order = append(configServerOAuthResolutions.order, key)
	}
	configServerOAuthResolutions.mu.Unlock()
	return cloneResolvedConfigServer(resolved), nil
}

// cloneResolvedConfigServer copies a resolved config server (and its OAuth
// settings) so a cached entry can never be mutated by a caller.
func cloneResolvedConfigServer(server *config.ConfigServer) *config.ConfigServer {
	if server == nil {
		return nil
	}
	clone := *server
	if server.OAuth != nil {
		oauth := *server.OAuth
		if server.OAuth.DeviceAuthorizationURL != nil {
			device := *server.OAuth.DeviceAuthorizationURL
			oauth.DeviceAuthorizationURL = &device
		}
		clone.OAuth = &oauth
	}
	return &clone
}

// forgetConfigServerOAuthResolution drops the cached resolutions for one server,
// so onboarding or a server change is rediscovered (Rust #63473 rediscovers
// anonymous servers after onboarding).
func forgetConfigServerOAuthResolution(baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return
	}
	configServerOAuthResolutions.mu.Lock()
	defer configServerOAuthResolutions.mu.Unlock()
	kept := make([]configServerOAuthResolutionKey, 0, len(configServerOAuthResolutions.order))
	for _, key := range configServerOAuthResolutions.order {
		if key.baseURL == baseURL {
			delete(configServerOAuthResolutions.entries, key)
			continue
		}
		kept = append(kept, key)
	}
	configServerOAuthResolutions.order = kept
}

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
