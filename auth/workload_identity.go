package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// WorkloadIdentityExchange mirrors Rust codex-workload-identity (936f5eb3ee):
// it exchanges a file-backed JWT assertion and federation rule ID for
// short-lived ChatGPT credentials, caching valid access tokens, refreshing
// them before expiry or after rejection, and coalescing concurrent exchanges.
type WorkloadIdentityExchange struct {
	client            *http.Client
	completedAttempts uint64
	config            WorkloadIdentityConfig
	mu                sync.Mutex
	state             workloadIdentityCacheState
	tokenURL          *url.URL
}

type WorkloadIdentityConfig struct {
	AssertionFile           string
	FederationRuleID        string
	WorkloadIdentityContext string
}

type workloadIdentityCacheState struct {
	cached           *workloadCachedToken
	lastAttemptError error
	tokenGeneration  uint64
}

type workloadCachedToken struct {
	expiresAt time.Time
	refreshAt time.Time
	token     WorkloadIdentityToken
}

type WorkloadIdentityToken struct {
	AccessToken          string
	ChatGPTAccountID     string
	ChatGPTAccountUserID string
	ChatGPTPLanType      *string
	ExpiresIn            uint64
	Scope                string
	UserID               string
	Version              uint64
}

type workloadIdentityError string

// workloadAssertionFileError wraps a failure to read the assertion file so
// retry classification can inspect the underlying I/O error (Rust
// WorkloadIdentityError::AssertionFile).
type workloadAssertionFileError struct {
	path string
	err  error
}

func (e *workloadAssertionFileError) Error() string {
	return fmt.Sprintf("could not read workload identity assertion file %s: %v", e.path, e.err)
}

func (e *workloadAssertionFileError) Unwrap() error { return e.err }

const (
	workloadErrInvalidFederationRuleID   workloadIdentityError = "the workload identity federation rule ID must not be empty"
	workloadErrAssertionFileMustAbsolute workloadIdentityError = "the workload identity assertion file path must be absolute"
	workloadErrInvalidAssertion          workloadIdentityError = "the workload identity assertion is invalid"
	workloadErrAssertionTooLarge         workloadIdentityError = "the workload identity assertion exceeds 16 KiB"
	workloadErrInvalidTokenURL           workloadIdentityError = "the workload identity token URL must use HTTPS or loopback HTTP"
	workloadErrExchangeUnavailable       workloadIdentityError = "workload identity token exchange is unavailable"
	workloadErrInvalidExchangeResponse   workloadIdentityError = "the workload identity exchange returned an invalid response"
)

func (e workloadIdentityError) Error() string { return string(e) }

const (
	workloadAccessTokenType   = "urn:ietf:params:oauth:token-type:access_token"
	workloadJWTBearerGrant    = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	workloadMaxTokenLifetime  = time.Hour
	workloadMaxResponseBytes  = 1 << 20
	workloadRequestTimeout    = 30 * time.Second
	workloadTransientRetry    = 30 * time.Second
	workloadMaxAssertionBytes = 16 * 1024
)

// NewWorkloadIdentityConfig validates the federation rule ID and assertion
// path (Rust WorkloadIdentityConfig::new). workloadIdentityContext is the
// optional OPENAI_WORKLOAD_IDENTITY_CONTEXT value forwarded unchanged to the
// token exchange (Rust #38767).
func NewWorkloadIdentityConfig(federationRuleID string, assertionFile string, workloadIdentityContext string) (WorkloadIdentityConfig, error) {
	federationRuleID = strings.TrimSpace(federationRuleID)
	if federationRuleID == "" {
		return WorkloadIdentityConfig{}, workloadErrInvalidFederationRuleID
	}
	if !filepathIsAbsolute(assertionFile) {
		return WorkloadIdentityConfig{}, workloadErrAssertionFileMustAbsolute
	}
	return WorkloadIdentityConfig{
		AssertionFile:           assertionFile,
		FederationRuleID:        federationRuleID,
		WorkloadIdentityContext: workloadIdentityContext,
	}, nil
}

