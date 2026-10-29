package app

// Responses API proxy authentication (Rust responses-api-proxy auth.rs,
// #55297/#55298): a locked API key or renewable federated OpenAI credentials
// obtained by exchanging a fresh identity-provider subject token. Token
// requests never follow redirects and never put credentials, request URLs, or
// response bodies into diagnostics.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	codexnetwork "codex_go/network"
)

const (
	responsesProxyOpenAITokenURL          = "https://auth.openai.com/oauth/token"
	responsesProxyMaxTokenBytes           = 16 * 1024
	responsesProxyMaxResponseBytes        = 64 * 1024
	responsesProxyMaxExchangeRequestBytes = 64 * 1024
	responsesProxyTokenRequestTimeout     = 30 * time.Second
	responsesProxyTokenConnectTimeout     = 10 * time.Second
	responsesProxyMaxFederatedLifetime    = time.Hour
	responsesProxyExchangeGrantType       = "urn:ietf:params:oauth:grant-type:token-exchange"
	responsesProxyJWTTokenType            = "urn:ietf:params:oauth:token-type:jwt"
	responsesProxyAccessTokenType         = "urn:ietf:params:oauth:token-type:access_token"
)

// responsesProxyAuth supplies the Authorization header for one upstream
// request. FollowsRedirects reports whether the upstream client may follow
// redirects; a federated credential must never be sent to a redirect target.
type responsesProxyAuth interface {
	Header(ctx context.Context) (string, error)
	FollowsRedirects() bool
}

// responsesProxyAPIKeyAuth is the locked, static API key path.
type responsesProxyAPIKeyAuth struct {
	header string
}

func (a responsesProxyAPIKeyAuth) Header(context.Context) (string, error) {
	return a.header, nil
}

func (responsesProxyAPIKeyAuth) FollowsRedirects() bool {
	return true
}

// responsesProxyTokenClient is the credential-acquisition client: it honors
// system and environment proxy settings, bounds the connect timeout, and never
// follows redirects (Rust TokenClient).
type responsesProxyTokenClient struct {
	client *http.Client
}

