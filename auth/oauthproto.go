package auth

// Generic public-client OAuth protocol used by the gateway credential manager.
//
// Rust parity: codex-rs/login/src/oauth/{pkce,authorization,client,error,diagnostics}.rs.
// The shared operations never own credential state or choose a recovery policy:
// callers keep tokens, storage, and the decision to re-authorize. Token grants
// never follow redirects and never include primary-provider credentials or
// request logs.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// pkceCodes holds the S256 proof key for an authorization-code grant. The
// verifier is a credential and must never be logged.
type pkceCodes struct {
	codeVerifier  string
	codeChallenge string
}

func generatePKCE() (*pkceCodes, error) {
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(verifier))
	return &pkceCodes{codeVerifier: verifier, codeChallenge: base64.RawURLEncoding.EncodeToString(digest[:])}, nil
}

func generateOAuthState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

type authorizationRequest struct {
	endpoint        string
	clientID        string
	redirectURI     string
	scope           string
	resource        string
	extraParameters [][2]string
	pkce            *pkceCodes
	state           string
}

// buildAuthorizationURL appends the standard authorization parameters (plus
// issuer-specific extensions) in Rust's order; ordering matters for parity
// snapshots, and OAuth servers accept either order.
func buildAuthorizationURL(request authorizationRequest) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(request.endpoint))
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("invalid provider OAuth authorization endpoint")
	}
	var builder strings.Builder
	builder.WriteString(parsed.RawQuery)
	appendPair := func(key string, value string) {
		if builder.Len() > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(url.QueryEscape(key))
		builder.WriteByte('=')
		builder.WriteString(url.QueryEscape(value))
	}
	appendPair("response_type", "code")
	appendPair("client_id", request.clientID)
	appendPair("redirect_uri", request.redirectURI)
	appendPair("code_challenge", request.pkce.codeChallenge)
	appendPair("code_challenge_method", "S256")
	appendPair("state", request.state)
	if strings.TrimSpace(request.scope) != "" {
		appendPair("scope", request.scope)
	}
	if strings.TrimSpace(request.resource) != "" {
		appendPair("resource", request.resource)
	}
	for _, extra := range request.extraParameters {
		appendPair(extra[0], extra[1])
	}
	parsed.RawQuery = builder.String()
	return parsed.String(), nil
}

// callbackParameters are kept out of logs because code and state are credentials.
type callbackParameters struct {
	code             string
	state            string
	errorCode        string
	errorDescription string
}

type callbackErrorKind int

const (
	callbackErrorStateMismatch callbackErrorKind = iota
	callbackErrorProvider
	callbackErrorMissingCode
)

type callbackError struct {
	kind        callbackErrorKind
	code        string
	description string
}

func parseCallbackParameters(callback *url.URL) callbackParameters {
	query := callback.Query()
	return callbackParameters{
		code:             query.Get("code"),
		state:            query.Get("state"),
		errorCode:        query.Get("error"),
		errorDescription: query.Get("error_description"),
	}
}

// validate checks state before accepting an authorization code or provider error.
func (p callbackParameters) validate(expectedState string) (string, *callbackError) {
	if p.state != expectedState {
		return "", &callbackError{kind: callbackErrorStateMismatch}
	}
	if p.errorCode != "" {
		return "", &callbackError{kind: callbackErrorProvider, code: p.errorCode, description: p.errorDescription}
	}
	if strings.TrimSpace(p.code) == "" {
		return "", &callbackError{kind: callbackErrorMissingCode}
	}
	return p.code, nil
}

// tokenEncoding selects the token request body shape. ChatGPT refresh uses JSON;
// authorization-code and gateway grants use form encoding.
type tokenEncoding int

const (
	tokenEncodingForm tokenEncoding = iota
	tokenEncodingJSON
)

type errorBodyLimit struct {
	unlimited bool
	bytes     int
}

type tokenEndpoint struct {
	url            string
	clientID       string
	encoding       tokenEncoding
	timeout        time.Duration
	errorBodyLimit errorBodyLimit
}

type authorizationCodeGrant struct {
	code        string
	redirectURI string
	pkce        *pkceCodes
	resource    string
}

type refreshTokenGrant struct {
	refreshToken string
	resource     string
}

// oauthError kinds mirror Rust's OAuthError. Transport errors never carry the
// request URL's credentials, and decoder errors are opaque because a JSON
// decoder's message can include the offending token value.
type oauthErrorKind int

const (
	oauthErrorTransport oauthErrorKind = iota
	oauthErrorInvalidResponse
	oauthErrorRejected
)

type oauthError struct {
	kind      oauthErrorKind
	transport error
	rejection *tokenRejection
}