// NewWorkloadIdentityExchange validates the token URL and builds an exchange
// (Rust WorkloadIdentityExchange::new). HTTP is accepted only for loopback.
func NewWorkloadIdentityExchange(config WorkloadIdentityConfig, tokenURL string, client *http.Client) (*WorkloadIdentityExchange, error) {
	parsed, err := url.Parse(strings.TrimSpace(tokenURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, workloadErrInvalidTokenURL
	}
	if err := validateWorkloadTokenURL(parsed); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &WorkloadIdentityExchange{
		client:   client,
		config:   config,
		tokenURL: parsed,
	}, nil
}

func validateWorkloadTokenURL(parsed *url.URL) error {
	if parsed.Host == "" {
		return workloadErrInvalidTokenURL
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return workloadErrInvalidTokenURL
	}
	host := parsed.Hostname()
	isLoopback := strings.EqualFold(host, "localhost")
	if !isLoopback {
		if ip := net.ParseIP(host); ip != nil {
			isLoopback = ip.IsLoopback()
		}
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopback) {
		return workloadErrInvalidTokenURL
	}
	return nil
}

func filepathIsAbsolute(path string) bool {
	return strings.TrimSpace(path) != "" && (strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") || len(path) > 1 && path[1] == ':')
}

// readWorkloadAssertion reopens the assertion file for each exchange so its
// owner can rotate the credential (Rust read_assertion).
func readWorkloadAssertion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", &workloadAssertionFileError{path: path, err: err}
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, workloadMaxAssertionBytes+1))
	if err != nil {
		return "", &workloadAssertionFileError{path: path, err: err}
	}
	if len(bytes) > workloadMaxAssertionBytes {
		return "", workloadErrAssertionTooLarge
	}
	assertion := strings.TrimSpace(string(bytes))
	if assertion == "" || strings.ContainsRune(assertion, 0) {
		return "", workloadErrInvalidAssertion
	}
	return assertion, nil
}

// Resolve returns a cached token when possible, otherwise performs one shared
// exchange (Rust WorkloadIdentityExchange::resolve).
func (e *WorkloadIdentityExchange) Resolve(ctx context.Context) (WorkloadIdentityToken, error) {
	if e == nil {
		return WorkloadIdentityToken{}, workloadErrExchangeUnavailable
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	observedAttempts := e.completedAttempts
	now := time.Now()
	if cached := e.state.cached; cached != nil {
		if token, ok := cached.tokenAt(now); ok && now.Before(cached.refreshAt) {
			return token, nil
		}
	}
	if e.completedAttempts != observedAttempts {
		if e.state.lastAttemptError != nil {
			return WorkloadIdentityToken{}, e.state.lastAttemptError
		}
		if cached := e.state.cached; cached != nil {
			if token, ok := cached.tokenAt(now); ok {
				return token, nil
			}
		}
	}
	validFrom := time.Now()
	result, err := e.exchange(ctx)
	if err == nil {
		e.state.store(result, validFrom)
		if cached := e.state.cached; cached != nil {
			result = cached.token
		}
	} else if workloadErrorIsTransient(err) && e.state.cached != nil && now.Before(e.state.cached.expiresAt) {
		if e.state.cached.refreshAt.After(now) {
			e.state.cached.refreshAt = now.Add(workloadTransientRetry)
		}
		if token, ok := e.state.cached.tokenAt(now); ok {
			result = token
			err = nil
		}
	}
	e.completedAttempts++
	e.state.lastAttemptError = err
	return result, err
}

// Refresh exchanges after a downstream service rejects observedTokenVersion
// (Rust WorkloadIdentityExchange::refresh).
func (e *WorkloadIdentityExchange) Refresh(ctx context.Context, observedTokenVersion uint64) (WorkloadIdentityToken, error) {
	if e == nil {
		return WorkloadIdentityToken{}, workloadErrExchangeUnavailable
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.tokenGeneration != observedTokenVersion {
		if cached := e.state.cached; cached != nil {
			if token, ok := cached.tokenAt(time.Now()); ok {
				return token, nil
			}
		}
	}
	e.state.cached = nil
	validFrom := time.Now()
	result, err := e.exchange(ctx)
	if err == nil {
		e.state.store(result, validFrom)
		if cached := e.state.cached; cached != nil {
			result = cached.token
		}
	}
	e.completedAttempts++
	e.state.lastAttemptError = err
	return result, err
}

// InvalidateIfCurrent drops the cached token only when it is still the token
// the caller rejected, so a newer concurrent exchange is never discarded
// (Rust WorkloadIdentityExchange::invalidate_if_current).
func (e *WorkloadIdentityExchange) InvalidateIfCurrent(observedTokenVersion uint64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.tokenGeneration == observedTokenVersion {
		e.state.cached = nil
		e.state.lastAttemptError = nil
	}
}

func (e *WorkloadIdentityExchange) exchange(ctx context.Context) (WorkloadIdentityToken, error) {
	assertion, err := readWorkloadAssertion(e.config.AssertionFile)
	if err != nil {
		return WorkloadIdentityToken{}, err
	}
	form := url.Values{}
	form.Set("grant_type", workloadJWTBearerGrant)
	form.Set("assertion", assertion)
	form.Set("federation_rule_id", e.config.FederationRuleID)
	if e.config.WorkloadIdentityContext != "" {
		form.Set("workload_identity_context", e.config.WorkloadIdentityContext)
	}
	reqCtx, cancel := context.WithTimeout(ctx, workloadRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.tokenURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return WorkloadIdentityToken{}, workloadErrExchangeUnavailable
	}
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := e.client.Do(request)
	if err != nil {
		return WorkloadIdentityToken{}, workloadErrExchangeUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return WorkloadIdentityToken{}, &workloadExchangeRejected{Status: response.StatusCode}
	}
	if response.ContentLength > workloadMaxResponseBytes {
		return WorkloadIdentityToken{}, workloadErrInvalidExchangeResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, workloadMaxResponseBytes+1))
	if err != nil {
		return WorkloadIdentityToken{}, workloadErrExchangeUnavailable
	}
	if len(body) > workloadMaxResponseBytes {
		return WorkloadIdentityToken{}, workloadErrInvalidExchangeResponse
	}
	var wire workloadTokenExchangeResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return WorkloadIdentityToken{}, workloadErrInvalidExchangeResponse
	}
	return wire.intoToken()
}

