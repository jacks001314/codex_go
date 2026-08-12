package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

type OAuthLoginSessionOptions struct {
	ServerURL             string
	ClientID              string
	ClientSecret          string
	RegistrationEndpoint  string
	ClientName            string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RedirectURL           string
	Resource              string
	Scopes                []string
	State                 string
	ClientRegistration    MCPServerOauthClientRegistration
	CallbackID            string
	CIMDAdvertised        *bool
	PublicClientAuth      *bool
}

type OAuthLoginSession struct {
	ServerURL        string
	ClientID         string
	ClientSecret     string
	TokenEndpoint    string
	RedirectURL      string
	CallbackPath     string
	CodeVerifier     string
	State            string
	AuthorizationURL string
	Scopes           []string
}

type OAuthCallbackResult struct {
	Code  string
	State string
}

type OAuthProviderError struct {
	Code        string
	Description string
}

func (e *OAuthProviderError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Description) == "" {
		return strings.TrimSpace(e.Code)
	}
	if strings.TrimSpace(e.Code) == "" {
		return strings.TrimSpace(e.Description)
	}
	return fmt.Sprintf("%s: %s", strings.TrimSpace(e.Code), strings.TrimSpace(e.Description))
}

func NewOAuthLoginSession(options *OAuthLoginSessionOptions) (*OAuthLoginSession, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth login session options are required")
	}
	if strings.TrimSpace(options.ServerURL) == "" {
		return nil, errors.New("MCP OAuth server URL is required")
	}
	if strings.TrimSpace(options.ClientID) == "" {
		return nil, errors.New("MCP OAuth client ID is required")
	}
	if strings.TrimSpace(options.AuthorizationEndpoint) == "" || strings.TrimSpace(options.TokenEndpoint) == "" {
		return nil, errors.New("MCP OAuth authorization and token endpoints are required")
	}
	if strings.TrimSpace(options.RedirectURL) == "" {
		return nil, errors.New("MCP OAuth redirect URL is required")
	}
	callbackPath, err := MCPOAuthCallbackPath(options.RedirectURL)
	if err != nil {
		return nil, err
	}
	state := strings.TrimSpace(options.State)
	if state == "" {
		state, err = randomURLSafeString(24)
		if err != nil {
			return nil, err
		}
	}
	verifier := oauth2.GenerateVerifier()
	scopes := normalizeMCPOAuthScopes(options.Scopes)
	config := &oauth2.Config{
		ClientID:     strings.TrimSpace(options.ClientID),
		ClientSecret: strings.TrimSpace(options.ClientSecret),
		RedirectURL:  strings.TrimSpace(options.RedirectURL),
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   strings.TrimSpace(options.AuthorizationEndpoint),
			TokenURL:  strings.TrimSpace(options.TokenEndpoint),
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	authOptions := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if resource := strings.TrimSpace(options.Resource); resource != "" {
		authOptions = append(authOptions, oauth2.SetAuthURLParam("resource", resource))
	}
	return &OAuthLoginSession{
		ServerURL:        strings.TrimSpace(options.ServerURL),
		ClientID:         strings.TrimSpace(options.ClientID),
		ClientSecret:     strings.TrimSpace(options.ClientSecret),
		TokenEndpoint:    strings.TrimSpace(options.TokenEndpoint),
		RedirectURL:      strings.TrimSpace(options.RedirectURL),
		CallbackPath:     callbackPath,
		CodeVerifier:     verifier,
		State:            state,
		AuthorizationURL: config.AuthCodeURL(state, authOptions...),
		Scopes:           scopes,
	}, nil
}