func (e *oauthError) Error() string {
	if e == nil {
		return ""
	}
	switch e.kind {
	case oauthErrorTransport:
		if e.transport == nil {
			return "OAuth token request failed"
		}
		return "OAuth token request failed: " + sanitizeURLForLogging(e.transport.Error())
	case oauthErrorRejected:
		if e.rejection == nil {
			return "OAuth token request was rejected"
		}
		return e.rejection.Error()
	default:
		return "OAuth token response is invalid"
	}
}

func (e *oauthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.transport
}

// rejected reports the sanitized rejection for refresh-recovery classification.
func (e *oauthError) rejected() *tokenRejection {
	if e == nil || e.kind != oauthErrorRejected {
		return nil
	}
	return e.rejection
}

// tokenRejection is a sanitized token-endpoint rejection: the HTTP status
// survives even when the body could not be read.
type tokenRejection struct {
	statusCode     int
	requestID      string
	errorCode      string
	displayMessage string
	bodyReadError  error
}

func (r *tokenRejection) Error() string {
	if r == nil {
		return ""
	}
	message := fmt.Sprintf("token endpoint returned status %d: %s", r.statusCode, r.displayMessage)
	if r.requestID != "" {
		message += " (request id: " + r.requestID + ")"
	}
	return message
}

var tokenRejectionRequestIDHeaders = []string{"x-request-id", "x-openai-request-id", "cf-ray"}

func newTokenRejection(statusCode int, header http.Header, body string, secrets []string) *tokenRejection {
	rejection := &tokenRejection{statusCode: statusCode}
	for _, name := range tokenRejectionRequestIDHeaders {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			rejection.requestID = truncateRunes(redactRequestSecrets(value, secrets), 128)
			break
		}
	}
	detail := parseTokenErrorDetail(body, secrets)
	rejection.errorCode = detail.errorCode
	rejection.displayMessage = detail.displayMessage
	return rejection
}

type tokenErrorDetail struct {
	// errorCode is the issuer's unmodified code, used for recovery classification.
	errorCode string
	// displayMessage is the redacted, caller-facing text.
	displayMessage string
}

func parseTokenErrorDetail(body string, secrets []string) tokenErrorDetail {
	trimmed := strings.TrimSpace(body)
	var parsed map[string]any
	_ = json.Unmarshal([]byte(trimmed), &parsed)
	nonemptyText := func(value any) string {
		text, _ := value.(string)
		return strings.TrimSpace(text)
	}
	displayCode := ""
	if parsed != nil {
		if value := nonemptyText(parsed["error"]); value != "" {
			displayCode = value
		} else if nested, ok := parsed["error"].(map[string]any); ok {
			displayCode = nonemptyText(nested["code"])
		}
	}
	code := displayCode
	if code == "" && parsed != nil {
		code = nonemptyText(parsed["code"])
	}
	message := ""
	if parsed != nil {
		message = nonemptyText(parsed["error_description"])
		if message == "" {
			if nested, ok := parsed["error"].(map[string]any); ok {
				message = nonemptyText(nested["message"])
			}
		}
	}
	display := message
	if display == "" {
		display = displayCode
	}
	if display == "" {
		if trimmed == "" {
			display = "unknown error"
		} else {
			display = trimmed
		}
	}
	return tokenErrorDetail{errorCode: code, displayMessage: redactRequestSecrets(display, secrets)}
}

// oauthClient executes OAuth grants against a token endpoint.
type oauthClient struct {
	client   *http.Client
	endpoint tokenEndpoint
}

func newOAuthClient(client *http.Client, endpoint tokenEndpoint) *oauthClient {
	return &oauthClient{client: client, endpoint: endpoint}
}

// exchangeCode performs the authorization-code grant and decodes the response
// into out.
func (c *oauthClient) exchangeCode(ctx context.Context, grant authorizationCodeGrant, out any) error {
	parameters := [][2]string{
		{"grant_type", "authorization_code"},
		{"client_id", c.endpoint.clientID},
		{"code", grant.code},
		{"redirect_uri", grant.redirectURI},
		{"code_verifier", grant.pkce.codeVerifier},
	}
	if strings.TrimSpace(grant.resource) != "" {
		parameters = append(parameters, [2]string{"resource", grant.resource})
	}
	return c.exchange(ctx, parameters, []string{grant.code, grant.pkce.codeVerifier}, out)
}

// refresh performs the refresh-token grant and decodes the response into out.
func (c *oauthClient) refresh(ctx context.Context, grant refreshTokenGrant, out any) error {
	parameters := [][2]string{
		{"grant_type", "refresh_token"},
		{"client_id", c.endpoint.clientID},
		{"refresh_token", grant.refreshToken},
	}
	if strings.TrimSpace(grant.resource) != "" {
		parameters = append(parameters, [2]string{"resource", grant.resource})
	}
	return c.exchange(ctx, parameters, []string{grant.refreshToken}, out)
}

