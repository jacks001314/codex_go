package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust daybreak.rs: read-only Daybreak eligibility used to choose the cyber
// refusal copy. The read is account-scoped and runs in the background; pending
// or failed reads use the neutral Limited copy.

const (
	daybreakVerifiedAccessTimeout = 3 * time.Second
	daybreakVerifiedAccessPath    = "/accounts/verified_access"
	daybreakResponseMaxBytes      = 1 << 20
)

// daybreakAuthStatusFunc reads the server's view of the local auth.
type daybreakAuthStatusFunc func(ctx context.Context) (appserver.AuthStatusResponse, error)

// daybreakNoticeCache caches one account's eligibility for a session (Rust
// daybreak::NoticeCache).
type daybreakNoticeCache struct {
	mu      sync.Mutex
	notice  codextui.DaybreakNotice
	loaded  bool
	started bool
}

// current returns the cached notice, or the neutral Limited copy while
// discovery is pending or failed.
func (c *daybreakNoticeCache) current() codextui.DaybreakNotice {
	if c == nil {
		return codextui.DaybreakNoticeLimited
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		return codextui.DaybreakNoticeLimited
	}
	return c.notice
}

func (c *daybreakNoticeCache) set(notice codextui.DaybreakNotice) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notice = notice
	c.loaded = true
}

// prefetchDaybreakNotice starts the bounded background eligibility read once
// (Rust prefetch_notice).
func prefetchDaybreakNotice(ctx context.Context, cache *daybreakNoticeCache, statusFn daybreakAuthStatusFunc, baseURL string, codexHome string) {
	if cache == nil || statusFn == nil {
		return
	}
	cache.mu.Lock()
	if cache.started {
		cache.mu.Unlock()
		return
	}
	cache.started = true
	cache.mu.Unlock()
	go func() {
		cache.set(loadDaybreakNotice(ctx, statusFn, baseURL, codexHome))
	}()
}

// daybreakNoticeForModel resolves the refusal copy for a model: non-openai
// providers never read Daybreak, and unknown models use the neutral copy (Rust
// on_cyber_policy_error + Notice::for_model).
func daybreakNoticeForModel(provider string, cache *daybreakNoticeCache, model string) codextui.DaybreakNotice {
	if !strings.EqualFold(strings.TrimSpace(provider), "openai") {
		return codextui.DaybreakNoticeLimited
	}
	return codextui.DaybreakNoticeForModel(cache.current(), model)
}

// loadDaybreakNotice reads the account's verified-access programs (Rust
// read_notice). Every failure path returns the neutral Limited copy.
func loadDaybreakNotice(ctx context.Context, statusFn daybreakAuthStatusFunc, baseURL string, codexHome string) codextui.DaybreakNotice {
	ctx, cancel := context.WithTimeout(ctx, daybreakVerifiedAccessTimeout)
	defer cancel()
	status, err := statusFn(ctx)
	if err != nil {
		return codextui.DaybreakNoticeLimited
	}
	accessToken, accountID := chatGPTAuthCredentials(codexHome)
	if accessToken == "" {
		return codextui.DaybreakNoticeLimited
	}
	if !strings.EqualFold(derefString(status.AuthMethod), "chatgpt") || strings.TrimSpace(derefString(status.AuthToken)) != accessToken {
		return codextui.DaybreakNoticeLimited
	}
	notice, ok := fetchDaybreakNotice(ctx, baseURL, accessToken, accountID)
	if !ok {
		return codextui.DaybreakNoticeLimited
	}
	// The token can rotate while the request is in flight; discard the result
	// rather than caching eligibility read with stale credentials (Rust re-reads
	// local auth before returning).
	if rotatedToken, rotatedAccount := chatGPTAuthCredentials(codexHome); rotatedToken != accessToken || rotatedAccount != accountID {
		return codextui.DaybreakNoticeLimited
	}
	return notice
}

// chatGPTAuthCredentials reads the local ChatGPT access token and account id,
// or empty strings when the stored auth is not ChatGPT.
func chatGPTAuthCredentials(codexHome string) (string, string) {
	snapshot, err := auth.NewStore(codexHome).Load()
	if err != nil || snapshot == nil {
		return "", ""
	}
	account := auth.AccountFromAuth(snapshot)
	if account == nil || account.Type != auth.AccountChatGPT {
		return "", ""
	}
	accessToken := ""
	if raw, ok := snapshot.Tokens["access_token"].(string); ok {
		accessToken = strings.TrimSpace(raw)
	}
	return accessToken, strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
}

// fetchDaybreakNotice issues the authenticated verified-access request and maps
// the programs to a notice.
func fetchDaybreakNotice(ctx context.Context, baseURL string, accessToken string, accountID string) (codextui.DaybreakNotice, bool) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return codextui.DaybreakNoticeLimited, false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+daybreakVerifiedAccessPath, nil)
	if err != nil {
		return codextui.DaybreakNoticeLimited, false
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	httpClient := &http.Client{Timeout: daybreakVerifiedAccessTimeout}
	response, err := httpClient.Do(request)
	if err != nil {
		return codextui.DaybreakNoticeLimited, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return codextui.DaybreakNoticeLimited, false
	}
	var access codextui.DaybreakVerifiedAccess
	if err := json.NewDecoder(io.LimitReader(response.Body, daybreakResponseMaxBytes)).Decode(&access); err != nil {
		return codextui.DaybreakNoticeLimited, false
	}
	return access.Notice(), true
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// interactiveDaybreakBaseURL resolves the effective ChatGPT base URL for the
// verified-access read (Rust config.chatgpt_base_url).
func interactiveDaybreakBaseURL() string {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), nil)
	if err != nil || loaded == nil {
		return config.DefaultChatGPTBaseURL
	}
	if base := strings.TrimSpace(loaded.ChatGPTBaseURL()); base != "" {
		return base
	}
	return config.DefaultChatGPTBaseURL
}

// turnErrorIsCyberPolicy reports whether the server classified a turn error as a
// cybersecurity policy refusal (Rust CodexErrorInfo::CyberPolicy), so the TUI can
// render the Daybreak-aware refusal copy.
func turnErrorIsCyberPolicy(turnErr appserver.TurnError) bool {
	return normalizeCodexErrorKind(turnErr.CodexErrorInfo) == "cyberpolicy"
}

// cyberPolicyError reports whether a local turn failed with a cybersecurity
// policy refusal (Rust CodexErrorInfo::CyberPolicy).
func cyberPolicyError(err error) bool {
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	return apiErr.Details().Kind == codexapi.ErrorCyberPolicy
}

// normalizeCodexErrorKind reads the kind out of a codexErrorInfo payload, which
// is a bare string for most classifications and a tagged object for the ones
// carrying an HTTP status.
func normalizeCodexErrorKind(info any) string {
	text := ""
	switch value := info.(type) {
	case string:
		text = value
	case map[string]any:
		for _, key := range []string{"type", "kind"} {
			if candidate, ok := value[key].(string); ok {
				text = candidate
				break
			}
		}
	}
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "_", "")
	text = strings.ReplaceAll(text, "-", "")
	return text
}
