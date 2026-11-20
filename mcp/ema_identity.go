package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	managedconfig "codex_go/config"
)

// Enterprise IdP identity resolution mirrors Rust
// codex-rs/rmcp-client/src/ema_identity.rs (#55685, #62129): an admitted
// exchange subject (a refresh token, or a live ID token when the IdP issued no
// refresh token) is paired with the IdP token endpoint and client
// authentication that may receive it, plus the caller-owned credential lease
// that guards it.

// MCPEMAIDPIdentity is an opaque IdP exchange subject paired with the
// credentials admitted for its exchange. Credentials are caller-owned: a mapped
// lease must keep its owned refresh guard until the IdP exchange finishes.
type MCPEMAIDPIdentity[L any] struct {
	TokenEndpoint        string
	SubjectToken         MCPEMASubjectToken
	ClientAuthentication mcpEMAClientAuthentication
	Credentials          L
}

// MapMCPEMAIDPIdentityCredentials mirrors Rust EmaIdpIdentity::map_credentials:
// replace the credential lease while preserving the endpoint, subject token,
// and approved client authentication. The mapping must retain its owned
// refresh guard until the IdP exchange finishes.
func MapMCPEMAIDPIdentityCredentials[L, U any](identity MCPEMAIDPIdentity[L], mapCredentials func(L) U) MCPEMAIDPIdentity[U] {
	return MCPEMAIDPIdentity[U]{
		TokenEndpoint:        identity.TokenEndpoint,
		SubjectToken:         identity.SubjectToken,
		ClientAuthentication: identity.ClientAuthentication,
		Credentials:          mapCredentials(identity.Credentials),
	}
}

// MCPEMAAccountIDPIdentityRequest carries trusted persisted provenance plus one
// admitted account record whose ID and refresh tokens must both belong to that
// issuer and client.
type MCPEMAAccountIDPIdentityRequest[L any] struct {
	// IdP is the configured enterprise IdP profile; its issuer and client
	// authenticate discovery and the refresh-token exchange.
	IdP managedconfig.MCPServerIdPOAuthConfig
	// ClientID is the account OAuth client that issued the refresh token.
	ClientID string
	IDToken  string
	// RefreshToken is exchanged with the IdP only. It is never sent to the
	// resource authorization server.
	RefreshToken string
	Credentials  L
	HTTPClient   *http.Client
}

// ResolveMCPEMAAccountIDPIdentity mirrors Rust
// resolve_ema_account_idp_identity: corroborate the account's ID-token bindings
// against the trusted issuer and client before discovering the refresh-token
// endpoint. An expired ID token does not by itself invalidate the refresh
// grant, and this does not verify a JWT signature or authenticate an expired
// ID token.
func ResolveMCPEMAAccountIDPIdentity[L any](ctx context.Context, request MCPEMAAccountIDPIdentityRequest[L]) (MCPEMAIDPIdentity[L], error) {
	if strings.TrimSpace(request.RefreshToken) == "" {
		return MCPEMAIDPIdentity[L]{}, &EMAAuthFailure{
			Code:    EMAAuthFailureReauthenticationRequired,
			Context: "Codex account has no refresh token; sign in again",
		}
	}
	if request.IdP.ClientSecret != "" && request.ClientID != request.IdP.ClientID {
		return MCPEMAIDPIdentity[L]{}, errors.New("enterprise IdP client secret does not belong to the account OAuth client")
	}
	if _, err := mcpEMAOIDCIdentity(request.IDToken, request.IdP.Issuer, request.ClientID); err != nil {
		return MCPEMAIDPIdentity[L]{}, err
	}
	metadata, err := resolveMCPEMAIDPTokenEndpoint(
		ctx,
		request.IdP.Issuer,
		request.ClientID,
		request.IdP.ClientSecret,
		request.IdP.TokenEndpointAuthMethod,
		request.HTTPClient,
	)
	if err != nil {
		return MCPEMAIDPIdentity[L]{}, fmt.Errorf("enterprise IdP discovery or validation failed: %w", err)
	}
	return MCPEMAIDPIdentity[L]{
		TokenEndpoint:        metadata.TokenEndpoint,
		SubjectToken:         MCPEMARefreshTokenSubject(request.RefreshToken),
		ClientAuthentication: metadata.ClientAuthentication,
		Credentials:          request.Credentials,
	}, nil
}