func NewOAuthLoginSessionWithClientRegistration(ctx context.Context, options *OAuthLoginSessionOptions, client *http.Client) (*OAuthLoginSession, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth login session options are required")
	}
	if strings.TrimSpace(options.ClientID) != "" {
		return NewOAuthLoginSession(options)
	}
	registration := options.ClientRegistration
	if registration == "" {
		registration = MCPServerOauthClientRegistrationAuto
	}
	callbackID := strings.TrimSpace(options.CallbackID)
	if registration == MCPServerOauthClientRegistrationAuto || registration == MCPServerOauthClientRegistrationCimd {
		cimdAdvertised := false
		publicClientAuthSupported := false
		if options.CIMDAdvertised != nil && options.PublicClientAuth != nil {
			cimdAdvertised = *options.CIMDAdvertised
			publicClientAuthSupported = *options.PublicClientAuth
		} else {
			var err error
			cimdAdvertised, publicClientAuthSupported, err = mcpOAuthMetadataCIMDFlags(ctx, client, options.ServerURL)
			if err != nil && registration == MCPServerOauthClientRegistrationCimd {
				return nil, err
			}
		}
		nativeRedirect := mcpOAuthNativeRedirectSupported(options.RedirectURL, callbackID)
		offerCIMD := cimdAdvertised && publicClientAuthSupported && nativeRedirect
		if registration == MCPServerOauthClientRegistrationCimd {
			if !cimdAdvertised || !publicClientAuthSupported {
				return nil, errors.New("MCP authorization server does not advertise CIMD with token endpoint auth method `none`")
			}
			if !nativeRedirect {
				return nil, fmt.Errorf("MCP OAuth CIMD requires an ephemeral loopback callback at `/callback/%s`", callbackID)
			}
		}
		if offerCIMD {
			return newOAuthLoginSessionCIMD(options)
		}
	}
	if registration == MCPServerOauthClientRegistrationDcr && strings.TrimSpace(options.RegistrationEndpoint) == "" {
		return nil, errors.New("MCP OAuth login requires dynamic client registration (clientRegistration=dcr), but the server does not advertise a registration endpoint")
	}
	if strings.TrimSpace(options.RegistrationEndpoint) == "" {
		return NewOAuthLoginSession(options)
	}
	registered, err := NewOAuthClientRegistrar(client).Register(ctx, &OAuthClientRegistrationOptions{
		RegistrationEndpoint: options.RegistrationEndpoint,
		ClientName:           options.ClientName,
		RedirectURIs:         []string{options.RedirectURL},
		Scopes:               options.Scopes,
	})
	if err != nil {
		return nil, err
	}
	cloned := *options
	cloned.ClientID = registered.ClientID
	cloned.ClientSecret = registered.ClientSecret
	return NewOAuthLoginSession(&cloned)
}

// newOAuthLoginSessionCIMD builds a login session for the native Client ID
// Metadata Document flow: the client identifier is the ChatGPT-hosted Codex
// metadata document URL and the authorization request carries it explicitly
// (Rust #38089, draft-ietf-oauth-client-id-metadata-document).
func newOAuthLoginSessionCIMD(options *OAuthLoginSessionOptions) (*OAuthLoginSession, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth login session options are required")
	}
	callbackID := strings.TrimSpace(options.CallbackID)
	if callbackID == "" {
		return nil, errors.New("MCP OAuth CIMD requires a callback id")
	}
	clientMetadataURL := fmt.Sprintf("https://chatgpt.com/oauth/codex/%s/client.json", callbackID)
	cloned := *options
	cloned.ClientID = clientMetadataURL
	session, err := NewOAuthLoginSession(&cloned)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(session.AuthorizationURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("client_metadata_url", clientMetadataURL)
	parsed.RawQuery = query.Encode()
	session.AuthorizationURL = parsed.String()
	return session, nil
}

