package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultOAuthIssuer         = "https://auth.openai.com"
	DefaultOAuthPort           = 1455
	FallbackOAuthPort          = 1457
	DefaultOAuthClient         = "app_EMoamEEZ73f0CkXaXp7hrann"
	RefreshTokenURLEnvOverride = "CODEX_REFRESH_TOKEN_URL_OVERRIDE"
	RevokeTokenURLEnvOverride  = "CODEX_REVOKE_TOKEN_URL_OVERRIDE"
	LoginClientIDEnvOverride   = "CODEX_APP_SERVER_LOGIN_CLIENT_ID"
	legacyLoginClientIDEnvVar  = "CODEX_APP_SERVER_LOGIN_CLIENT_ID"
	chatGPTAuthClaimNamespace  = "https://api.openai.com/auth"
)

type OAuthOptions struct {
	CodexHome  string
	Issuer     string
	ClientID   string
	HTTPClient *http.Client
	// FallbackHTTPClient, when set, retries the authorization-code exchange
	// through the system proxy after a connection failure before any redirect
	// (Rust #46562).
	FallbackHTTPClient *http.Client
	PollInterval       time.Duration
	PollTimeout        time.Duration
	DevicePrompt       io.Writer
	OpenBrowser        bool
	CallbackPort       uint16
	ForceState         string
	ForcedWorkspaces   []string
	// WorkspaceLogin, when set, binds the browser callback to the managed login
	// that requested a workspace switch (Rust #57514 WorkspaceLogin).
	WorkspaceLogin *WorkspaceLogin
	StoreOptions   *StoreOptions
}

type PKCECodes struct {
	CodeVerifier  string
	CodeChallenge string
}

type DeviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        time.Duration
}

type BrowserLoginServer struct {
	AuthURL string
	Port    uint16
	Done    <-chan error
	server  *http.Server
}

type oauthCallbackOutcome struct {
	successURL string
	err        error
}

type ExchangedTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// Issuer and ClientID record the OAuth authority that issued the refresh
	// token. They are persisted as oauth_provenance and are not part of the
	// token endpoint response.
	Issuer   string `json:"-"`
	ClientID string `json:"-"`
}

// RefreshReason is the caller-reported diagnostic Rust attaches to a ChatGPT
// token refresh request (Rust `login/src/oauth/client.rs::RefreshReason`,
// #53991). It is never evidence of token validity; it only tells AuthAPI why
// the refresh was requested.
type RefreshReason string

const (
	// RefreshReasonProactiveExpiry means the access token is inside the refresh
	// window (Rust `RefreshReason::ProactiveExpiry`).
	RefreshReasonProactiveExpiry RefreshReason = "proactive_expiry"
	// RefreshReasonScheduled means the periodic refresh interval elapsed
	// (Rust `RefreshReason::Scheduled`).
	RefreshReasonScheduled RefreshReason = "scheduled"
	// RefreshReasonUnauthorizedRecovery means a 401 triggered the recovery flow
	// (Rust `RefreshReason::UnauthorizedRecovery`).
	RefreshReasonUnauthorizedRecovery RefreshReason = "unauthorized_recovery"
	// RefreshReasonForced means an explicit refresh was requested
	// (Rust `RefreshReason::Forced`, the default of `refresh_token`).
	RefreshReasonForced RefreshReason = "forced"
)

// RefreshReasonValue reports the wire value Rust sends in the
// `refresh_reason` grant parameter. An unknown reason is omitted instead of
// being relayed.
func RefreshReasonValue(reason RefreshReason) string {
	switch reason {
	case RefreshReasonProactiveExpiry, RefreshReasonScheduled, RefreshReasonUnauthorizedRecovery, RefreshReasonForced:
		return string(reason)
	}
	return ""
}

type RefreshChatGPTTokenOptions struct {
	CodexHome    string
	Issuer       string
	ClientID     string
	HTTPClient   *http.Client
	RefreshToken string
	AuthSnapshot *AuthDotJSON
	StoreOptions *StoreOptions
	// Reason reports why the caller refreshes, mirroring Rust
	// `RefreshTokenGrant::refresh_reason` (#53991). The zero value is Rust's
	// `RefreshReason::Forced`, which is what the explicit `refresh_token`
	// entry points report; gateway refreshes never carry the field.
	Reason RefreshReason
}