func (c *oauthClient) exchange(ctx context.Context, parameters [][2]string, secrets []string, out any) error {
	if c.endpoint.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.endpoint.timeout)
		defer cancel()
	}
	request, err := c.buildRequest(ctx, parameters)
	if err != nil {
		return &oauthError{kind: oauthErrorTransport, transport: err}
	}
	response, err := c.client.Do(request)
	if err != nil {
		return &oauthError{kind: oauthErrorTransport, transport: err}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, readErr := readTokenErrorBody(response.Body, c.endpoint.errorBodyLimit)
		rejection := newTokenRejection(response.StatusCode, response.Header, body, secrets)
		if readErr != nil {
			rejection.bodyReadError = readErr
		}
		return &oauthError{kind: oauthErrorRejected, rejection: rejection}
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(out); err != nil {
		// Decoder errors can contain token values, including through their source.
		return &oauthError{kind: oauthErrorInvalidResponse}
	}
	return nil
}

func (c *oauthClient) buildRequest(ctx context.Context, parameters [][2]string) (*http.Request, error) {
	bodyValues := url.Values{}
	jsonBody := map[string]string{}
	for _, pair := range parameters {
		bodyValues.Add(pair[0], pair[1])
		jsonBody[pair[0]] = pair[1]
	}
	var (
		body        io.Reader
		contentType string
	)
	if c.endpoint.encoding == tokenEncodingJSON {
		encoded, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(string(encoded))
		contentType = "application/json"
	} else {
		body = strings.NewReader(bodyValues.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	requestContext := ctx
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.endpoint.url, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", contentType)
	return request, nil
}

// readTokenErrorBody honors the caller's diagnostic-read contract: a bounded
// read omits oversized bodies entirely so an echoed credential cannot be cut
// into a prefix that evades the caller's redaction.
func readTokenErrorBody(reader io.Reader, limit errorBodyLimit) (string, error) {
	if limit.unlimited {
		data, err := io.ReadAll(reader)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if limit.bytes <= 0 {
		return "", nil
	}
	buffer := make([]byte, 0, limit.bytes)
	chunk := make([]byte, 4096)
	for {
		read, err := reader.Read(chunk)
		if read > 0 {
			if len(buffer)+read > limit.bytes {
				return "", nil
			}
			buffer = append(buffer, chunk[:read]...)
		}
		if errors.Is(err, io.EOF) {
			return string(buffer), nil
		}
		if err != nil {
			return "", err
		}
	}
}

var sensitiveURLQueryKeys = []string{
	"access_token", "api_key", "client_secret", "code", "code_verifier", "id_token",
	"key", "refresh_token", "requested_token", "state", "subject_token", "token",
}

const redactedURLValue = "<redacted>"

// sanitizeURLForLogging redacts credentials in a free-form URL string, mirroring
// Rust's sanitize_url_for_logging (used for caller-supplied issuer values).
func sanitizeURLForLogging(raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return raw
	}
	// Transport errors embed a URL inside free-form text; redact any URL found.
	start := strings.Index(candidate, "http")
	if start < 0 {
		return raw
	}
	end := start
	for end < len(candidate) && !isURLTerminator(candidate[end]) {
		end++
	}
	parsed, err := url.Parse(candidate[start:end])
	if err != nil || parsed.Scheme == "" {
		return "<invalid-url>"
	}
	redactSensitiveURLParts(parsed)
	candidate = candidate[:start] + parsed.String() + candidate[end:]
	return candidate
}

func isURLTerminator(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '"', '\'', ')', ']', '}', ',', ';':
		return true
	default:
		return false
	}
}

func redactSensitiveURLParts(parsed *url.URL) {
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	if len(query) == 0 {
		parsed.RawQuery = ""
		return
	}
	redacted := url.Values{}
	for key, values := range query {
		for _, value := range values {
			if isSensitiveURLQueryKey(key) {
				redacted.Add(key, redactedURLValue)
				continue
			}
			redacted.Add(key, value)
		}
	}
	parsed.RawQuery = redacted.Encode()
}

func isSensitiveURLQueryKey(key string) bool {
	for _, candidate := range sensitiveURLQueryKeys {
		if strings.EqualFold(candidate, key) {
			return true
		}
	}
	return false
}

// redactRequestSecrets removes plain, JSON-escaped, and form-encoded spellings of
// caller-supplied secrets from diagnostic text.
func redactRequestSecrets(text string, secrets []string) string {
	redacted := text
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, url.QueryEscape(secret), "[REDACTED]")
		if encoded, err := json.Marshal(secret); err == nil && len(encoded) >= 2 {
			redacted = strings.ReplaceAll(redacted, string(encoded[1:len(encoded)-1]), "[REDACTED]")
		}
		redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
	}
	return redacted
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