type workloadExchangeRejected struct{ Status int }

func (e *workloadExchangeRejected) Error() string {
	return fmt.Sprintf("workload identity token exchange rejected with status %d", e.Status)
}

// IsTransient reports whether retrying the rejected exchange may succeed
// without changing configuration (Rust WorkloadIdentityError::is_transient).
func (e *workloadExchangeRejected) IsTransient() bool {
	return e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// workloadErrorIsTransient classifies exchange failures for retry handling
// (Rust WorkloadIdentityError::is_transient): transient failures are limited
// to unavailable exchanges, retryable HTTP statuses, and transient
// assertion-file I/O errors.
func workloadErrorIsTransient(err error) bool {
	if err == nil {
		return false
	}
	var rejected *workloadExchangeRejected
	if errors.As(err, &rejected) {
		return rejected.IsTransient()
	}
	var assertion *workloadAssertionFileError
	if errors.As(err, &assertion) {
		return workloadAssertionErrorIsTransient(assertion.err)
	}
	return err == workloadErrExchangeUnavailable
}

func workloadAssertionErrorIsTransient(err error) bool {
	return errors.Is(err, syscall.EINTR) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, syscall.EWOULDBLOCK) ||
		errors.Is(err, syscall.EAGAIN)
}

type workloadTokenExchangeResponse struct {
	AccessToken          string  `json:"access_token"`
	ChatGPTAccountID     string  `json:"chatgpt_account_id"`
	ChatGPTAccountUserID string  `json:"chatgpt_account_user_id"`
	ChatGPTPLanType      *string `json:"chatgpt_plan_type"`
	ExpiresIn            uint64  `json:"expires_in"`
	IssuedTokenType      string  `json:"issued_token_type"`
	Scope                string  `json:"scope"`
	TokenType            string  `json:"token_type"`
	UserID               string  `json:"user_id"`
}

func (w workloadTokenExchangeResponse) intoToken() (WorkloadIdentityToken, error) {
	lifetime := time.Duration(w.ExpiresIn) * time.Second
	if strings.TrimSpace(w.AccessToken) == "" ||
		w.IssuedTokenType != workloadAccessTokenType ||
		!strings.EqualFold(w.TokenType, "bearer") ||
		lifetime <= 0 ||
		lifetime > workloadMaxTokenLifetime ||
		strings.TrimSpace(w.Scope) == "" ||
		strings.TrimSpace(w.ChatGPTAccountID) == "" ||
		strings.TrimSpace(w.ChatGPTAccountUserID) == "" ||
		strings.TrimSpace(w.UserID) == "" ||
		(w.ChatGPTPLanType != nil && strings.TrimSpace(*w.ChatGPTPLanType) == "") {
		return WorkloadIdentityToken{}, workloadErrInvalidExchangeResponse
	}
	return WorkloadIdentityToken{
		AccessToken:          w.AccessToken,
		ChatGPTAccountID:     w.ChatGPTAccountID,
		ChatGPTAccountUserID: w.ChatGPTAccountUserID,
		ChatGPTPLanType:      cloneOptionalString(w.ChatGPTPLanType),
		ExpiresIn:            w.ExpiresIn,
		Scope:                w.Scope,
		UserID:               w.UserID,
	}, nil
}

func (s *workloadIdentityCacheState) store(token WorkloadIdentityToken, validFrom time.Time) {
	s.tokenGeneration++
	token.Version = s.tokenGeneration
	lifetime := time.Duration(token.ExpiresIn) * time.Second
	refreshMargin := 120 * time.Second
	if lifetime/2 < refreshMargin {
		refreshMargin = lifetime / 2
	}
	cached := &workloadCachedToken{
		expiresAt: validFrom.Add(lifetime),
		refreshAt: validFrom.Add(lifetime - refreshMargin),
		token:     token,
	}
	s.cached = cached
}

func (c *workloadCachedToken) tokenAt(now time.Time) (WorkloadIdentityToken, bool) {
	if c == nil || !now.Before(c.expiresAt) {
		return WorkloadIdentityToken{}, false
	}
	token := c.token
	remaining := c.expiresAt.Sub(now)
	token.ExpiresIn = uint64(remaining / time.Second)
	return token, true
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
