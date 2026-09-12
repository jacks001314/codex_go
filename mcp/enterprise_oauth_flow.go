package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Rust parity: codex-rs/rmcp-client/src/ema_identity.rs +
// enterprise_oauth_login.rs (#43844). Enterprise IdP login requires published
// authorization metadata bound to the configured issuer; the browser flow
// returns an authorization URL and stages validated credentials that the
// caller commits explicitly.

const (
	mcpOAuthOIDCWellKnownPath = "/.well-known/openid-configuration"
)

// mcpEnterpriseAuthorizationMetadata is the discovered metadata needed to start
// an enterprise OIDC login.
type mcpEnterpriseAuthorizationMetadata struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	CallbackMode          MCPOAuthCallbackMode
}

// enterpriseDiscoveryURLs mirrors rmcp
// AuthorizationManager::generate_discovery_urls (spec-2025-11-25 4.3 order).
func enterpriseDiscoveryURLs(issuer string) ([]string, error) {
	parsed, err := parseMCPHTTPURL(issuer)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(parsed.Path, "/")
	build := func(discoveryPath string) string {
		candidate := *parsed
		candidate.Path = discoveryPath
		candidate.RawPath = ""
		candidate.RawQuery = ""
		candidate.Fragment = ""
		return candidate.String()
	}
	if path == "" {
		return []string{
			build(mcpOAuthAuthorizationServerWellKnownPath),
			build(mcpOAuthOIDCWellKnownPath),
		}, nil
	}
	return []string{
		build(mcpOAuthAuthorizationServerWellKnownPath + "/" + path),
		build(mcpOAuthOIDCWellKnownPath + "/" + path),
		build("/" + path + mcpOAuthOIDCWellKnownPath),
		build(mcpOAuthAuthorizationServerWellKnownPath),
	}, nil
}

// enterpriseExpectedIssuerForDiscoveryURL mirrors rmcp
// expected_issuer_for_authorization_metadata_url: the discovery URL implies the
// issuer it must be bound to, or "" when the form is unrecognized.
func enterpriseExpectedIssuerForDiscoveryURL(discoveryURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(discoveryURL))
	if err != nil {
		return ""
	}
	path := parsed.Path
	var issuerPath string
	switch {
	case path == mcpOAuthAuthorizationServerWellKnownPath || path == mcpOAuthOIDCWellKnownPath:
		issuerPath = ""
	case strings.HasPrefix(path, mcpOAuthAuthorizationServerWellKnownPath+"/"):
		issuerPath = strings.TrimPrefix(path, mcpOAuthAuthorizationServerWellKnownPath+"/")
	case strings.HasPrefix(path, mcpOAuthOIDCWellKnownPath+"/"):
		issuerPath = strings.TrimPrefix(path, mcpOAuthOIDCWellKnownPath+"/")
	case strings.HasSuffix(path, mcpOAuthOIDCWellKnownPath):
		issuerPath = strings.Trim(strings.TrimSuffix(path, mcpOAuthOIDCWellKnownPath), "/")
	default:
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if issuerPath == "" {
		parsed.Path = ""
	} else {
		parsed.Path = "/" + issuerPath
	}
	return parsed.String()
}

// enterpriseIssuerIdentifiersMatch mirrors rmcp issuer_identifiers_match: exact
// equality, or equality after dropping a root trailing slash from both sides.
func enterpriseIssuerIdentifiersMatch(received string, expected string) bool {
	if received == expected {
		return true
	}
	return trimMCPOAuthRootSlash(received) == trimMCPOAuthRootSlash(expected)
}

func trimMCPOAuthRootSlash(issuer string) string {
	trimmed := strings.TrimSuffix(issuer, "/")
	if trimmed == issuer {
		return issuer
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return issuer
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return trimmed
	}
	return issuer
}

