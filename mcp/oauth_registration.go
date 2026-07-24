package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OAuthClientRegistrationOptions struct {
	RegistrationEndpoint    string
	ClientName              string
	RedirectURIs            []string
	Scopes                  []string
	GrantTypes              []string
	ResponseTypes           []string
	TokenEndpointAuthMethod string
}

type OAuthClientRegistrationResponse struct {
	ClientID                string         `json:"client_id"`
	ClientSecret            string         `json:"client_secret,omitempty"`
	ClientIDIssuedAt        *int64         `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt   *int64         `json:"client_secret_expires_at,omitempty"`
	TokenEndpointAuthMethod string         `json:"token_endpoint_auth_method,omitempty"`
	Raw                     map[string]any `json:"-"`
}

type OAuthClientRegistrar struct {
	Client *http.Client
}

func NewOAuthClientRegistrar(client *http.Client) *OAuthClientRegistrar {
	return &OAuthClientRegistrar{Client: mcpHTTPClientWithDefaultHeaders(client, nil)}
}

func (r *OAuthClientRegistrar) Register(ctx context.Context, options *OAuthClientRegistrationOptions) (*OAuthClientRegistrationResponse, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth client registration options are required")
	}
	endpoint := strings.TrimSpace(options.RegistrationEndpoint)
	if endpoint == "" {
		return nil, errors.New("MCP OAuth registration endpoint is required")
	}
	redirectURIs := uniqueNonEmptyStrings(options.RedirectURIs)
	if len(redirectURIs) == 0 {
		return nil, errors.New("MCP OAuth redirect URI is required")
	}
	payload := map[string]any{
		"redirect_uris":              redirectURIs,
		"grant_types":                registrationValuesOrDefault(options.GrantTypes, []string{"authorization_code", "refresh_token"}),
		"response_types":             registrationValuesOrDefault(options.ResponseTypes, []string{"code"}),
		"token_endpoint_auth_method": firstNonEmptyMCP(options.TokenEndpointAuthMethod, "none"),
	}
	if name := strings.TrimSpace(options.ClientName); name != "" {
		payload["client_name"] = name
	} else {
		payload["client_name"] = "Codex"
	}
	if scopes := normalizeMCPOAuthScopes(options.Scopes); len(scopes) > 0 {
		payload["scope"] = strings.Join(scopes, " ")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := http.DefaultClient
	if r != nil && r.Client != nil {
		client = r.Client
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return nil, fmt.Errorf("MCP OAuth client registration failed: %s: %s", response.Status, detail)
		}
		return nil, fmt.Errorf("MCP OAuth client registration failed: %s", response.Status)
	}
	raw := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(response.Body, mcpOAuthMetadataMaxBytes)).Decode(&raw); err != nil {
		return nil, err
	}
	out := oauthClientRegistrationResponseFromMap(raw)
	if strings.TrimSpace(out.ClientID) == "" {
		return nil, errors.New("MCP OAuth registration response missing client_id")
	}
	return out, nil
}

func registrationValuesOrDefault(values []string, fallback []string) []string {
	normalized := uniqueNonEmptyStrings(values)
	if len(normalized) == 0 {
		return append([]string(nil), fallback...)
	}
	return normalized
}

func oauthClientRegistrationResponseFromMap(raw map[string]any) *OAuthClientRegistrationResponse {
	out := &OAuthClientRegistrationResponse{Raw: cloneAnyMap(raw)}
	if raw == nil {
		return out
	}
	out.ClientID = stringFromAnyMap(raw, "client_id")
	out.ClientSecret = stringFromAnyMap(raw, "client_secret")
	out.TokenEndpointAuthMethod = stringFromAnyMap(raw, "token_endpoint_auth_method")
	out.ClientIDIssuedAt = int64PointerFromAny(raw["client_id_issued_at"])
	out.ClientSecretExpiresAt = int64PointerFromAny(raw["client_secret_expires_at"])
	return out
}

func int64PointerFromAny(value any) *int64 {
	switch v := value.(type) {
	case int64:
		return &v
	case int:
		out := int64(v)
		return &out
	case float64:
		out := int64(v)
		return &out
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			return &parsed
		}
	}
	return nil
}
