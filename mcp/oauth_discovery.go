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
	mcpOAuthLoginDiscoveryMaxTimeout         = 5 * time.Second
)

type StreamableHTTPOAuthDiscovery struct {
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []string
	Resource              string
	AuthorizationServer   string
	// Issuer is the authorization server issuer discovered for the MCP
	// server (Rust #39615): refresh tokens are bound to this issuer.
	Issuer                            string
	ClientIDMetadataDocumentSupported bool
	PublicClientTokenAuthSupported    bool
	// CallbackMode is the OAuth mix-up defense advertised by the authorization
	// server (Rust #40691).
	CallbackMode MCPOAuthCallbackMode
}

type oauthAuthorizationServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint"`
	ScopesSupported                            []string `json:"scopes_supported"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
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
	client = mcpOAuthDiscoveryHTTPClient(mcpHTTPClientWithDefaultHeaders(client, nil))
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

func mcpOAuthDiscoveryHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	originalCheckRedirect := client.CheckRedirect
	cloned.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameHTTPOrigin(via[len(via)-1].URL, request.URL) {
			return fmt.Errorf("OAuth discovery redirect to non-same-origin URL rejected: %s", request.URL)
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cloned
}

func sameHTTPOrigin(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, right.Scheme) || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	port := func(value *url.URL) string {
		if explicit := value.Port(); explicit != "" {
			return explicit
		}
		switch strings.ToLower(value.Scheme) {
		case "http":
			return "80"
		case "https":
			return "443"
		default:
			return ""
		}
	}
	return port(left) == port(right)
}

func SupportsStreamableHTTPOAuthLogin(ctx context.Context, serverURL string, client *http.Client) (bool, error) {
	discovery, err := DiscoverStreamableHTTPOAuth(ctx, serverURL, client)
	if err != nil {
		return false, err
	}
	return discovery != nil, nil
}

func buildMCPOAuthURLForLogin(config *ServerConfig, scopes []string, timeoutSecs *uint64, client *http.Client) string {
	if timeoutSecs == nil || *timeoutSecs == 0 {
		return buildMCPOAuthURL(config, scopes)
	}
	timeout := time.Duration(*timeoutSecs) * time.Second
	if timeout > mcpOAuthLoginDiscoveryMaxTimeout {
		timeout = mcpOAuthLoginDiscoveryMaxTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if client == nil {
		client = &http.Client{}
	} else {
		cloned := *client
		client = &cloned
	}
	client.Timeout = timeout
	return buildMCPOAuthURLWithDiscovery(ctx, config, scopes, client)
}

func mcpOAuthLoginDiscoveryTimeout(timeoutSecs *uint64) time.Duration {
	if timeoutSecs == nil || *timeoutSecs == 0 {
		return 0
	}
	timeout := time.Duration(*timeoutSecs) * time.Second
	if timeout > mcpOAuthLoginDiscoveryMaxTimeout {
		return mcpOAuthLoginDiscoveryMaxTimeout
	}
	return timeout
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
			// Rust #44636: a candidate-local 503 may still be recoverable
			// through the same issuer's OIDC discovery endpoints.
			fallback, fallbackOK, fallbackErr := fetchMCPOAuthOIDCDiscoveryFallback(ctx, client, candidate, err)
			if fallbackErr != nil {
				return nil, false, fallbackErr
			}
			if fallbackOK {
				return discoveryFromMCPOAuthAuthorizationMetadata(serverURL, fallback), true, nil
			}
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

// mcpOAuthMetadataHTTPStatusError carries the HTTP status of a failed metadata
// request so callers can distinguish a recoverable 503 (Rust #44636).
type mcpOAuthMetadataHTTPStatusError struct {
	URL        string
	StatusCode int
	Status     string
	Detail     string
}

func (e *mcpOAuthMetadataHTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return fmt.Sprintf("MCP OAuth metadata %s failed: %s: %s", e.URL, e.Status, e.Detail)
	}
	return fmt.Sprintf("MCP OAuth metadata %s failed: %s", e.URL, e.Status)
}

// mcpOAuthOIDCFallbackURLs returns the same issuer's OIDC discovery endpoints in
// RMCP's order (Rust #44636): the well-known-prefixed candidate first, then the
// path-suffixed one when the issuer has a path component.
func mcpOAuthOIDCFallbackURLs(candidateURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(candidateURL))
	if err != nil {
		return nil
	}
	issuerPath, ok := strings.CutPrefix(parsed.Path, mcpOAuthAuthorizationServerWellKnownPath)
	if !ok || (issuerPath != "" && !strings.HasPrefix(issuerPath, "/")) {
		return nil
	}
	base := *parsed
	base.RawQuery = ""
	base.Fragment = ""
	candidates := make([]string, 0, 2)
	prefixed := base
	prefixed.Path = "/.well-known/openid-configuration" + issuerPath
	prefixed.RawPath = ""
	candidates = append(candidates, prefixed.String())
	if issuerPath != "" {
		suffixed := base
		suffixed.Path = issuerPath + "/.well-known/openid-configuration"
		suffixed.RawPath = ""
		candidates = append(candidates, suffixed.String())
	}
	return uniqueNonEmptyStrings(candidates)
}

// fetchMCPOAuthOIDCDiscoveryFallback tries the issuer's OIDC endpoints after a
// 503 on its OAuth authorization-server metadata, keeping the original error as
// the discovery failure when no candidate is usable (Rust #44636).
func fetchMCPOAuthOIDCDiscoveryFallback(ctx context.Context, client *http.Client, candidateURL string, originalErr error) (*oauthAuthorizationServerMetadata, bool, error) {
	var statusErr *mcpOAuthMetadataHTTPStatusError
	if !errors.As(originalErr, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		return nil, false, nil
	}
	fallbackURLs := mcpOAuthOIDCFallbackURLs(candidateURL)
	if len(fallbackURLs) == 0 {
		return nil, false, nil
	}
	// Do not follow fallback redirects: RMCP owns discovery redirect policy.
	fallbackClient := mcpOAuthNoRedirectClient(client)
	for _, fallbackURL := range fallbackURLs {
		metadata, ok, err := fetchMCPOAuthAuthorizationServerMetadata(ctx, fallbackClient, fallbackURL)
		if err == nil {
			if ok {
				return metadata, true, nil
			}
			continue
		}
		var fallbackStatus *mcpOAuthMetadataHTTPStatusError
		if !errors.As(err, &fallbackStatus) {
			return nil, false, err
		}
		switch fallbackStatus.StatusCode {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusServiceUnavailable:
			continue
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return nil, false, err
		default:
			if fallbackStatus.StatusCode >= 500 {
				return nil, false, err
			}
			// Any other response keeps the original discovery error instead of
			// enabling a lower-priority endpoint fallback.
			return nil, false, nil
		}
	}
	return nil, false, nil
}

func mcpOAuthNoRedirectClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
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
		return false, &mcpOAuthMetadataHTTPStatusError{
			URL:        metadataURL,
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Detail:     detail,
		}
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
	callbackMode, err := MCPOAuthCallbackModeForDiscovery(metadata.AuthorizationResponseIssParameterSupported, metadata.Issuer)
	if err != nil {
		// Rust maps a missing metadata issuer to the callback-specific fallback
		// (`callback_mode(..).unwrap_or(CallbackSpecific)`).
		callbackMode = MCPOAuthCallbackSpecific
	}
	return &StreamableHTTPOAuthDiscovery{
		AuthorizationEndpoint:             strings.TrimSpace(metadata.AuthorizationEndpoint),
		TokenEndpoint:                     strings.TrimSpace(metadata.TokenEndpoint),
		RegistrationEndpoint:              strings.TrimSpace(metadata.RegistrationEndpoint),
		ScopesSupported:                   normalizeMCPOAuthScopes(metadata.ScopesSupported),
		AuthorizationServer:               authorizationServer,
		Issuer:                            authorizationServer,
		ClientIDMetadataDocumentSupported: metadata.ClientIDMetadataDocumentSupported,
		PublicClientTokenAuthSupported:    metadataHasPublicClientTokenAuth(metadata.TokenEndpointAuthMethodsSupported),
		CallbackMode:                      callbackMode,
	}
}

func metadataHasPublicClientTokenAuth(methods []string) bool {
	for _, method := range methods {
		if method == "none" {
			return true
		}
	}
	return false
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