func newResponsesProxyTokenClient() *responsesProxyTokenClient {
	client := codexnetwork.NewHTTPClient(true, 0)
	if transport := codexnetwork.BaseTransport(client.Transport); transport != nil {
		transport = transport.Clone()
		transport.DialContext = (&net.Dialer{
			Timeout:   responsesProxyTokenConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
		client.Transport = codexnetwork.ReplaceBaseTransport(client.Transport, transport)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &responsesProxyTokenClient{client: client}
}

// responsesProxySubjectTokenSource supplies a fresh JWT whenever an OpenAI
// credential needs to be issued. Implementations must not include tokens,
// credential-bearing URLs, or upstream response bodies in errors.
type responsesProxySubjectTokenSource interface {
	SubjectToken(ctx context.Context, client *responsesProxyTokenClient) (string, error)
}

// responsesProxyFederatedAuth exchanges a subject token for an OpenAI access
// token, serializes concurrent exchanges, and renews proactively.
type responsesProxyFederatedAuth struct {
	mu             sync.Mutex
	client         *responsesProxyTokenClient
	tokenURL       *url.URL
	source         responsesProxySubjectTokenSource
	provider       string
	serviceAccount string
	cached         *responsesProxyCachedToken
}

type responsesProxyCachedToken struct {
	header    string
	renewAt   time.Time
	expiresAt time.Time
}

func newResponsesProxyFederatedAuth(source responsesProxySubjectTokenSource, provider, serviceAccount string) (*responsesProxyFederatedAuth, error) {
	return newResponsesProxyFederatedAuthWith(source, provider, serviceAccount, responsesProxyOpenAITokenURL, newResponsesProxyTokenClient())
}

func newResponsesProxyFederatedAuthWith(source responsesProxySubjectTokenSource, provider, serviceAccount, tokenURL string, client *responsesProxyTokenClient) (*responsesProxyFederatedAuth, error) {
	if source == nil {
		return nil, errors.New("workload identity subject token source is unavailable")
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(serviceAccount) == "" {
		return nil, errors.New("workload identity provider and service account are required")
	}
	if client == nil {
		client = newResponsesProxyTokenClient()
	}
	parsed, err := url.Parse(strings.TrimSpace(tokenURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid OpenAI token URL")
	}
	return &responsesProxyFederatedAuth{
		client:         client,
		tokenURL:       parsed,
		source:         source,
		provider:       provider,
		serviceAccount: serviceAccount,
	}, nil
}

func (*responsesProxyFederatedAuth) FollowsRedirects() bool {
	return false
}

func (a *responsesProxyFederatedAuth) Header(ctx context.Context) (string, error) {
	if a == nil {
		return "", errors.New("workload identity credential cache unavailable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if cached := a.cached; cached != nil && now.Before(cached.renewAt) && now.Before(cached.expiresAt) {
		return cached.header, nil
	}
	// Discard the old credential before a fallible exchange; never reuse it when
	// acquisition fails.
	a.cached = nil
	cached, err := a.exchange(ctx)
	if err != nil {
		return "", err
	}
	a.cached = cached
	return cached.header, nil
}

type responsesProxyExchangeRequest struct {
	GrantType          string `json:"grant_type"`
	SubjectTokenType   string `json:"subject_token_type"`
	SubjectToken       string `json:"subject_token"`
	IdentityProviderID string `json:"identity_provider_id"`
	ServiceAccountID   string `json:"service_account_id"`
}

type responsesProxyExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	TokenType       string `json:"token_type"`
	IssuedTokenType string `json:"issued_token_type"`
	ExpiresIn       uint64 `json:"expires_in"`
	ExpiresAt       uint64 `json:"expires_at"`
}

func (a *responsesProxyFederatedAuth) exchange(ctx context.Context) (*responsesProxyCachedToken, error) {
	subject, err := a.source.SubjectToken(ctx, a.client)
	if err != nil {
		return nil, err
	}
	if subject == "" || len(subject) > responsesProxyMaxTokenBytes {
		return nil, errors.New("workload identity source returned an invalid subject token")
	}
	body, err := json.Marshal(responsesProxyExchangeRequest{
		GrantType:          responsesProxyExchangeGrantType,
		SubjectTokenType:   responsesProxyJWTTokenType,
		SubjectToken:       subject,
		IdentityProviderID: a.provider,
		ServiceAccountID:   a.serviceAccount,
	})
	if err != nil {
		return nil, errors.New("could not encode OpenAI workload identity exchange request")
	}
	if len(body) > responsesProxyMaxExchangeRequestBytes {
		clear(body)
		return nil, fmt.Errorf("OpenAI workload identity exchange request exceeds %d bytes", responsesProxyMaxExchangeRequestBytes)
	}
	// Start the lifetime clock before the OpenAI exchange, after obtaining the
	// subject. expires_in is measured from issuance, not receipt.
	started := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, responsesProxyTokenRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.tokenURL.String(), bytes.NewReader(body))
	if err != nil {
		clear(body)
		return nil, errors.New("OpenAI workload identity token exchange failed")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.client.Do(request)
	clear(body)
	if err != nil {
		return nil, errors.New("OpenAI workload identity token exchange failed")
	}
	payload, err := readResponsesProxyTokenResponse(response, "OpenAI workload identity")
	if err != nil {
		return nil, err
	}
	var exchange responsesProxyExchangeResponse
	decodeErr := json.Unmarshal(payload, &exchange)
	clear(payload)
	if decodeErr != nil {
		return nil, errors.New("OpenAI workload identity returned an invalid token response")
	}
	if !strings.EqualFold(exchange.TokenType, "bearer") || exchange.IssuedTokenType != responsesProxyAccessTokenType {
		return nil, errors.New("OpenAI returned an unsupported workload identity token type")
	}
	if exchange.ExpiresIn > uint64(responsesProxyMaxFederatedLifetime/time.Second) {
		return nil, errors.New("OpenAI returned an invalid token lifetime")
	}
	header, err := responsesProxyBearerHeader(exchange.AccessToken)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	remainingAbsolute := time.Unix(int64(exchange.ExpiresAt), 0).Sub(now)
	if remainingAbsolute <= 0 {
		return nil, errors.New("OpenAI returned an expired workload identity token")
	}
	remainingRelative := time.Duration(exchange.ExpiresIn)*time.Second - time.Since(started)
	if remainingRelative <= 0 {
		return nil, errors.New("OpenAI returned an expired workload identity token")
	}
	remaining := remainingAbsolute
	if remainingRelative < remaining {
		remaining = remainingRelative
	}
	// Bound the wall-clock deadline too, even when the issuer's clock is ahead.
	expiresAt := now.Add(remaining)
	// Proactive renewal without a fixed skew that rejects short-lived subjects.
	skew := remaining / 10
	if skew > time.Minute {
		skew = time.Minute
	}
	return &responsesProxyCachedToken{
		header:    header,
		renewAt:   time.Now().Add(remaining - skew),
		expiresAt: expiresAt,
	}, nil
}

// readResponsesProxyTokenResponse reads one bounded token response without
// exposing its contents in errors. Redirects are failures.
func readResponsesProxyTokenResponse(response *http.Response, source string) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("%s returned an unreadable token response", source)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s token request rejected (HTTP %d)", source, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responsesProxyMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s returned an unreadable token response", source)
	}
	if len(body) > responsesProxyMaxResponseBytes {
		clear(body)
		return nil, fmt.Errorf("%s returned an oversized token response", source)
	}
	return body, nil
}

// responsesProxyBearerHeader validates an RFC 6750 bearer token (JWT periods
// allowed) and returns the Authorization header value.
func responsesProxyBearerHeader(token string) (string, error) {
	raw := []byte(token)
	if len(raw) == 0 || len(raw) > responsesProxyMaxTokenBytes || !responsesProxyBearerBytesValid(raw) {
		return "", errors.New("invalid workload identity bearer credential")
	}
	return "Bearer " + token, nil
}

func responsesProxyBearerBytesValid(token []byte) bool {
	for _, b := range token {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
			b == '-' || b == '.' || b == '_' || b == '~' || b == '+' || b == '/' || b == '=' {
			continue
		}
		return false
	}
	return true
}

// responsesProxyGithubOIDC obtains fresh GitHub Actions OIDC subject tokens.
type responsesProxyGithubOIDC struct {
	requestURL  *url.URL
	requestAuth string
}

func newResponsesProxyGithubOIDC(requestURL, audience string, stdin io.Reader) (*responsesProxyGithubOIDC, error) {
	// Avoid echoing caller-supplied URL parameters in parsing errors.
	parsed, err := url.Parse(strings.TrimSpace(requestURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("GitHub OIDC request URL must be HTTPS without user info or fragment")
	}
	query := parsed.Query()
	query.Del("audience")
	query.Set("audience", audience)
	parsed.RawQuery = query.Encode()
	requestAuth, err := readResponsesProxyGithubRequestAuth(stdin)
	if err != nil {
		return nil, err
	}
	return &responsesProxyGithubOIDC{requestURL: parsed, requestAuth: requestAuth}, nil
}

func (g *responsesProxyGithubOIDC) SubjectToken(ctx context.Context, client *responsesProxyTokenClient) (string, error) {
	if g == nil || client == nil {
		return "", errors.New("GitHub OIDC token request failed")
	}
	requestCtx, cancel := context.WithTimeout(ctx, responsesProxyTokenRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, g.requestURL.String(), nil)
	if err != nil {
		return "", errors.New("GitHub OIDC token request failed")
	}
	request.Header.Set("Authorization", g.requestAuth)
	response, err := client.client.Do(request)
	if err != nil {
		return "", errors.New("GitHub OIDC token request failed")
	}
	payload, err := readResponsesProxyTokenResponse(response, "GitHub OIDC")
	if err != nil {
		return "", err
	}
	var decoded responsesProxyGithubTokenResponse
	decodeErr := json.Unmarshal(payload, &decoded)
	clear(payload)
	if decodeErr != nil {
		return "", errors.New("GitHub OIDC returned an invalid token response")
	}
	return decoded.Value, nil
}

type responsesProxyGithubTokenResponse struct {
	Value string `json:"value"`
}

// responsesProxyGithubRequestAuth reads the bearer request credential the
// launcher passes on stdin once, for the lifetime of the proxy.
func readResponsesProxyGithubRequestAuth(stdin io.Reader) (string, error) {
	bufferSize := len("Bearer ") + responsesProxyMaxTokenBytes + 2
	token, err := readResponsesProxyCredential(stdin, bufferSize, validateResponsesProxyBearerToken, func() error {
		return errors.New("GitHub OIDC request credential too large")
	})
	if err != nil {
		return "", err
	}
	return "Bearer " + string(token), nil
}

// readResponsesProxyCredential reads one bounded credential line. The validator
// receives the raw token region and returns the validated prefix length.
func readResponsesProxyCredential(stdin io.Reader, bufferSize int, validate func([]byte) (int, error), tooLarge func() error) ([]byte, error) {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	capacity := bufferSize - len("Bearer ")
	if capacity <= 0 {
		return nil, tooLarge()
	}
	token := make([]byte, 0, capacity)
	chunk := make([]byte, 1024)
	sawNewline := false
	sawEOF := false
	for len(token) < capacity {
		readSize := capacity - len(token)
		if readSize > len(chunk) {
			readSize = len(chunk)
		}
		n, err := stdin.Read(chunk[:readSize])
		if n > 0 {
			data := chunk[:n]
			if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
				token = append(token, data[:idx+1]...)
				sawNewline = true
				break
			}
			token = append(token, data...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				sawEOF = true
				break
			}
			clear(token)
			return nil, err
		}
		if n == 0 {
			sawEOF = true
			break
		}
	}
	if len(token) >= capacity && !sawNewline && !sawEOF {
		clear(token)
		return nil, tooLarge()
	}
	length, err := validate(token)
	if err != nil {
		clear(token)
		return nil, err
	}
	return token[:length], nil
}

// validateResponsesProxyAPIKeyToken keeps the API key alphabet narrow.
func validateResponsesProxyAPIKeyToken(token []byte) (int, error) {
	trimmed := token
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '\n' || trimmed[len(trimmed)-1] == '\r') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return 0, errors.New("API key must be provided via stdin (e.g. printenv OPENAI_API_KEY | codex responses-api-proxy)")
	}
	for _, b := range trimmed {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' {
			continue
		}
		return 0, errors.New("API key may only contain ASCII letters, numbers, '-' or '_'")
	}
	return len(trimmed), nil
}

// validateResponsesProxyBearerToken accepts RFC 6750 characters, including JWT
// periods, and strips one trailing CRLF.
func validateResponsesProxyBearerToken(token []byte) (int, error) {
	trimmed := token
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\r' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 || len(trimmed) > responsesProxyMaxTokenBytes || !responsesProxyBearerBytesValid(trimmed) {
		return 0, errors.New("invalid workload identity bearer credential")
	}
	return len(trimmed), nil
}

// responsesProxyUpstreamClient builds the upstream client for the selected auth
// mode; federated credentials forbid redirects.
func responsesProxyUpstreamClient(auth responsesProxyAuth) *http.Client {
	client := &http.Client{Timeout: 0}
	if auth != nil && !auth.FollowsRedirects() {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
}
