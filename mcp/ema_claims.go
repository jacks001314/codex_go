package mcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// idJAGTokenType mirrors Rust ID_JAG_TOKEN_TYPE.
const idJAGTokenType = "urn:ietf:params:oauth:token-type:id-jag"

// mcpEMAOAuthResource models Rust OAuthResource: a single string or an array of
// strings. `isExact` requires exactly one value matching the expectation.
type mcpEMAOAuthResource struct {
	single *string
	list   []string
}

func (r *mcpEMAOAuthResource) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return errors.New("resource value is empty")
	}
	switch trimmed[0] {
	case '"':
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		r.single = &value
		r.list = nil
		return nil
	case '[':
		var values []string
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		r.list = values
		r.single = nil
		return nil
	default:
		return errors.New("resource must be a string or an array of strings")
	}
}

func (r *mcpEMAOAuthResource) isExact(expected string) bool {
	if r == nil {
		return false
	}
	if r.single != nil {
		return *r.single == expected
	}
	return len(r.list) == 1 && r.list[0] == expected
}

func (r *mcpEMAOAuthResource) matches(expected string) (matches bool, multiple bool) {
	if r == nil {
		return false, false
	}
	if r.single != nil {
		return *r.single == expected, false
	}
	for _, value := range r.list {
		if value == expected {
			return true, len(r.list) > 1
		}
	}
	return false, len(r.list) > 1
}

type mcpEMAJWTParts struct {
	Header map[string]any
	Claims []byte
}

// mcpEMASignedJWTParts mirrors Rust signed_jwt: a compact JWS with three
// non-empty segments, a JSON header, and a non-empty non-"none" algorithm.
func mcpEMASignedJWTParts(token string) (*mcpEMAJWTParts, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("identity assertion is not a compact signed JWT")
	}
	headerSegment, payloadSegment, signatureSegment := parts[0], parts[1], parts[2]
	if headerSegment == "" || payloadSegment == "" || signatureSegment == "" {
		return nil, errors.New("identity assertion contains an empty JWT segment")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSegment)
	if err != nil {
		return nil, errors.New("invalid identity assertion JWT header")
	}
	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, errors.New("invalid identity assertion JWT header")
	}
	alg, _ := header["alg"].(string)
	if strings.TrimSpace(alg) == "" || strings.EqualFold(alg, "none") {
		return nil, errors.New("identity assertion is unsigned")
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		return nil, errors.New("invalid identity assertion JWT claims")
	}
	return &mcpEMAJWTParts{Header: header, Claims: claimsBytes}, nil
}

type mcpEMAOIDCClaims struct {
	Issuer   string               `json:"iss"`
	Subject  string               `json:"sub"`
	Audience *mcpEMAOAuthResource `json:"aud"`
	AZP      *string              `json:"azp"`
	Exp      uint64               `json:"exp"`
}

// mcpEMAOIDCIdentity mirrors Rust oidc_identity.
func mcpEMAOIDCIdentity(assertion string, expectedIssuer string, expectedAudience string) (*mcpEMAOIDCClaims, error) {
	parts, err := mcpEMASignedJWTParts(assertion)
	if err != nil {
		return nil, err
	}
	var claims mcpEMAOIDCClaims
	if err := json.Unmarshal(parts.Claims, &claims); err != nil {
		return nil, errors.New("invalid identity assertion JWT claims")
	}
	if claims.Audience == nil {
		return nil, errors.New("invalid identity assertion JWT claims")
	}
	if claims.Issuer != expectedIssuer || strings.TrimSpace(claims.Subject) == "" {
		return nil, errors.New("OIDC identity assertion issuer or subject does not match the enterprise IdP")
	}
	audienceMatches, multipleAudiences := claims.Audience.matches(expectedAudience)
	if !audienceMatches ||
		(claims.AZP != nil && *claims.AZP != expectedAudience) ||
		(multipleAudiences && (claims.AZP == nil || *claims.AZP != expectedAudience)) {
		return nil, errors.New("OIDC identity assertion audience or authorized party does not match the IdP client")
	}
	return &claims, nil
}