func NormalizeOAuthOptions(opts *OAuthOptions) *OAuthOptions {
	if opts == nil {
		opts = &OAuthOptions{}
	}
	normalized := *opts
	if strings.TrimSpace(normalized.CodexHome) == "" {
		normalized.CodexHome = DefaultCodexHome()
	}
	normalized.Issuer = strings.TrimRight(strings.TrimSpace(firstNonEmptyAuth(normalized.Issuer, DefaultOAuthIssuer)), "/")
	normalized.ClientID = strings.TrimSpace(firstNonEmptyAuth(normalized.ClientID, oauthClientFromEnv()))
	if normalized.HTTPClient == nil {
		normalized.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if normalized.PollInterval <= 0 {
		normalized.PollInterval = 5 * time.Second
	}
	if normalized.PollTimeout <= 0 {
		normalized.PollTimeout = 15 * time.Minute
	}
	if normalized.CallbackPort == 0 {
		normalized.CallbackPort = DefaultOAuthPort
	}
	return &normalized
}

func NewPKCECodes() (*PKCECodes, error) {
	randomBytes := make([]byte, 64)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(randomBytes)
	sum := sha256.Sum256([]byte(verifier))
	return &PKCECodes{
		CodeVerifier:  verifier,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func BuildAuthorizeURL(opts *OAuthOptions, redirectURI string, pkce *PKCECodes, state string) (string, error) {
	opts = NormalizeOAuthOptions(opts)
	if pkce == nil {
		return "", errors.New("pkce codes are required")
	}
	base, err := url.Parse(opts.Issuer + "/oauth/authorize")
	if err != nil {
		return "", err
	}
	query := base.Query()
	query.Set("response_type", "code")
	query.Set("client_id", opts.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", "openid profile email offline_access api.connectors.read api.connectors.invoke")
	query.Set("code_challenge", pkce.CodeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("state", state)
	query.Set("originator", "codex_cli_rs")
	if workspaces := normalizedWorkspaceList(opts.ForcedWorkspaces); len(workspaces) > 0 {
		query.Set("allowed_workspace_id", strings.Join(workspaces, ","))
	}
	if login := opts.WorkspaceLogin; login != nil {
		query.Set("current_workspace_id", login.WorkspaceID)
		if login.Email != "" {
			query.Set("login_hint", login.Email)
		}
		if login.Connection != "" {
			query.Set("connection", login.Connection)
		}
	}
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func RequestDeviceCode(ctx context.Context, opts *OAuthOptions) (*DeviceCode, error) {
	opts = NormalizeOAuthOptions(opts)
	var response struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		UserCodeAlt  string `json:"usercode"`
		Interval     string `json:"interval"`
	}
	body := map[string]string{"client_id": opts.ClientID}
	if err := postJSON(ctx, opts.HTTPClient, opts.Issuer+"/api/accounts/deviceauth/usercode", body, &response); err != nil {
		return nil, err
	}
	userCode := firstNonEmptyAuth(response.UserCode, response.UserCodeAlt)
	if strings.TrimSpace(response.DeviceAuthID) == "" || strings.TrimSpace(userCode) == "" {
		return nil, errors.New("device code response omitted device_auth_id or user_code")
	}
	interval := opts.PollInterval
	if strings.TrimSpace(response.Interval) != "" {
		if parsed, err := time.ParseDuration(strings.TrimSpace(response.Interval) + "s"); err == nil && parsed > 0 {
			interval = parsed
		}
	}
	return &DeviceCode{
		VerificationURL: opts.Issuer + "/codex/device",
		UserCode:        userCode,
		DeviceAuthID:    response.DeviceAuthID,
		Interval:        interval,
	}, nil
}

func CompleteDeviceCodeLogin(ctx context.Context, opts *OAuthOptions, code *DeviceCode) error {
	opts = NormalizeOAuthOptions(opts)
	if code == nil {
		return errors.New("device code is required")
	}
	tokenResponse, err := pollForDeviceAuthorizationCode(ctx, opts, code)
	if err != nil {
		return err
	}
	tokens, err := ExchangeCodeForTokens(ctx, opts, opts.Issuer+"/deviceauth/callback", &PKCECodes{
		CodeVerifier:  tokenResponse.CodeVerifier,
		CodeChallenge: tokenResponse.CodeChallenge,
	}, tokenResponse.AuthorizationCode)
	if err != nil {
		return fmt.Errorf("device code exchange failed: %w", err)
	}
	if err := EnsureWorkspaceAllowed(opts.ForcedWorkspaces, tokens.IDToken); err != nil {
		return err
	}
	return PersistChatGPTTokensWithOptions(opts.CodexHome, tokens, opts.StoreOptions)
}

func RunDeviceCodeLogin(ctx context.Context, opts *OAuthOptions) error {
	opts = NormalizeOAuthOptions(opts)
	code, err := RequestDeviceCode(ctx, opts)
	if err != nil {
		return err
	}
	if opts.DevicePrompt != nil {
		fmt.Fprintf(opts.DevicePrompt, "\nFollow these steps to sign in with ChatGPT using device code authorization:\n\n")
		fmt.Fprintf(opts.DevicePrompt, "1. Open this link in your browser and sign in to your account\n   %s\n\n", code.VerificationURL)
		fmt.Fprintf(opts.DevicePrompt, "2. Enter this one-time code\n   %s\n\n", code.UserCode)
		fmt.Fprintln(opts.DevicePrompt, "Device codes are a common phishing target. Never share this code.")
	}
	return CompleteDeviceCodeLogin(ctx, opts, code)
}

func StartBrowserLogin(ctx context.Context, opts *OAuthOptions) (*BrowserLoginServer, error) {
	opts = NormalizeOAuthOptions(opts)
	pkce, err := NewPKCECodes()
	if err != nil {
		return nil, err
	}
	state := strings.TrimSpace(opts.ForceState)
	if state == "" {
		state, err = randomURLToken(32)
		if err != nil {
			return nil, err
		}
	}
	listener, err := bindOAuthListener(opts.CallbackPort)
	if err != nil {
		return nil, err
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	authURL, err := BuildAuthorizeURL(opts, redirectURI, pkce, state)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	result := make(chan error, 1)
	done := make(chan error, 1)
	var resultOnce sync.Once
	sendResult := func(err error) {
		resultOnce.Do(func() {
			result <- err
		})
	}
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, request *http.Request) {
		outcome := handleOAuthCallback(request, opts, redirectURI, pkce, state)
		if outcome.err != nil {
			writeOAuthErrorPage(w, outcome.err)
			sendResult(outcome.err)
			return
		}
		if outcome.successURL != "" {
			http.Redirect(w, request, outcome.successURL, http.StatusFound)
			return
		}
		http.Redirect(w, request, fmt.Sprintf("http://localhost:%d/success", port), http.StatusFound)
	})
	mux.HandleFunc("/success", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, oauthSuccessHTML())
		sendResult(nil)
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, "Login cancelled")
		sendResult(errors.New("Login cancelled"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		http.NotFound(w, request)
	})
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			sendResult(err)
		}
	}()
	go func() {
		var err error
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case err = <-result:
		}
		_ = server.Shutdown(context.Background())
		done <- err
	}()
	if opts.OpenBrowser {
		_ = OpenBrowser(authURL)
	}
	return &BrowserLoginServer{AuthURL: authURL, Port: port, Done: done, server: server}, nil
}

