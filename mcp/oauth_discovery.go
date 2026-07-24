package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	mcpOAuthAuthorizationServerWellKnownPath = "/.well-known/oauth-authorization-server"
	mcpOAuthProtectedResourceWellKnownPath   = "/.well-known/oauth-protected-resource"
	mcpOAuthMetadataMaxBytes                 = 1 << 20
	mcpOAuthLoginDiscoveryMaxTimeout         = 2 * time.Second
)

type StreamableHTTPOAuthDiscovery struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []string
	Resource              string
	AuthorizationServer   string
}

type oauthAuthorizationServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

type oauthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

func DiscoverStreamableHTTPOAuth(ctx context.Context, serverURL string, client *http.Client) (*StreamableHTTPOAuthDiscovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}
	client = mcpHTTPClientWithDefaultHeaders(client, nil)
	raw := strings.TrimSpace(serverURL)
	if raw == "" {
		return nil, errors.New("MCP OAuth discovery URL is required")
	}
	if _, err := parseMCPHTTPURL(raw); err != nil {
		return nil, err
	}
	if discovery, found, err := discoverMCPOAuthAuthorizationServer(ctx, client, raw); found || err != nil {
		return discovery, err
	}
	resourceMetadata, err := discoverMCPOAuthProtectedResource(ctx, client, raw)
	if err != nil {
		return nil, err
	}
	if resourceMetadata == nil {
		return nil, nil
	}
	for _, authorizationServer := range resourceMetadata.AuthorizationServers {
		discovery, found, err := discoverMCPOAuthAuthorizationServer(ctx, client, authorizationServer)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if discovery.Resource == "" {
			discovery.Resource = strings.TrimSpace(resourceMetadata.Resource)
		}
		if discovery.Resource == "" {
			discovery.Resource = raw
		}
		return discovery, nil
	}
	return nil, nil
}

func SupportsStreamableHTTPOAuthLogin(ctx context.Context, serverURL string, client *http.Client) (bool, error) {
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, serverURL, client)
	if err != nil {
		return false, err
	}
	return discovery != nil, nil
}

func buildMCPOAuthURLForLogin(config *ServerConfig, scopes []string, timeoutSecs *uint64) string {
	if timeoutSecs == nil || *timeoutSecs == 0 {
		return buildMCPOAuthURL(config, scopes)
	}
	timeout := time.Duration(*timeoutSecs) * time.Second
	if timeout > mcpOAuthLoginDiscoveryMaxTimeout {
		timeout = mcpOAuthLoginDiscoveryMaxTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return buildMCPOAuthURLWithDiscovery(ctx, config, scopes, &http.Client{Timeout: timeout})
}

func buildMCPOAuthURLWithDiscovery(ctx context.Context, config *ServerConfig, scopes []string, client *http.Client) string {
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, config.URL, client)
	if err != nil || discovery == nil || strings.TrimSpace(discovery.AuthorizationEndpoint) == "" {
		return buildMCPOAuthURL(config, scopes)
	}
	return buildMCPOAuthAuthorizeURL(discovery.AuthorizationEndpoint, config, scopes, discovery.Resource)
}

func buildMCPOAuthURL(config *ServerConfig, scopes []string) string {
	if config == nil {
		return "http://localhost/oauth"
	}
	raw := strings.TrimSpace(config.URL)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "http://localhost/oauth"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/oauth/authorize"
	parsed.RawPath = ""
	return buildMCPOAuthAuthorizeURL(parsed.String(), config, scopes, config.OAuthResource)
}

func buildMCPOAuthAuthorizeURL(endpoint string, config *ServerConfig, scopes []string, resource string) string {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "http://localhost/oauth"
	}
	clientID := ""
	if config != nil {
		clientID = strings.TrimSpace(config.OAuthClientID)
	}
	oauthConfig := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL: parsed.String(),
		},
		Scopes: normalizeMCPOAuthScopes(scopes),
	}
	options := []oauth2.AuthCodeOption{}
	resource = strings.TrimSpace(resource)
	if config != nil && strings.TrimSpace(config.OAuthResource) != "" {
		resource = strings.TrimSpace(config.OAuthResource)
	}
	if resource != "" {
		options = append(options, oauth2.SetAuthURLParam("resource", resource))
	}
	return oauthConfig.AuthCodeURL("", options...)
}