// ValidateMCPEMAOIDCIdentityAssertion mirrors Rust
// validate_oidc_identity_assertion.
func ValidateMCPEMAOIDCIdentityAssertion(assertion string, expectedIssuer string, expectedAudience string) error {
	claims, err := mcpEMAOIDCIdentity(assertion, expectedIssuer, expectedAudience)
	if err != nil {
		return err
	}
	if claims.Exp <= mcpEMANowUnix() {
		return errors.New("OIDC identity assertion is expired")
	}
	return nil
}

type mcpEMAIDJAGClaims struct {
	Issuer   string               `json:"iss"`
	Subject  string               `json:"sub"`
	Audience *mcpEMAOAuthResource `json:"aud"`
	ClientID string               `json:"client_id"`
	JTI      string               `json:"jti"`
	Exp      uint64               `json:"exp"`
	Iat      uint64               `json:"iat"`
	Resource *mcpEMAOAuthResource `json:"resource"`
	Scope    *string              `json:"scope"`
}

// MCPEMAIDJAGBinding carries the trusted values an ID-JAG must be bound to.
// Mirrors Rust IdJagBinding.
type MCPEMAIDJAGBinding struct {
	Issuer   string
	Audience string
	ClientID string
	Resource string
	// RequestedScopes is empty when the scope parameter was omitted, which is
	// not the same as an empty authorization ceiling.
	RequestedScopes map[string]struct{}
}

type mcpEMAIDJAGResponse struct {
	AccessToken     string               `json:"access_token"`
	IssuedTokenType string               `json:"issued_token_type"`
	TokenType       string               `json:"token_type"`
	Resource        *mcpEMAOAuthResource `json:"resource"`
	Scope           *string              `json:"scope"`
	RefreshToken    *string              `json:"refresh_token"`
}

// Validate mirrors Rust IdJagResponse::validate, returning the granted scopes.
func (r *mcpEMAIDJAGResponse) Validate(binding MCPEMAIDJAGBinding) (map[string]struct{}, error) {
	if r == nil {
		return nil, errors.New("enterprise IdP returned an unsupported ID-JAG token type or refresh token")
	}
	if r.IssuedTokenType != idJAGTokenType || r.TokenType != "N_A" || r.RefreshToken != nil {
		return nil, errors.New("enterprise IdP returned an unsupported ID-JAG token type or refresh token")
	}
	parts, err := mcpEMASignedJWTParts(r.AccessToken)
	if err != nil {
		return nil, err
	}
	typ, _ := parts.Header["typ"].(string)
	var claims mcpEMAIDJAGClaims
	if err := json.Unmarshal(parts.Claims, &claims); err != nil {
		return nil, errors.New("invalid identity assertion JWT claims")
	}
	if typ != "oauth-id-jag+jwt" ||
		claims.Issuer != binding.Issuer ||
		!claims.Audience.isExact(binding.Audience) ||
		claims.ClientID != binding.ClientID ||
		strings.TrimSpace(claims.Subject) == "" ||
		strings.TrimSpace(claims.JTI) == "" {
		return nil, errors.New("ID-JAG type, issuer, audience, client, subject, or JWT ID is invalid")
	}
	now := mcpEMANowUnix()
	if claims.Exp <= now || claims.Iat > now+60 {
		return nil, errors.New("enterprise IdP returned an expired or future-issued ID-JAG")
	}
	if !claims.Resource.isExact(binding.Resource) ||
		(r.Resource != nil && !r.Resource.isExact(binding.Resource)) {
		return nil, errors.New("ID-JAG must authorize exactly the configured MCP resource")
	}
	granted := map[string]struct{}{}
	if claims.Scope != nil {
		granted, err = mcpEMAParseScope(*claims.Scope)
		if err != nil {
			return nil, err
		}
	} else if len(binding.RequestedScopes) > 0 {
		return nil, errors.New("ID-JAG is missing the requested scope authorization")
	}
	if len(binding.RequestedScopes) > 0 && !mcpEMAScopeSubset(granted, binding.RequestedScopes) {
		return nil, errors.New("ID-JAG contains a scope outside the enterprise authorization request")
	}
	if r.Scope != nil {
		responseScopes, err := mcpEMAParseScope(*r.Scope)
		if err != nil {
			return nil, err
		}
		if !mcpEMAScopeSetEqual(responseScopes, granted) {
			return nil, errors.New("enterprise IdP token response scope does not match the signed ID-JAG")
		}
	} else if len(binding.RequestedScopes) > 0 && !mcpEMAScopeSetEqual(granted, binding.RequestedScopes) {
		return nil, errors.New("enterprise IdP token response omitted its narrowed scope")
	}
	return granted, nil
}