func (s *BrowserLoginServer) Cancel(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	if s.Port != 0 {
		_ = sendOAuthCancelRequest(s.Port)
	}
	return s.server.Shutdown(ctx)
}

func bindOAuthListener(port uint16) (net.Listener, error) {
	if port == 0 {
		port = DefaultOAuthPort
	}
	preferred := fmt.Sprintf("127.0.0.1:%d", port)
	fallback := fmt.Sprintf("127.0.0.1:%d", FallbackOAuthPort)
	bindAddress := preferred
	cancelAttempted := false
	usingFallback := false
	for attempts := 0; ; attempts++ {
		listener, err := net.Listen("tcp", bindAddress)
		if err == nil {
			return listener, nil
		}
		if !isAddrInUse(err) {
			return nil, err
		}
		if !cancelAttempted && !usingFallback {
			cancelAttempted = true
			_ = sendOAuthCancelRequest(port)
		}
		if attempts >= 9 {
			if port == DefaultOAuthPort && !usingFallback {
				bindAddress = fallback
				usingFallback = true
				attempts = -1
				continue
			}
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return strings.Contains(strings.ToLower(opErr.Error()), "address already in use") ||
			strings.Contains(strings.ToLower(opErr.Error()), "only one usage of each socket address")
	}
	return false
}

func sendOAuthCancelRequest(port uint16) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = fmt.Fprintf(conn, "GET /cancel HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nConnection: close\r\n\r\n", port)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, 512))
	return nil
}