// MCPEMAStoredSubject mirrors Rust stored_ema_subject: resolve the subject an
// enterprise sign-in presents to the ID-JAG exchange. A non-empty refresh token
// is preferred; it only corroborates the stored ID token's issuer, audience and
// account binding, so an expired ID token still leaves a valid refresh grant
// usable. Without a refresh token the ID token itself is the exchange
// credential, so it must also still be live. An unusable record requires a new
// sign-in instead of silently falling back to another credential.
func MCPEMAStoredSubject(tokens *OAuthTokenSet) (MCPEMASubjectToken, error) {
	if tokens == nil {
		return MCPEMASubjectToken{}, &EMAAuthFailure{
			Code:    EMAAuthFailureReauthenticationRequired,
			Context: "enterprise IdP session is missing; sign in again",
		}
	}
	assertion := tokens.IDToken
	if strings.TrimSpace(assertion) == "" {
		return MCPEMASubjectToken{}, &EMAAuthFailure{
			Code:    EMAAuthFailureReauthenticationRequired,
			Context: "enterprise IdP session has no OIDC ID token; sign in again",
		}
	}
	invalidIdentity := &EMAAuthFailure{
		Code:    EMAAuthFailureReauthenticationRequired,
		Context: "stored enterprise IdP identity is invalid; sign in again",
	}
	if refreshToken := tokens.RefreshToken; strings.TrimSpace(refreshToken) != "" {
		if _, err := mcpEMAOIDCIdentity(assertion, tokens.ServerURL, tokens.ClientID); err != nil {
			return MCPEMASubjectToken{}, invalidIdentity
		}
		return MCPEMARefreshTokenSubject(refreshToken), nil
	}
	if err := ValidateMCPEMAOIDCIdentityAssertion(assertion, tokens.ServerURL, tokens.ClientID); err != nil {
		return MCPEMASubjectToken{}, &EMAAuthFailure{
			Code:    EMAAuthFailureReauthenticationRequired,
			Context: "enterprise IdP ID token is expired or invalid; sign in again",
		}
	}
	return MCPEMAIDTokenSubject(assertion), nil
}

// MCPEMAStoredIdentityIsUsable mirrors Rust stored_ema_identity_is_usable: a
// saved enterprise sign-in can authenticate the registration only when it is
// bound to the configured issuer and client and yields an exchange subject.
func MCPEMAStoredIdentityIsUsable(tokens *OAuthTokenSet, issuer string, clientID string) bool {
	if tokens == nil {
		return false
	}
	if tokens.ServerURL != issuer || strings.TrimSpace(tokens.Issuer) != issuer || tokens.ClientID != clientID {
		return false
	}
	_, err := MCPEMAStoredSubject(tokens)
	return err == nil
}

// MCPEMAAccountOIDCIdentityIsUsable mirrors Rust
// account_oidc_identity_is_usable: validate account ID-token bindings without
// treating an expired ID token as an expired grant.
func MCPEMAAccountOIDCIdentityIsUsable(idToken string, issuer string, clientID string) bool {
	_, err := mcpEMAOIDCIdentity(idToken, issuer, clientID)
	return err == nil
}

// MCPEMAAccountOIDCIssuerHint mirrors Rust account_oidc_issuer_hint: an
// unverified issuer claim used only to explain a legacy login migration. Never
// use this hint to choose where a refresh token is sent.
func MCPEMAAccountOIDCIssuerHint(idToken string, issuer string) bool {
	return mcpEMAOIDCIssuerHint(idToken, issuer)
}

// resolveMCPEMAIDPTokenEndpoint mirrors Rust resolve_ema_idp_token_endpoint:
// discover the configured IdP and require the advertised ID-JAG token-exchange
// capability that a refresh-token exchange depends on.
func resolveMCPEMAIDPTokenEndpoint(
	ctx context.Context,
	issuer string,
	clientID string,
	clientSecret string,
	method managedconfig.MCPEMATokenEndpointAuthMethod,
	client *http.Client,
) (*mcpEnterpriseAuthorizationMetadata, error) {
	metadata, err := resolveEnterpriseAuthorizationMetadata(ctx, issuer, clientID, clientSecret, method, client)
	if err != nil {
		return nil, err
	}
	if err := validateMCPEMAIDPTokenExchangeCapability(metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

// validateMCPEMAIDPTokenExchangeCapability mirrors Rust's capability gate: a
// published advertisement that omits ID-JAG identity chaining or the
// token-exchange grant type rejects the exchange, while an absent list is
// tolerated.
func validateMCPEMAIDPTokenExchangeCapability(metadata *mcpEnterpriseAuthorizationMetadata) error {
	if metadata == nil {
		return errors.New("enterprise IdP authorization metadata is required")
	}
	for _, capability := range []struct {
		advertised  []byte
		expected    string
		description string
	}{
		{metadata.IdentityChainingRequestedTokenTypesSupported, idJAGTokenType, "identity_chaining_requested_token_types_supported"},
		{metadata.GrantTypesSupported, mcpEMATokenExchangeGrantType, "grant_types_supported"},
	} {
		advertised, err := advertisedMCPEMAJSONCapability(capability.advertised, capability.expected, capability.description)
		if err != nil {
			return err
		}
		if advertised != nil && !*advertised {
			return errors.New("enterprise IdP does not advertise the required ID-JAG token exchange capability")
		}
	}
	return nil
}