// resolveEnterpriseAuthorizationMetadata ports
// resolve_ema_idp_authorization_manager: the configured issuer must publish
// authorization metadata that is bound to it, with validated endpoints and an
// advertised public-client token endpoint auth method. Candidate-order and
// error semantics follow rmcp's discovery loop (network failures abort;
// non-200/ malformed responses try the next candidate; issuer problems abort).
func resolveEnterpriseAuthorizationMetadata(ctx context.Context, issuer string, client *http.Client) (*mcpEnterpriseAuthorizationMetadata, error) {
	issuer = strings.TrimSpace(issuer)
	if err := validateMCPEMAOAuthEndpoint(issuer, "enterprise IdP issuer"); err != nil {
		return nil, err
	}
	candidates, err := enterpriseDiscoveryURLs(issuer)
	if err != nil {
		return nil, errors.New("failed to discover enterprise IdP authorization metadata")
	}
	discoveryClient := mcpOAuthDiscoveryHTTPClient(client)
	for _, candidate := range candidates {
		metadata, err := fetchMCPEnterpriseAuthorizationMetadata(ctx, discoveryClient, candidate)
		if err != nil {
			return nil, err
		}
		if metadata == nil {
			continue
		}
		expected := enterpriseExpectedIssuerForDiscoveryURL(candidate)
		if expected != "" {
			received := strings.TrimSpace(metadata.Issuer)
			if received == "" {
				return nil, errors.New("failed to discover enterprise IdP authorization metadata")
			}
			if !enterpriseIssuerIdentifiersMatch(received, expected) {
				return nil, errors.New("failed to discover enterprise IdP authorization metadata")
			}
		}
		// The enterprise resolver additionally requires the published issuer to
		// equal the configured issuer exactly.
		if strings.TrimSpace(metadata.Issuer) != issuer {
			return nil, errors.New("enterprise IdP authorization metadata issuer does not match configuration")
		}
		if err := validateMCPEMAOAuthEndpoint(metadata.AuthorizationEndpoint, "enterprise IdP authorization endpoint"); err != nil {
			return nil, err
		}
		if err := validateMCPEMAOAuthEndpoint(metadata.TokenEndpoint, "enterprise IdP token endpoint"); err != nil {
			return nil, err
		}
		var methods []any
		if metadata.TokenEndpointAuthMethodsSupported != nil {
			methods = make([]any, 0, len(metadata.TokenEndpointAuthMethodsSupported))
			for _, method := range metadata.TokenEndpointAuthMethodsSupported {
				methods = append(methods, method)
			}
		}
		if err := validateMCPEMAPublicClientAuth(methods, "enterprise IdP"); err != nil {
			return nil, err
		}
		callbackMode, err := MCPOAuthCallbackModeForDiscovery(metadata.AuthorizationResponseIssParameterSupported, metadata.Issuer)
		if err != nil {
			return nil, err
		}
		return &mcpEnterpriseAuthorizationMetadata{
			Issuer:                strings.TrimSpace(metadata.Issuer),
			AuthorizationEndpoint: strings.TrimSpace(metadata.AuthorizationEndpoint),
			TokenEndpoint:         strings.TrimSpace(metadata.TokenEndpoint),
			CallbackMode:          callbackMode,
		}, nil
	}
	return nil, errors.New("enterprise IdP must publish authorization metadata")
}

// fetchMCPEnterpriseAuthorizationMetadata performs one strict discovery GET.
// A non-200 response or malformed JSON yields (nil, nil) so the caller tries the
// next candidate; a transport or context error aborts discovery.
func fetchMCPEnterpriseAuthorizationMetadata(ctx context.Context, client *http.Client, metadataURL string) (*oauthAuthorizationServerMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(metadataURL), nil)
	if err != nil {
		return nil, errors.New("failed to discover enterprise IdP authorization metadata")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("failed to discover enterprise IdP authorization metadata")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, nil
	}
	var metadata oauthAuthorizationServerMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, mcpOAuthMetadataMaxBytes)).Decode(&metadata); err != nil {
		return nil, nil
	}
	return &metadata, nil
}

// EnterpriseOAuthLoginOptions describes a host-registered enterprise login. The
// caller owns the loopback listener (Go's OAuthLoginServer owns the callback
// server) and passes the already-bound redirect URL.
type EnterpriseOAuthLoginOptions struct {
	CodexHome      string
	CredentialName string
	Issuer         string
	ClientID       string
	RedirectURL    string
	HTTPClient     *http.Client
	Timeout        time.Duration
}

// EnterpriseOAuthLoginHandle owns an enterprise authorization attempt. It
// returns an authorization URL and stages validated credentials; dropping it
// without committing writes nothing.
type EnterpriseOAuthLoginHandle struct {
	credentialName   string
	issuer           string
	session          *OAuthLoginSession
	authorizationURL string
	generation       mcpOAuthEnterpriseGeneration
	httpClient       *http.Client
}

