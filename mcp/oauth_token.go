package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

type OAuthTokenClient struct {
	Client *http.Client
}

type OAuthCodeExchangeOptions struct {
	ServerName            string
	ServerURL             string
	ClientID              string
	ClientSecret          string
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RedirectURL           string
	Code                  string
	CodeVerifier          string
	Scopes                []string
}

type OAuthRefreshOptions struct {
	ServerName      string
	ServerURL       string
	ClientID        string
	ClientSecret    string
	Issuer          string
	TokenEndpoint   string
	AccessToken     string
	RefreshToken    string
	Scopes          []string
	ExpiresAtMillis *int64
}

func NewOAuthTokenClient(client *http.Client) *OAuthTokenClient {
	return &OAuthTokenClient{Client: mcpHTTPClientWithDefaultHeaders(client, nil)}
}

func (c *OAuthTokenClient) ExchangeAuthorizationCode(ctx context.Context, options *OAuthCodeExchangeOptions) (*OAuthTokenSet, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth code exchange options are required")
	}
	if strings.TrimSpace(options.Code) == "" {
		return nil, errors.New("MCP OAuth authorization code is required")
	}
	config, err := oauth2ConfigForCodeExchange(options)
	if err != nil {
		return nil, err
	}
	exchangeOptions := []oauth2.AuthCodeOption{}
	if verifier := strings.TrimSpace(options.CodeVerifier); verifier != "" {
		exchangeOptions = append(exchangeOptions, oauth2.VerifierOption(verifier))
	}
	token, err := config.Exchange(oauth2Context(ctx, c.Client), strings.TrimSpace(options.Code), exchangeOptions...)
	if err != nil {
		return nil, err
	}
	return oauth2TokenToSet(token, &oauthTokenSetOptions{
		ServerName:   options.ServerName,
		ServerURL:    options.ServerURL,
		ClientID:     options.ClientID,
		ClientSecret: options.ClientSecret,
		Issuer:       options.Issuer,
		Scopes:       options.Scopes,
	}), nil
}

func (c *OAuthTokenClient) RefreshToken(ctx context.Context, options *OAuthRefreshOptions) (*OAuthTokenSet, error) {
	if options == nil {
		return nil, errors.New("MCP OAuth refresh options are required")
	}
	if strings.TrimSpace(options.RefreshToken) == "" {
		return nil, errors.New("MCP OAuth refresh token is required")
	}
	config, err := oauth2ConfigForRefresh(options)
	if err != nil {
		return nil, err
	}
	current := &oauth2.Token{
		AccessToken:  strings.TrimSpace(options.AccessToken),
		RefreshToken: strings.TrimSpace(options.RefreshToken),
		Expiry:       time.Now().Add(-time.Second),
	}
	token, err := config.TokenSource(oauth2Context(ctx, c.Client), current).Token()
	if err != nil {
		return nil, err
	}
	return oauth2TokenToSet(token, &oauthTokenSetOptions{
		ServerName:   options.ServerName,
		ServerURL:    options.ServerURL,
		ClientID:     options.ClientID,
		ClientSecret: options.ClientSecret,
		Issuer:       options.Issuer,
		Scopes:       options.Scopes,
	}), nil
}

func oauth2ConfigForCodeExchange(options *OAuthCodeExchangeOptions) (*oauth2.Config, error) {
	if strings.TrimSpace(options.ServerName) == "" || strings.TrimSpace(options.ServerURL) == "" {
		return nil, errors.New("MCP OAuth server name and URL are required")
	}
	if strings.TrimSpace(options.ClientID) == "" {
		return nil, errors.New("MCP OAuth client ID is required")
	}
	if strings.TrimSpace(options.TokenEndpoint) == "" {
		return nil, errors.New("MCP OAuth token endpoint is required")
	}
	return &oauth2.Config{
		ClientID:     strings.TrimSpace(options.ClientID),
		ClientSecret: strings.TrimSpace(options.ClientSecret),
		RedirectURL:  strings.TrimSpace(options.RedirectURL),
		Scopes:       normalizeMCPOAuthScopes(options.Scopes),
		Endpoint: oauth2.Endpoint{
			AuthURL:   strings.TrimSpace(options.AuthorizationEndpoint),
			TokenURL:  strings.TrimSpace(options.TokenEndpoint),
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}, nil
}

func oauth2ConfigForRefresh(options *OAuthRefreshOptions) (*oauth2.Config, error) {
	if strings.TrimSpace(options.ServerName) == "" || strings.TrimSpace(options.ServerURL) == "" {
		return nil, errors.New("MCP OAuth server name and URL are required")
	}
	if strings.TrimSpace(options.ClientID) == "" {
		return nil, errors.New("MCP OAuth client ID is required")
	}
	if strings.TrimSpace(options.TokenEndpoint) == "" {
		return nil, errors.New("MCP OAuth token endpoint is required")
	}
	return &oauth2.Config{
		ClientID:     strings.TrimSpace(options.ClientID),
		ClientSecret: strings.TrimSpace(options.ClientSecret),
		Scopes:       normalizeMCPOAuthScopes(options.Scopes),
		Endpoint: oauth2.Endpoint{
			TokenURL:  strings.TrimSpace(options.TokenEndpoint),
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}, nil
}

type oauthTokenSetOptions struct {
	ServerName   string
	ServerURL    string
	ClientID     string
	ClientSecret string
	Issuer       string
	Scopes       []string
}

func oauth2TokenToSet(token *oauth2.Token, options *oauthTokenSetOptions) *OAuthTokenSet {
	if token == nil || options == nil {
		return nil
	}
	tokens := &OAuthTokenSet{
		ServerName:   strings.TrimSpace(options.ServerName),
		ServerURL:    strings.TrimSpace(options.ServerURL),
		ClientID:     strings.TrimSpace(options.ClientID),
		ClientSecret: strings.TrimSpace(options.ClientSecret),
		Issuer:       strings.TrimSpace(options.Issuer),
		AccessToken:  strings.TrimSpace(token.AccessToken),
		RefreshToken: strings.TrimSpace(token.RefreshToken),
		Scopes:       scopesFromOAuth2Token(token, options.Scopes),
	}
	if !token.Expiry.IsZero() {
		expiresAt := token.Expiry.UnixMilli()
		tokens.ExpiresAtMillis = &expiresAt
	}
	return tokens
}

func scopesFromOAuth2Token(token *oauth2.Token, fallback []string) []string {
	if token == nil {
		return normalizeMCPOAuthScopes(fallback)
	}
	if raw := token.Extra("scope"); raw != nil {
		switch value := raw.(type) {
		case string:
			if scopes := normalizeMCPOAuthScopes(strings.Fields(value)); len(scopes) > 0 {
				return scopes
			}
		case []string:
			if scopes := normalizeMCPOAuthScopes(value); len(scopes) > 0 {
				return scopes
			}
		case []any:
			values := make([]string, 0, len(value))
			for _, item := range value {
				if text, ok := item.(string); ok {
					values = append(values, text)
				}
			}
			if scopes := normalizeMCPOAuthScopes(values); len(scopes) > 0 {
				return scopes
			}
		}
	}
	return normalizeMCPOAuthScopes(fallback)
}

func oauth2Context(ctx context.Context, client *http.Client) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}