func discoverMCPOAuthAuthorizationServer(ctx context.Context, client *http.Client, serverURL string) (*StreamableHTTPOAuthDiscovery, bool, error) {
	candidates, err := mcpOAuthAuthorizationServerMetadataURLs(serverURL)
	if err != nil {
		return nil, false, err
	}
	var lastErr error
	for _, candidate := range candidates {
		metadata, ok, err := fetchMCPOAuthAuthorizationServerMetadata(ctx, client, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			return discoveryFromMCPOAuthAuthorizationMetadata(serverURL, metadata), true, nil
		}
	}
	if lastErr != nil {
		return nil, false, lastErr
	}
	return nil, false, nil
}

func discoverMCPOAuthProtectedResource(ctx context.Context, client *http.Client, serverURL string) (*oauthProtectedResourceMetadata, error) {
	if metadataURL, err := mcpOAuthProtectedResourceMetadataURLFromChallenge(ctx, client, serverURL); metadataURL != "" || err != nil {
		if err != nil {
			return nil, err
		}
		metadata, ok, err := fetchMCPOAuthProtectedResourceMetadata(ctx, client, metadataURL)
		if err != nil || !ok {
			return nil, err
		}
		return metadata, nil
	}
	candidates, err := mcpOAuthProtectedResourceMetadataURLs(serverURL)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, candidate := range candidates {
		metadata, ok, err := fetchMCPOAuthProtectedResourceMetadata(ctx, client, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			return metadata, nil
		}
	}
	return nil, lastErr
}

func mcpOAuthAuthorizationServerMetadataURLs(raw string) ([]string, error) {
	parsed, err := parseMCPHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(parsed.Path, "/")
	candidates := []string{}
	if path != "" {
		withPath := *parsed
		withPath.Path = mcpOAuthAuthorizationServerWellKnownPath + "/" + path
		withPath.RawPath = ""
		withPath.RawQuery = ""
		withPath.Fragment = ""
		candidates = append(candidates, withPath.String())
	}
	root := *parsed
	root.Path = mcpOAuthAuthorizationServerWellKnownPath
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""
	candidates = append(candidates, root.String())
	return uniqueNonEmptyStrings(candidates), nil
}

func mcpOAuthProtectedResourceMetadataURLs(raw string) ([]string, error) {
	parsed, err := parseMCPHTTPURL(raw)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(parsed.Path, "/")
	candidates := []string{}
	if path != "" {
		withPath := *parsed
		withPath.Path = mcpOAuthProtectedResourceWellKnownPath + "/" + path
		withPath.RawPath = ""
		withPath.RawQuery = ""
		withPath.Fragment = ""
		candidates = append(candidates, withPath.String())
	}
	root := *parsed
	root.Path = mcpOAuthProtectedResourceWellKnownPath
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""
	candidates = append(candidates, root.String())
	return uniqueNonEmptyStrings(candidates), nil
}

func mcpOAuthProtectedResourceMetadataURLFromChallenge(ctx context.Context, client *http.Client, serverURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(serverURL), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	}
	for _, challenge := range response.Header.Values("WWW-Authenticate") {
		if metadataURL := authChallengeParam(challenge, "resource_metadata"); metadataURL != "" {
			return metadataURL, nil
		}
	}
	return "", nil
}

func fetchMCPOAuthAuthorizationServerMetadata(ctx context.Context, client *http.Client, metadataURL string) (*oauthAuthorizationServerMetadata, bool, error) {
	var metadata oauthAuthorizationServerMetadata
	ok, err := fetchMCPOAuthMetadata(ctx, client, metadataURL, &metadata)
	if err != nil || !ok {
		return nil, ok, err
	}
	if strings.TrimSpace(metadata.AuthorizationEndpoint) == "" || strings.TrimSpace(metadata.TokenEndpoint) == "" {
		return nil, false, nil
	}
	return &metadata, true, nil
}