// mcpOAuthMetadataCIMDFlags reports whether the authorization server
// advertises Client ID Metadata Documents with public-client token auth.
func mcpOAuthMetadataCIMDFlags(ctx context.Context, client *http.Client, serverURL string) (bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}
	candidates, err := mcpOAuthAuthorizationServerMetadataURLs(serverURL)
	if err != nil {
		return false, false, err
	}
	var lastErr error
	for _, candidate := range candidates {
		var metadata oauthAuthorizationServerMetadata
		ok, err := fetchMCPOAuthMetadata(ctx, client, candidate, &metadata)
		if err != nil {
			lastErr = err
			continue
		}
		if !ok {
			continue
		}
		publicClient := false
		for _, method := range metadata.TokenEndpointAuthMethodsSupported {
			if method == "none" {
				publicClient = true
				break
			}
		}
		return metadata.ClientIDMetadataDocumentSupported, publicClient, nil
	}
	if lastErr != nil {
		return false, false, lastErr
	}
	return false, false, errors.New("MCP OAuth authorization server metadata not found")
}

// mcpOAuthNativeRedirectSupported validates the ephemeral loopback callback
// shape required by the CIMD flow (Rust #38089).
func mcpOAuthNativeRedirectSupported(redirectURL string, callbackID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(redirectURL))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return false
	}
	if portValue, err := strconv.Atoi(port); err != nil || portValue <= 0 {
		return false
	}
	callbackID = strings.TrimSpace(callbackID)
	if callbackID == "" || parsed.Path != "/callback/"+callbackID {
		return false
	}
	return parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}

func (s *OAuthLoginSession) CompleteCallback(ctx context.Context, rawPath string, client *OAuthTokenClient, serverName string) (*OAuthTokenSet, error) {
	if s == nil {
		return nil, errors.New("MCP OAuth login session is nil")
	}
	callback, err := ParseMCPOAuthCallback(rawPath, s.CallbackPath)
	if err != nil {
		return nil, err
	}
	if callback.State != s.State {
		return nil, errors.New("MCP OAuth callback state mismatch")
	}
	if client == nil {
		client = NewOAuthTokenClient(nil)
	}
	return client.ExchangeAuthorizationCode(ctx, &OAuthCodeExchangeOptions{
		ServerName:            serverName,
		ServerURL:             s.ServerURL,
		ClientID:              s.ClientID,
		ClientSecret:          s.ClientSecret,
		AuthorizationEndpoint: "",
		TokenEndpoint:         s.TokenEndpoint,
		RedirectURL:           s.RedirectURL,
		Code:                  callback.Code,
		CodeVerifier:          s.CodeVerifier,
		Scopes:                s.Scopes,
	})
}

func ParseMCPOAuthCallback(rawPath string, expectedCallbackPath string) (*OAuthCallbackResult, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawPath))
	if err != nil {
		return nil, err
	}
	if parsed.Path != expectedCallbackPath {
		return nil, errors.New("invalid MCP OAuth callback path")
	}
	query := parsed.Query()
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	if code != "" && state != "" {
		return &OAuthCallbackResult{Code: code, State: state}, nil
	}
	errorCode := strings.TrimSpace(query.Get("error"))
	errorDescription := strings.TrimSpace(query.Get("error_description"))
	if errorCode != "" || errorDescription != "" {
		return nil, &OAuthProviderError{Code: errorCode, Description: errorDescription}
	}
	return nil, errors.New("invalid MCP OAuth callback")
}

func MCPOAuthCallbackID(serverURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("MCP server URL %q must include a host", serverURL)
	}
	parsed.Fragment = ""
	digest := sha256.Sum256([]byte(parsed.String()))
	return base64.RawURLEncoding.EncodeToString(digest[:9]), nil
}

func AppendMCPOAuthCallbackID(redirectURI string, callbackID string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(redirectURI))
	if err != nil {
		return "", err
	}
	path := parsed.Path
	if strings.HasSuffix(path, "/") {
		parsed.Path = path + strings.TrimSpace(callbackID)
	} else {
		parsed.Path = path + "/" + strings.TrimSpace(callbackID)
	}
	return parsed.String(), nil
}

func MCPOAuthCallbackPath(redirectURI string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(redirectURI))
	if err != nil {
		return "", err
	}
	return parsed.Path, nil
}

func randomURLSafeString(bytesLen int) (string, error) {
	if bytesLen <= 0 {
		bytesLen = 24
	}
	data := make([]byte, bytesLen)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
