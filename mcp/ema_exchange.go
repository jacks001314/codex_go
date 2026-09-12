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
)

// Enterprise ID-JAG exchange grant types mirror Rust
// TOKEN_EXCHANGE_GRANT_TYPE / JWT_BEARER_GRANT_TYPE.
const (
	mcpEMATokenExchangeGrantType = "urn:ietf:params:oauth:grant-type:token-exchange"
	mcpEMAJWTBearerGrantType     = "urn:ietf:params:oauth:grant-type:jwt-bearer"
)

// mcpEMAExchangeTimeout bounds each exchange request (Rust uses 30s).
const mcpEMAExchangeTimeout = 30 * time.Second

// MCPEMAAccessToken is a resource-bound bearer and its server-reported
// lifetime. String redacts the token like Rust's custom Debug impl.
type MCPEMAAccessToken struct {
	AccessToken string
	ExpiresIn   *time.Duration
}

func (t MCPEMAAccessToken) String() string {
	return fmt.Sprintf("MCPEMAAccessToken{access_token:[REDACTED] expires_in:%v}", mcpEMADurationPtrString(t.ExpiresIn))
}

func (t MCPEMAAccessToken) GoString() string {
	return t.String()
}

func mcpEMADurationPtrString(value *time.Duration) string {
	if value == nil {
		return "<nil>"
	}
	return value.String()
}

// MCPEMAIDJAGExchangeRequest carries trusted authorization-server metadata and
// an IdP credential. The exchange primitive performs no resource discovery or
// interactive login. Mirrors Rust EmaIdJagExchangeRequest.
type MCPEMAIDJAGExchangeRequest struct {
	Resource                         string
	Scopes                           []string
	MCPClientID                      string
	AuthorizationServerIssuer        string
	AuthorizationServerTokenEndpoint string
	IDPTokenEndpoint                 string
	IDPIssuer                        string
	IDPClientID                      string
	RefreshToken                     string
	IDPHTTPClient                    *http.Client
	ResourceHTTPClient               *http.Client
}

// ExchangeMCPEMAIDJAG mirrors Rust exchange_id_jag: an enterprise IdP
// credential is exchanged for an ID-JAG, which is then traded for a
// resource-bound MCP bearer token.
func ExchangeMCPEMAIDJAG(ctx context.Context, request MCPEMAIDJAGExchangeRequest) (*MCPEMAAccessToken, error) {
	for _, endpoint := range []struct {
		value       string
		description string
	}{
		{request.Resource, "enterprise MCP resource"},
		{request.AuthorizationServerIssuer, "MCP authorization server issuer"},
		{request.AuthorizationServerTokenEndpoint, "MCP token endpoint"},
		{request.IDPIssuer, "enterprise IdP issuer"},
		{request.IDPTokenEndpoint, "enterprise IdP token endpoint"},
	} {
		if err := validateMCPEMAOAuthEndpoint(endpoint.value, endpoint.description); err != nil {
			return nil, err
		}
	}
	if request.AuthorizationServerIssuer == request.IDPIssuer {
		return nil, errors.New("enterprise IdP and MCP authorization server issuers must be different for ID-JAG")
	}
	if strings.TrimSpace(request.MCPClientID) == "" || strings.TrimSpace(request.IDPClientID) == "" {
		return nil, errors.New("enterprise authorization requires the registered IdP and MCP client IDs")
	}
	if strings.TrimSpace(request.RefreshToken) == "" {
		return nil, errors.New("enterprise IdP refresh token must not be empty")
	}
	requestedScopes := make(map[string]struct{}, len(request.Scopes))
	validScopes := true
	for _, scope := range request.Scopes {
		if scope == "" || strings.ContainsFunc(scope, mcpEMAIsASCIIWhitespace) {
			validScopes = false
		}
		if _, duplicate := requestedScopes[scope]; duplicate {
			validScopes = false
		}
		requestedScopes[scope] = struct{}{}
	}
	if len(requestedScopes) != len(request.Scopes) {
		validScopes = false
	}
	if !validScopes {
		return nil, errors.New("enterprise MCP authorization scopes must be distinct, non-empty scope tokens")
	}
	params := [][2]string{
		{"grant_type", mcpEMATokenExchangeGrantType},
		{"requested_token_type", idJAGTokenType},
		{"audience", request.AuthorizationServerIssuer},
		{"resource", request.Resource},
		{"subject_token", request.RefreshToken},
		{"subject_token_type", "urn:ietf:params:oauth:token-type:refresh_token"},
	}
	if len(request.Scopes) > 0 {
		params = append(params, [2]string{"scope", strings.Join(request.Scopes, " ")})
	}
	idJag, err := postMCPEMAForm[mcpEMAIDJAGResponse](
		ctx,
		request.IDPHTTPClient,
		request.IDPTokenEndpoint,
		params,
		request.IDPClientID,
		EMAInvalidGrantSourceEnterpriseIdentity,
		"enterprise IdP ID-JAG exchange",
	)
	if err != nil {
		return nil, err
	}
	grantedScopes, err := idJag.Validate(MCPEMAIDJAGBinding{
		Issuer:          request.IDPIssuer,
		Audience:        request.AuthorizationServerIssuer,
		ClientID:        request.MCPClientID,
		Resource:        request.Resource,
		RequestedScopes: requestedScopes,
	})
	if err != nil {
		return nil, err
	}
	// Only the signed assertion carries authority to the Resource AS. Repeating
	// the requested resource or scopes could undo enterprise policy narrowing.
	accessToken, err := postMCPEMAForm[mcpEMAMCPAccessTokenResponse](
		ctx,
		request.ResourceHTTPClient,
		request.AuthorizationServerTokenEndpoint,
		[][2]string{
			{"grant_type", mcpEMAJWTBearerGrantType},
			{"assertion", idJag.AccessToken},
		},
		request.MCPClientID,
		EMAInvalidGrantSourceResourceAuthorization,
		"MCP JWT bearer exchange",
	)
	if err != nil {
		return nil, err
	}
	return accessToken.Validate(request.Resource, grantedScopes)
}

func mcpEMAIsASCIIWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

type mcpEMAOAuthErrorResponse struct {
	Error *string `json:"error"`
}

// postMCPEMAForm mirrors Rust post_form: a non-redirecting form POST whose
// provider-controlled error text never reaches callers verbatim.
func postMCPEMAForm[T any](
	ctx context.Context,
	client *http.Client,
	endpoint string,
	params [][2]string,
	clientID string,
	invalidGrantSource EMAInvalidGrantSource,
	operation string,
) (*T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body := mcpEMAEncodeForm(params, clientID)
	postClient := mcpHTTPClientWithDefaultHeaders(client, nil)
	cloned := *postClient
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	requestCtx, cancel := context.WithTimeout(ctx, mcpEMAExchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", operation, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := cloned.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", operation, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, mcpEMAMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", operation, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var oauthError mcpEMAOAuthErrorResponse
		_ = json.Unmarshal(responseBody, &oauthError)
		code := safeMCPEMAOAuthErrorCode(oauthError.Error)
		switch code {
		case string(EMAAuthFailureInvalidGrant):
			return nil, &EMAAuthFailure{
				Code:        EMAAuthFailureInvalidGrant,
				GrantSource: invalidGrantSource,
				Context:     fmt.Sprintf("%s returned HTTP %d: invalid_grant", operation, response.StatusCode),
			}
		case string(EMAAuthFailureInsufficientUserAuthn):
			return nil, &EMAAuthFailure{
				Code:    EMAAuthFailureInsufficientUserAuthn,
				Context: fmt.Sprintf("%s returned HTTP %d: insufficient_user_authentication", operation, response.StatusCode),
			}
		default:
			return nil, fmt.Errorf("%s returned HTTP %d: %s", operation, response.StatusCode, code)
		}
	}
	var decoded T
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse %s response", operation)
	}
	return &decoded, nil
}

const mcpEMAMaxResponseBytes = 1 << 20

// mcpEMAEncodeForm serializes fields in order and appends client_id, matching
// Rust's form serializer.
func mcpEMAEncodeForm(params [][2]string, clientID string) string {
	var builder strings.Builder
	writePair := func(name string, value string) {
		if builder.Len() > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(url.QueryEscape(name))
		builder.WriteByte('=')
		builder.WriteString(url.QueryEscape(value))
	}
	for _, pair := range params {
		writePair(pair[0], pair[1])
	}
	writePair("client_id", clientID)
	return builder.String()
}