func fetchMCPOAuthProtectedResourceMetadata(ctx context.Context, client *http.Client, metadataURL string) (*oauthProtectedResourceMetadata, bool, error) {
	var metadata oauthProtectedResourceMetadata
	ok, err := fetchMCPOAuthMetadata(ctx, client, metadataURL, &metadata)
	if err != nil || !ok {
		return nil, ok, err
	}
	metadata.AuthorizationServers = uniqueNonEmptyStrings(metadata.AuthorizationServers)
	if len(metadata.AuthorizationServers) == 0 {
		return nil, false, nil
	}
	return &metadata, true, nil
}

func fetchMCPOAuthMetadata(ctx context.Context, client *http.Client, metadataURL string, out any) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(metadataURL), nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return false, fmt.Errorf("MCP OAuth metadata %s failed: %s: %s", metadataURL, response.Status, detail)
		}
		return false, fmt.Errorf("MCP OAuth metadata %s failed: %s", metadataURL, response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, mcpOAuthMetadataMaxBytes)).Decode(out); err != nil {
		return false, err
	}
	return true, nil
}

func discoveryFromMCPOAuthAuthorizationMetadata(serverURL string, metadata *oauthAuthorizationServerMetadata) *StreamableHTTPOAuthDiscovery {
	if metadata == nil {
		return nil
	}
	authorizationServer := strings.TrimSpace(metadata.Issuer)
	if authorizationServer == "" {
		authorizationServer = strings.TrimSpace(serverURL)
	}
	return &StreamableHTTPOAuthDiscovery{
		AuthorizationEndpoint: strings.TrimSpace(metadata.AuthorizationEndpoint),
		TokenEndpoint:         strings.TrimSpace(metadata.TokenEndpoint),
		RegistrationEndpoint:  strings.TrimSpace(metadata.RegistrationEndpoint),
		ScopesSupported:       normalizeMCPOAuthScopes(metadata.ScopesSupported),
		AuthorizationServer:   authorizationServer,
	}
}

func normalizeMCPOAuthScopes(scopes []string) []string {
	normalized := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		normalized = append(normalized, scope)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func parseMCPHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("MCP OAuth discovery requires http or https URL: %s", raw)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("MCP OAuth discovery URL is missing host: %s", raw)
	}
	return parsed, nil
}

func authChallengeParam(challenge string, name string) string {
	target := strings.ToLower(strings.TrimSpace(name))
	lower := strings.ToLower(challenge)
	offset := 0
	for {
		idx := strings.Index(lower[offset:], target)
		if idx < 0 {
			return ""
		}
		idx += offset
		if idx > 0 && !isAuthChallengeSeparator(challenge[idx-1]) {
			offset = idx + len(target)
			continue
		}
		pos := idx + len(target)
		for pos < len(challenge) && challenge[pos] == ' ' {
			pos++
		}
		if pos >= len(challenge) || challenge[pos] != '=' {
			offset = idx + len(target)
			continue
		}
		pos++
		for pos < len(challenge) && challenge[pos] == ' ' {
			pos++
		}
		if pos >= len(challenge) {
			return ""
		}
		if challenge[pos] == '"' {
			pos++
			start := pos
			for pos < len(challenge) {
				if challenge[pos] == '"' && challenge[pos-1] != '\\' {
					return strings.TrimSpace(strings.ReplaceAll(challenge[start:pos], `\"`, `"`))
				}
				pos++
			}
			return ""
		}
		start := pos
		for pos < len(challenge) && challenge[pos] != ',' && challenge[pos] != ' ' {
			pos++
		}
		return strings.TrimSpace(challenge[start:pos])
	}
}

func isAuthChallengeSeparator(value byte) bool {
	return value == ' ' || value == ',' || value == '\t'
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