type mcpEMAMCPAccessTokenResponse struct {
	AccessToken  string               `json:"access_token"`
	TokenType    string               `json:"token_type"`
	ExpiresIn    *uint64              `json:"expires_in"`
	Resource     *mcpEMAOAuthResource `json:"resource"`
	Scope        *string              `json:"scope"`
	RefreshToken *string              `json:"refresh_token"`
}

// Validate mirrors Rust McpAccessTokenResponse::validate.
func (r *mcpEMAMCPAccessTokenResponse) Validate(resource string, idJAGScopes map[string]struct{}) (*MCPEMAAccessToken, error) {
	if r == nil {
		return nil, errors.New("MCP authorization server returned an invalid bearer token")
	}
	if !strings.EqualFold(r.TokenType, "bearer") || strings.TrimSpace(r.AccessToken) == "" {
		return nil, errors.New("MCP authorization server returned an invalid bearer token")
	}
	if r.RefreshToken != nil || (r.ExpiresIn != nil && *r.ExpiresIn == 0) {
		return nil, errors.New("MCP authorization server returned a refresh token or zero token lifetime")
	}
	// The stable EMA response does not require the resource to be echoed; when
	// present it must agree with the resource bound in the ID-JAG.
	if r.Resource != nil && !r.Resource.isExact(resource) {
		return nil, errors.New("MCP access token must authorize exactly the configured MCP resource")
	}
	// RFC 6749 defines an omitted scope as unchanged from the request; here that
	// authority is the scope carried by the validated ID-JAG.
	if r.Scope != nil {
		scopes, err := mcpEMAParseScope(*r.Scope)
		if err != nil {
			return nil, err
		}
		if !mcpEMAScopeSubset(scopes, idJAGScopes) {
			return nil, errors.New("MCP authorization server granted a scope outside the ID-JAG authorization")
		}
	}
	token := &MCPEMAAccessToken{AccessToken: r.AccessToken}
	if r.ExpiresIn != nil {
		lifetime := time.Duration(*r.ExpiresIn) * time.Second
		token.ExpiresIn = &lifetime
	}
	return token, nil
}

// mcpEMAParseScope mirrors Rust parse_scope: ASCII whitespace separated tokens
// with no duplicates and at least one scope.
func mcpEMAParseScope(scope string) (map[string]struct{}, error) {
	tokens := mcpEMASplitASCIIWhitespace(scope)
	scopes := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		scopes[token] = struct{}{}
	}
	if len(scopes) == 0 || len(scopes) != len(tokens) {
		return nil, errors.New("enterprise authorization contains malformed or duplicate scopes")
	}
	return scopes, nil
}

func mcpEMASplitASCIIWhitespace(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			return true
		default:
			return false
		}
	})
}

func mcpEMAScopeSubset(subset map[string]struct{}, superset map[string]struct{}) bool {
	for scope := range subset {
		if _, ok := superset[scope]; !ok {
			return false
		}
	}
	return true
}

func mcpEMAScopeSetEqual(left map[string]struct{}, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	return mcpEMAScopeSubset(left, right)
}

func mcpEMANowUnix() uint64 {
	now := time.Now().Unix()
	if now < 0 {
		return 0
	}
	return uint64(now)
}