func ExchangeCodeForTokens(ctx context.Context, opts *OAuthOptions, redirectURI string, pkce *PKCECodes, code string) (*ExchangedTokens, error) {
	opts = NormalizeOAuthOptions(opts)
	if pkce == nil {
		return nil, errors.New("pkce codes are required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", opts.ClientID)
	form.Set("code_verifier", pkce.CodeVerifier)
	response, err := exchangeOAuthCode(ctx, opts, form)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("token endpoint returned status %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var tokens ExchangedTokens
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" {
		return nil, errors.New("token response omitted access_token or refresh_token")
	}
	tokens.Issuer = opts.Issuer
	tokens.ClientID = opts.ClientID
	return &tokens, nil
}

// oauthCodeConnectTimeout bounds the authorization-code exchange attempt so the
// system-proxy retry still fits the login flow's budget (Rust #46562).
const oauthCodeConnectTimeout = 10 * time.Second

// exchangeOAuthCode posts the authorization-code exchange. With a fallback
// client it retries through the system proxy only after a connection failure
// that happened before any redirect; HTTP errors, POST failures after the
// request was sent, and successful redirect chains are final.
func exchangeOAuthCode(ctx context.Context, opts *OAuthOptions, form url.Values) (*http.Response, error) {
	newRequest := func(requestCtx context.Context) (*http.Request, error) {
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, opts.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return request, nil
	}
	if opts.FallbackHTTPClient == nil {
		request, err := newRequest(ctx)
		if err != nil {
			return nil, err
		}
		return opts.HTTPClient.Do(request)
	}
	attemptCtx, cancel := context.WithTimeout(ctx, oauthCodeConnectTimeout)
	request, err := newRequest(attemptCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	redirected := false
	response, err := oauthRedirectTrackingClient(opts.HTTPClient, &redirected).Do(request)
	cancel()
	if err == nil || redirected || ctx.Err() != nil || !isOAuthConnectFailure(err) {
		return response, err
	}
	retryRequest, err := newRequest(ctx)
	if err != nil {
		return nil, err
	}
	return opts.FallbackHTTPClient.Do(retryRequest)
}

// oauthRedirectTrackingClient records whether the client followed a redirect
// while preserving the original redirect policy.
func oauthRedirectTrackingClient(client *http.Client, observed *bool) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	cloned := *client
	original := client.CheckRedirect
	cloned.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if observed != nil {
			*observed = true
		}
		if original != nil {
			return original(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &cloned
}

// isOAuthConnectFailure reports a connection-phase failure (dial or DNS), the
// only condition that retries the code exchange through the system proxy.
func isOAuthConnectFailure(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return true
		}
		return false
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

func PersistChatGPTTokens(codexHome string, tokens *ExchangedTokens) error {
	return PersistChatGPTTokensWithOptions(codexHome, tokens, nil)
}

func PersistChatGPTTokensWithOptions(codexHome string, tokens *ExchangedTokens, storeOptions *StoreOptions) error {
	if tokens == nil {
		return errors.New("tokens are required")
	}
	auth := authDotJSONFromExchangedTokens(tokens)
	return NewStoreWithOptions(codexHome, storeOptions).Save(auth)
}

// authDotJSONFromExchangedTokens renders a completed OAuth exchange the way a
// fresh ChatGPT login stores it.
func authDotJSONFromExchangedTokens(tokens *ExchangedTokens) AuthDotJSON {
	auth := AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":      tokens.IDToken,
			"access_token":  tokens.AccessToken,
			"refresh_token": tokens.RefreshToken,
		},
		LastRefresh: time.Now().UTC().Format(time.RFC3339),
	}
	if issuer := strings.TrimSpace(tokens.Issuer); issuer != "" {
		clientID := strings.TrimSpace(tokens.ClientID)
		auth.Tokens["oauth_provenance"] = map[string]any{
			"issuer": issuer, "client_id": clientID,
		}
		auth.Tokens["login_owner_id"] = newOAuthLoginOwnerID()
	}
	if accountID := ChatGPTAccountIDFromJWT(tokens.IDToken); accountID != "" {
		auth.Tokens["account_id"] = accountID
	}
	return auth
}

func RefreshChatGPTTokens(ctx context.Context, opts *RefreshChatGPTTokenOptions) (*AuthDotJSON, error) {
	if opts == nil {
		opts = &RefreshChatGPTTokenOptions{}
	}
	codexHome := strings.TrimSpace(opts.CodexHome)
	if codexHome == "" {
		codexHome = DefaultCodexHome()
	}
	// Ordinary refresh holds the same gate as AccountOAuthGrantLease, so an
	// identity-provider exchange and a refresh cannot rotate the grant
	// concurrently (Rust #55546 refresh_lock).
	gate := accountRefreshGateFor(codexHome)
	if err := gate.acquire(ctx); err != nil {
		return nil, errors.New("Account refresh coordination is unavailable.")
	}
	defer gate.release()
	attempted := refreshAttemptSnapshot(codexHome, opts.AuthSnapshot, opts.StoreOptions)
	provenance := oauthProvenanceFromTokens(nilSafeTokens(attempted))
	normalized := NormalizeOAuthOptions(&OAuthOptions{
		CodexHome:  opts.CodexHome,
		Issuer:     firstNonEmptyAuth(opts.Issuer, provenance.Issuer),
		ClientID:   firstNonEmptyAuth(opts.ClientID, provenance.ClientID),
		HTTPClient: opts.HTTPClient,
	})
	if failed := RefreshFailureForAuth(normalized.CodexHome, attempted); failed != nil {
		return nil, failed
	}
	refreshToken := strings.TrimSpace(opts.RefreshToken)
	if refreshToken == "" {
		refreshToken = stringFromAny(nilSafeTokens(attempted), "refresh_token")
	}
	if refreshToken == "" {
		return nil, errors.New("refresh token is required")
	}
	body := map[string]string{
		"client_id":     normalized.ClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	// Rust #53991: report why the refresh was requested. The ChatGPT entry
	// points default to `forced`; gateway refreshes go through their own
	// request and never carry the field.
	reason := opts.Reason
	if reason == "" {
		reason = RefreshReasonForced
	}
	if value := RefreshReasonValue(reason); value != "" {
		body["refresh_reason"] = value
	}
	// Rust #58355: this generation is unsettled from the start of the exchange
	// until the refreshed credentials are persisted. A cancelled exchange keeps
	// the record, while a completed failure restores the record it replaced so
	// it cannot settle an earlier cancelled attempt.
	previousUnsettled := beginChatGPTRefresh(normalized.CodexHome, nilSafeTokens(attempted))
	refreshed, refreshErr := func() (*AuthDotJSON, error) {
		var out ExchangedTokens
		if err := postJSON(ctx, normalized.HTTPClient, refreshTokenURL(normalized.Issuer), body, &out); err != nil {
			var statusErr *httpStatusError
			if errors.As(err, &statusErr) {
				// RFC 6749 reports an unusable refresh token as invalid_grant
				// without preserving the legacy expired/reused/revoked subtype.
				// Keep it terminal with the generic reason (#39637).
				code := extractRefreshTokenErrorCode(statusErr.Body)
				isInvalidGrantBadRequest := statusErr.StatusCode == http.StatusBadRequest &&
					strings.EqualFold(code, "invalid_grant")
				failed := classifyRefreshTokenFailure(statusErr.Body)
				if statusErr.StatusCode == http.StatusUnauthorized ||
					failed.Reason != RefreshTokenFailedOther ||
					isInvalidGrantBadRequest {
					RecordPermanentRefreshFailureIfUnchangedWithOptions(normalized.CodexHome, attempted, failed, opts.StoreOptions)
					return nil, failed
				}
			}
			return nil, err
		}
		if strings.TrimSpace(out.AccessToken) == "" {
			return nil, errors.New("refresh response omitted access_token")
		}
		if strings.TrimSpace(out.RefreshToken) == "" {
			out.RefreshToken = refreshToken
		}
		if err := persistRefreshedChatGPTTokens(normalized.CodexHome, attempted, &out, opts.StoreOptions); err != nil {
			return nil, err
		}
		return NewStoreWithOptions(normalized.CodexHome, opts.StoreOptions).Load()
	}()
	switch {
	case refreshErr == nil:
		settleChatGPTRefresh(normalized.CodexHome)
	case ctx.Err() != nil:
		// The exchange was interrupted after it started, so the stored token may
		// already have been rotated even if this call never saw the response.
	default:
		restoreChatGPTRefresh(normalized.CodexHome, previousUnsettled)
	}
	return refreshed, refreshErr
}

func refreshAttemptSnapshot(codexHome string, provided *AuthDotJSON, storeOptions *StoreOptions) *AuthDotJSON {
	if provided != nil {
		return cloneAuthDotJSON(provided)
	}
	loaded, err := NewStoreWithOptions(codexHome, storeOptions).Load()
	if err != nil {
		return nil
	}
	return loaded
}

func refreshTokenURL(issuer string) string {
	if value := strings.TrimSpace(os.Getenv(RefreshTokenURLEnvOverride)); value != "" {
		return value
	}
	return strings.TrimRight(strings.TrimSpace(issuer), "/") + "/oauth/token"
}

func PersistRefreshedChatGPTTokens(codexHome string, tokens *ExchangedTokens) error {
	return PersistRefreshedChatGPTTokensWithOptions(codexHome, tokens, nil)
}

func PersistRefreshedChatGPTTokensWithOptions(codexHome string, tokens *ExchangedTokens, storeOptions *StoreOptions) error {
	if tokens == nil {
		return errors.New("tokens are required")
	}
	expected, err := NewStoreWithOptions(codexHome, storeOptions).Load()
	if err != nil {
		return err
	}
	return persistRefreshedChatGPTTokens(codexHome, expected, tokens, storeOptions)
}

var errAccountCredentialsChangedDuringRefresh = errors.New("Account credentials changed during refresh.")

// persistRefreshedChatGPTTokens writes a refresh response only when the stored
// credential still belongs to the login that requested it. The read, login
// check and write are one process-wide commit (Rust #55646 persist_tokens).
func persistRefreshedChatGPTTokens(codexHome string, expected *AuthDotJSON, tokens *ExchangedTokens, storeOptions *StoreOptions) error {
	if tokens == nil {
		return errors.New("tokens are required")
	}
	if expected == nil || expected.Tokens == nil {
		return errors.New("Token data is not available.")
	}
	store := NewStoreWithOptions(codexHome, storeOptions)
	guard, err := store.lockForWrite()
	if err != nil {
		return err
	}
	defer guard.release()
	commit, err := store.lockCommit()
	if err != nil {
		return err
	}
	defer commit.release()
	current, err := store.loadLocked()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("Token data is not available.")
	}
	if current.Mode() != "chatgpt" || !sameLoginForPersistence(expected.Tokens, current.Tokens) {
		return errAccountCredentialsChangedDuringRefresh
	}
	next := *current
	next.AuthMode = "chatgpt"
	next.Tokens = cloneAnyMap(current.Tokens)
	if next.Tokens == nil {
		next.Tokens = map[string]any{}
	}
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	// Attach provenance only when this response rotated the refresh token or the
	// stored grant is still the one the request used. A concurrent writer that
	// replaced the refresh token has unknown authority (Rust #55546).
	rotated := refreshToken != "" && refreshToken != tokenRefreshToken(expected.Tokens)
	provenance := oauthProvenanceFromTokens(expected.Tokens)
	endpointMatches := provenance.Issuer != "" &&
		refreshTokenURL(provenance.Issuer) == strings.TrimRight(provenance.Issuer, "/")+"/oauth/token"
	if endpointMatches && (rotated || tokenRefreshToken(next.Tokens) == tokenRefreshToken(expected.Tokens)) {
		next.Tokens["oauth_provenance"] = map[string]any{
			"issuer": provenance.Issuer, "client_id": provenance.ClientID,
		}
	} else {
		delete(next.Tokens, "oauth_provenance")
	}
	if idToken := strings.TrimSpace(tokens.IDToken); idToken != "" {
		next.Tokens["id_token"] = idToken
		// Rust #57514 persist_tokens keeps the selected workspace: a refresh may
		// return an ID token for the login's default workspace while
		// tokens.account_id still names the workspace the user switched to.
		if existing := strings.TrimSpace(stringFromAny(next.Tokens, "account_id")); existing == "" {
			if accountID := ChatGPTAccountIDFromJWT(idToken); accountID != "" {
				next.Tokens["account_id"] = accountID
			}
		}
	}
	next.Tokens["access_token"] = strings.TrimSpace(tokens.AccessToken)
	next.Tokens["refresh_token"] = refreshToken
	next.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	if auto, ok := store.backend().(*autoAuthStorage); ok {
		keyringAuth, keyringErr := auto.keyring.Load()
		if keyringErr != nil {
			// Rust #58343: the keyring generation is unknown, so the rotated
			// fallback tokens belong in the file store; never overwrite the
			// unread keyring entry, even if it recovered before this commit.
			return store.saveRefreshedFallbackLocked(next, keyringErr)
		}
		if keyringAuth != nil && !AuthsEqualForRefresh(keyringAuth, expected) {
			return errAccountCredentialsChangedDuringRefresh
		}
	}
	return store.saveLocked(next)
}

func ChatGPTAccountIDFromJWT(jwt string) string {
	return ChatGPTClaimsFromJWT(jwt).AccountID
}

func EnsureWorkspaceAllowed(expected []string, idToken string) error {
	allowed := normalizedWorkspaceSet(expected)
	if len(allowed) == 0 {
		return nil
	}
	accountID := strings.TrimSpace(ChatGPTAccountIDFromJWT(idToken))
	if accountID == "" {
		return errors.New("Login is restricted to a specific workspace, but the token did not include an chatgpt_account_id claim.")
	}
	return EnsureWorkspaceAccountAllowed(expected, accountID)
}

func EnsureWorkspaceAccountAllowed(expected []string, accountID string) error {
	allowed := normalizedWorkspaceSet(expected)
	if len(allowed) == 0 {
		return nil
	}
	accountID = strings.TrimSpace(accountID)
	if accountID != "" && allowed[accountID] {
		return nil
	}
	return fmt.Errorf("Login is restricted to workspace id(s) %s.", strings.Join(normalizedWorkspaceList(expected), ", "))
}

type ChatGPTJWTClaims struct {
	AccountID     string
	AccountUserID string
	Email         string
	PlanType      string
	UserID        string
	FedRAMP       bool
}

func ChatGPTClaimsFromJWT(jwt string) *ChatGPTJWTClaims {
	claims := &ChatGPTJWTClaims{}
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return claims
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return claims
	}
	claimMaps := []map[string]any{raw}
	if nested, ok := raw[chatGPTAuthClaimNamespace].(map[string]any); ok {
		claimMaps = append([]map[string]any{nested}, claimMaps...)
	}
	for _, key := range []string{"chatgpt_account_id", "account_id"} {
		if value := firstStringClaim(claimMaps, key); value != "" {
			claims.AccountID = value
			break
		}
	}
	claims.Email = firstStringClaim(claimMaps, "email")
	for _, key := range []string{"plan_type", "chatgpt_plan_type"} {
		if value := firstStringClaim(claimMaps, key); value != "" {
			claims.PlanType = value
			break
		}
	}
	for _, key := range []string{"chatgpt_user_id", "user_id"} {
		if value := firstStringClaim(claimMaps, key); value != "" {
			claims.UserID = value
			break
		}
	}
	if value := firstStringClaim(claimMaps, "chatgpt_account_user_id"); value != "" {
		claims.AccountUserID = value
	}
	for _, key := range []string{"is_fedramp_account", "chatgpt_account_is_fedramp"} {
		if value, ok := firstBoolClaim(claimMaps, key); ok {
			claims.FedRAMP = value
			break
		}
	}
	return claims
}