// StartEnterpriseOAuthLogin discovers the enterprise IdP metadata, captures the
// login generation, and builds the authorization URL. The browser is not
// launched and no credential is persisted.
func StartEnterpriseOAuthLogin(ctx context.Context, options *EnterpriseOAuthLoginOptions) (*EnterpriseOAuthLoginHandle, error) {
	if options == nil {
		return nil, errors.New("failed to start enterprise IdP authorization")
	}
	credentialName := strings.TrimSpace(options.CredentialName)
	issuer := strings.TrimSpace(options.Issuer)
	redirectURL := strings.TrimSpace(options.RedirectURL)
	if credentialName == "" || issuer == "" || redirectURL == "" {
		return nil, errors.New("failed to start enterprise IdP authorization")
	}
	if _, _, err := enterpriseCallbackSettings(issuer, options.ClientID, redirectURL, nil); err != nil {
		return nil, err
	}
	// Capture the generation before discovery or browser setup, then release
	// the lock while the user signs in.
	guard, err := AcquireEnterpriseOAuthCredentialGuard(options.CodexHome, credentialName, issuer)
	if err != nil {
		return nil, err
	}
	generation, ok, err := guard.generationFile.current()
	if err != nil {
		guard.Close()
		return nil, errors.New("failed to read enterprise login generation")
	}
	if !ok {
		generation, err = guard.generationFile.replace()
		if err != nil {
			guard.Close()
			return nil, errors.New("failed to initialize enterprise login generation")
		}
	}
	guard.Close()

	metadata, err := resolveEnterpriseAuthorizationMetadata(ctx, issuer, options.HTTPClient)
	if err != nil {
		return nil, err
	}
	session, err := NewOAuthLoginSession(&OAuthLoginSessionOptions{
		ServerURL:             issuer,
		ClientID:              strings.TrimSpace(options.ClientID),
		Issuer:                metadata.Issuer,
		AuthorizationEndpoint: metadata.AuthorizationEndpoint,
		TokenEndpoint:         metadata.TokenEndpoint,
		RedirectURL:           redirectURL,
		Scopes:                []string{"openid", "offline_access"},
		CallbackMode:          metadata.CallbackMode,
	})
	if err != nil {
		return nil, errors.New("failed to start enterprise IdP authorization")
	}
	authorizationURL, err := enterpriseAuthorizationURL(session.AuthorizationURL)
	if err != nil {
		return nil, errors.New("failed to start enterprise IdP authorization")
	}
	return &EnterpriseOAuthLoginHandle{
		credentialName:   credentialName,
		issuer:           issuer,
		session:          session,
		authorizationURL: authorizationURL,
		generation:       generation,
		httpClient:       options.HTTPClient,
	}, nil
}

// AuthorizationURL returns the enterprise authorization URL.
func (h *EnterpriseOAuthLoginHandle) AuthorizationURL() string {
	if h == nil {
		return ""
	}
	return h.authorizationURL
}

// Complete exchanges a callback for a validated grant without persisting it.
func (h *EnterpriseOAuthLoginHandle) Complete(ctx context.Context, rawCallbackPath string) (*EnterpriseOAuthCredentials, error) {
	if h == nil || h.session == nil {
		return nil, errors.New("enterprise IdP authorization failed")
	}
	tokens, err := h.session.CompleteCallback(ctx, rawCallbackPath, NewOAuthTokenClient(h.httpClient), h.credentialName)
	if err != nil {
		return nil, errors.New("enterprise IdP authorization failed")
	}
	if err := validateEnterpriseOAuthCredentials(tokens); err != nil {
		return nil, err
	}
	return &EnterpriseOAuthCredentials{Tokens: tokens, generation: h.generation}, nil
}

// EnterpriseOAuthCredentials is a validated grant that has not been stored. It
// intentionally does not expose secrets beyond the staged token set.
type EnterpriseOAuthCredentials struct {
	Tokens     *OAuthTokenSet
	generation mcpOAuthEnterpriseGeneration
}

// CommitIf revalidates the staged attempt and stores it under the credential
// lock (Rust EnterpriseOAuthCredentials::commit_if). Use the generic
// CommitEnterpriseOAuthCredentials helper; this method exposes the staged
// generation for callers that manage the commit themselves.
func (c *EnterpriseOAuthCredentials) CommitGeneration() mcpOAuthEnterpriseGeneration {
	if c == nil {
		return mcpOAuthEnterpriseGeneration{}
	}
	return c.generation
}
