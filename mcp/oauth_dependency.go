package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const mcpOAuthDependencyLoginTimeout = 5 * time.Minute

type OAuthScopesSource string

const (
	OAuthScopesExplicit   OAuthScopesSource = "explicit"
	OAuthScopesConfigured OAuthScopesSource = "configured"
	OAuthScopesDiscovered OAuthScopesSource = "discovered"
	OAuthScopesEmpty      OAuthScopesSource = "empty"
)

type ResolvedOAuthScopes struct {
	Scopes []string
	Source OAuthScopesSource
}

type OAuthDependencyLoginOptions struct {
	Name        string
	OpenBrowser func(string) error
	HTTPClient  *http.Client
	Timeout     time.Duration
}

func ResolveOAuthScopes(explicitScopes []string, explicit bool, configuredScopes []string, configured bool, discoveredScopes []string) ResolvedOAuthScopes {
	if explicit {
		return ResolvedOAuthScopes{Scopes: append([]string{}, explicitScopes...), Source: OAuthScopesExplicit}
	}
	if configured {
		return ResolvedOAuthScopes{Scopes: append([]string{}, configuredScopes...), Source: OAuthScopesConfigured}
	}
	if scopes := normalizeMCPOAuthScopes(discoveredScopes); len(scopes) > 0 {
		return ResolvedOAuthScopes{Scopes: scopes, Source: OAuthScopesDiscovered}
	}
	return ResolvedOAuthScopes{Scopes: []string{}, Source: OAuthScopesEmpty}
}

func ShouldRetryOAuthWithoutScopes(scopes ResolvedOAuthScopes, err error) bool {
	if scopes.Source != OAuthScopesDiscovered || err == nil {
		return false
	}
	var providerError *OAuthProviderError
	return errors.As(err, &providerError)
}

// LoginOAuthDependency discovers OAuth support, opens the authorization URL,
// waits for callback completion, and retries once without discovered scopes if
// the provider rejects them. The bool reports whether OAuth was supported.
func (s *MCPService) LoginOAuthDependency(ctx context.Context, options *OAuthDependencyLoginOptions) (bool, error) {
	if s == nil || options == nil {
		return false, nil
	}
	name := strings.TrimSpace(options.Name)
	if name == "" {
		return false, invalidMCPRequest("name is required")
	}
	config, ok := s.serverConfig(name)
	if !ok || strings.TrimSpace(config.URL) == "" || strings.TrimSpace(config.BearerTokenEnvVar) != "" {
		return false, nil
	}
	client := options.HTTPClient
	if client == nil {
		client = s.httpClientForServer(name, &config).oauthHTTPClient(mcpOAuthLoginDiscoveryMaxTimeout)
	}
	discoveryContext, cancelDiscovery := context.WithTimeout(contextOrBackground(ctx), mcpOAuthLoginDiscoveryMaxTimeout)
	discovery, err := DiscoverStreamableHTTPOAuth(discoveryContext, config.URL, client)
	cancelDiscovery()
	if err != nil {
		return false, err
	}
	if discovery == nil {
		return false, nil
	}
	resolved := ResolveOAuthScopes(nil, false, config.Scopes, config.ScopesConfigured, discovery.ScopesSupported)
	err = s.performOAuthDependencyLogin(ctx, name, &config, discovery, resolved.Scopes, options)
	if err != nil && ShouldRetryOAuthWithoutScopes(resolved, err) {
		err = s.performOAuthDependencyLogin(ctx, name, &config, discovery, nil, options)
	}
	return true, err
}

func (s *MCPService) performOAuthDependencyLogin(ctx context.Context, name string, config *ServerConfig, discovery *StreamableHTTPOAuthDiscovery, scopes []string, options *OAuthDependencyLoginOptions) error {
	store := s.oauthStoreForConfig(config)
	if store == nil {
		return errors.New("MCP OAuth credential store is unavailable")
	}
	client := options.HTTPClient
	if client == nil {
		client = s.httpClientForServer(name, config).oauthHTTPClient(0)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = mcpOAuthDependencyLoginTimeout
	}
	login, err := StartOAuthLoginServer(contextOrBackground(ctx), &OAuthLoginServerOptions{
		ServerName:            name,
		ServerURL:             config.URL,
		ClientID:              config.OAuthClientID,
		RegistrationEndpoint:  discovery.RegistrationEndpoint,
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
		Resource:              firstNonEmptyMCP(config.OAuthResource, discovery.Resource),
		Scopes:                scopes,
		Store:                 store,
		HTTPClient:            client,
		Timeout:               timeout,
	})
	if err != nil {
		return err
	}
	if options.OpenBrowser != nil {
		// Browser launch failure does not cancel the callback server, matching Rust.
		_ = options.OpenBrowser(login.AuthorizationURL)
	}
	waitContext, cancel := context.WithTimeout(contextOrBackground(ctx), timeout)
	defer cancel()
	var result *OAuthLoginServerResult
	select {
	case result = <-login.Done():
	case <-waitContext.Done():
		_ = login.Cancel(context.Background())
		if errors.Is(waitContext.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for OAuth callback: %w", waitContext.Err())
		}
		return waitContext.Err()
	}
	s.clearHTTPClients()
	if result != nil && result.Error != nil {
		return result.Error
	}
	return nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