func firstStringClaim(claimMaps []map[string]any, key string) string {
	for _, claims := range claimMaps {
		if value, ok := claims[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstBoolClaim(claimMaps []map[string]any, key string) (bool, bool) {
	for _, claims := range claimMaps {
		if value, ok := claims[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func normalizedWorkspaceList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizedWorkspaceSet(values []string) map[string]bool {
	list := normalizedWorkspaceList(values)
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]bool, len(list))
	for _, value := range list {
		out[value] = true
	}
	return out
}

func nilSafeTokens(snapshot *AuthDotJSON) map[string]any {
	if snapshot == nil {
		return nil
	}
	return snapshot.Tokens
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func OpenBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

func pollForDeviceAuthorizationCode(ctx context.Context, opts *OAuthOptions, code *DeviceCode) (*deviceTokenResponse, error) {
	deadline := time.NewTimer(opts.PollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(code.Interval)
	defer ticker.Stop()
	for {
		response, err := requestDeviceAuthorizationCode(ctx, opts, code)
		if err == nil {
			return response, nil
		}
		if !errors.Is(err, errDevicePending) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("device auth timed out")
		case <-ticker.C:
		}
	}
}

var errDevicePending = errors.New("device authorization is pending")

func requestDeviceAuthorizationCode(ctx context.Context, opts *OAuthOptions, code *DeviceCode) (*deviceTokenResponse, error) {
	body := map[string]string{
		"device_auth_id": code.DeviceAuthID,
		"user_code":      code.UserCode,
	}
	var out deviceTokenResponse
	err := postJSON(ctx, opts.HTTPClient, opts.Issuer+"/api/accounts/deviceauth/token", body, &out)
	if err == nil {
		return &out, nil
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusForbidden || statusErr.StatusCode == http.StatusNotFound) {
		return nil, errDevicePending
	}
	return nil, err
}

func handleOAuthCallback(request *http.Request, opts *OAuthOptions, redirectURI string, pkce *PKCECodes, state string) *oauthCallbackOutcome {
	query := request.URL.Query()
	if query.Get("state") != state {
		return &oauthCallbackOutcome{err: errors.New("state mismatch")}
	}
	if query.Get("error") != "" {
		return &oauthCallbackOutcome{err: oauthCallbackError(query.Get("error"), query.Get("error_description"))}
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		return &oauthCallbackOutcome{err: errors.New("missing authorization code")}
	}
	tokens, err := ExchangeCodeForTokens(request.Context(), opts, redirectURI, pkce, code)
	if err != nil {
		return &oauthCallbackOutcome{err: err}
	}
	if err := EnsureWorkspaceAllowed(opts.ForcedWorkspaces, tokens.IDToken); err != nil {
		return &oauthCallbackOutcome{err: err}
	}
	if opts.WorkspaceLogin != nil {
		if err := opts.WorkspaceLogin.Save(tokens); err != nil {
			return &oauthCallbackOutcome{err: fmt.Errorf("sign-in completed but credentials could not be saved locally: %w", err)}
		}
		return &oauthCallbackOutcome{successURL: composeOAuthSuccessURL(request, tokens)}
	}
	if err := PersistChatGPTTokensWithOptions(opts.CodexHome, tokens, opts.StoreOptions); err != nil {
		return &oauthCallbackOutcome{err: fmt.Errorf("sign-in completed but credentials could not be saved locally: %w", err)}
	}
	return &oauthCallbackOutcome{successURL: composeOAuthSuccessURL(request, tokens)}
}

func oauthCallbackError(code string, description string) error {
	code = strings.TrimSpace(code)
	description = strings.TrimSpace(description)
	if strings.EqualFold(code, "access_denied") && strings.Contains(strings.ToLower(description), "missing_codex_entitlement") {
		return errors.New("Codex is not enabled for your workspace. Contact your workspace administrator to request access to Codex.")
	}
	if description != "" {
		return fmt.Errorf("Sign-in failed: %s", description)
	}
	if code != "" {
		return fmt.Errorf("Sign-in failed: %s", code)
	}
	return errors.New("Sign-in failed")
}

func composeOAuthSuccessURL(request *http.Request, tokens *ExchangedTokens) string {
	if request == nil || tokens == nil {
		return ""
	}
	port := request.Host
	if strings.Contains(port, ":") {
		_, parsedPort, err := net.SplitHostPort(port)
		if err == nil {
			port = parsedPort
		}
	}
	if port == "" {
		return ""
	}
	query := url.Values{}
	query.Set("id_token", tokens.IDToken)
	claims := ChatGPTClaimsFromJWT(tokens.AccessToken)
	if strings.TrimSpace(claims.PlanType) != "" {
		query.Set("plan_type", strings.TrimSpace(claims.PlanType))
	}
	return "http://localhost:" + port + "/success?" + query.Encode()
}

func writeOAuthErrorPage(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	message := "Sign-in failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}
	_, _ = io.WriteString(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>Codex Sign-in Failed</title></head><body><h1>Codex Sign-in Failed</h1><p>"+htmlEscape(message)+"</p></body></html>")
}

func oauthSuccessHTML() string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Codex Sign-in Complete</title></head><body><h1>Successfully logged in</h1><p>You can close this window and return to Codex.</p></body></html>"
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func postJSON(ctx context.Context, client *http.Client, target string, body any, response any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(data))}
	}
	if response == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(response)
}

type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e.Body == "" {
		return "request failed with status " + e.Status
	}
	return "request failed with status " + e.Status + ": " + e.Body
}

func randomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func oauthClientFromEnv() string {
	if value := strings.TrimSpace(os.Getenv(LoginClientIDEnvOverride)); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv(legacyLoginClientIDEnvVar)); value != "" {
		return value
	}
	return DefaultOAuthClient
}

func firstNonEmptyAuth(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// OAuthProvenance records the authority that issued an account refresh token.
// It is optional for legacy auth records; token claims never infer it.
type OAuthProvenance struct {
	Issuer   string
	ClientID string
}

func oauthProvenanceFromTokens(tokens map[string]any) OAuthProvenance {
	raw, _ := tokens["oauth_provenance"].(map[string]any)
	if raw == nil {
		if encoded, ok := tokens["oauth_provenance"].(map[string]string); ok {
			return OAuthProvenance{Issuer: strings.TrimSpace(encoded["issuer"]), ClientID: strings.TrimSpace(encoded["client_id"])}
		}
		return OAuthProvenance{}
	}
	issuer, _ := raw["issuer"].(string)
	clientID, _ := raw["client_id"].(string)
	return OAuthProvenance{Issuer: strings.TrimSpace(issuer), ClientID: strings.TrimSpace(clientID)}
}

func newOAuthLoginOwnerID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data[:])
}
