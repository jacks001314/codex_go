package appserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/model"
)

// Rust parity: codex-rs/app-server/src/request_processors/account_processor/
// workspace_routing.rs. Discovery resolves the selected ChatGPT workspace's
// backend origin and account routing override from the accounts/check endpoint.
// The error messages match Rust verbatim because account/read reports routing
// failures as internal errors.
var (
	errWorkspaceRoutingMissingAccountID     = errors.New("workspace routing requires a ChatGPT account id")
	errWorkspaceRoutingDiscoveryFailed      = errors.New("workspace routing discovery failed")
	errWorkspaceRoutingUnauthorized         = errors.New("workspace routing discovery unauthorized (401)")
	errWorkspaceRoutingMissingWorkspace     = errors.New("selected workspace missing from routing discovery")
	errWorkspaceRoutingDuplicateWorkspace   = errors.New("duplicate workspace in routing discovery")
	errWorkspaceRoutingMissingBackendOrigin = errors.New("workspace routing discovery missing backend origin")
	errWorkspaceRoutingInvalidOverride      = errors.New("workspace routing discovery has invalid account routing override")
	errWorkspaceRoutingBackendNotOrigin     = errors.New("workspace routing discovery must return an origin")
	errWorkspaceRoutingBackendConflict      = errors.New("required ChatGPT backend conflicts with workspace routing")
	errWorkspaceRoutingInvalidBackendURL    = errors.New("invalid workspace backend URL")
	errWorkspaceRoutingInvalidBackendOrigin = errors.New("workspace backend must use an HTTPS origin without credentials")
)

// workspaceRoutingCacheKey mirrors Rust's WorkspaceRoutingKey: a cache hit is
// only valid for the same credential generation, workspace, and backend scope.
type workspaceRoutingCacheKey struct {
	authGeneration  uint64
	ownerGeneration uint64
	accountID       string
	baseURL         string
	requiredBaseURL string
}

// workspaceRoutingFetch coordinates one discovery per key: waiters share the
// first result, and a generation change starts a fresh key (Rust
// WorkspaceRoutingFetch + the weak-entry map).
type workspaceRoutingFetch struct {
	mu      sync.Mutex
	routing *auth.WorkspaceRouting
	done    bool
}

// workspaceRoutingState is the router's discovery cache.
type workspaceRoutingState struct {
	mu      sync.Mutex
	fetches map[workspaceRoutingCacheKey]*workspaceRoutingFetch
}

func (r *RuntimeRouter) workspaceRoutingFetch(key workspaceRoutingCacheKey) *workspaceRoutingFetch {
	r.workspaceRoutingOnce.Do(func() {
		r.workspaceRouting = &workspaceRoutingState{fetches: map[workspaceRoutingCacheKey]*workspaceRoutingFetch{}}
	})
	state := r.workspaceRouting
	state.mu.Lock()
	defer state.mu.Unlock()
	if fetch, ok := state.fetches[key]; ok {
		return fetch
	}
	// Keep only the newest key so a long-lived router does not retain every
	// credential generation it has ever seen.
	state.fetches = map[workspaceRoutingCacheKey]*workspaceRoutingFetch{}
	fetch := &workspaceRoutingFetch{}
	state.fetches[key] = fetch
	return fetch
}

// workspaceRoutingForAccountRead resolves the workspace routing for an
// account/read call, mirroring Rust read_account with no request scope. It
// returns nil when the credential is not a ChatGPT workspace credential.
func (r *RuntimeRouter) workspaceRoutingForAccountRead(ctx context.Context, requiredBaseURL string, snapshot *auth.AuthDotJSON) (*auth.WorkspaceRouting, error) {
	if r == nil || snapshot == nil {
		return nil, nil
	}
	// Rust is_chatgpt_auth: ChatGPT, ChatGPT auth tokens, and personal access
	// tokens all represent an authenticated human ChatGPT account.
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token":
	default:
		return nil, nil
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	if accountID == "" {
		return nil, errWorkspaceRoutingMissingAccountID
	}
	state := auth.AuthChangeState{}
	if r.authChangeTracker != nil {
		state = r.authChangeTracker.Snapshot()
	}
	baseURL := r.chatGPTBaseURL()
	key := workspaceRoutingCacheKey{
		authGeneration:  state.Generation,
		ownerGeneration: state.OwnerGeneration,
		accountID:       accountID,
		baseURL:         baseURL,
		requiredBaseURL: strings.TrimSpace(requiredBaseURL),
	}
	fetch := r.workspaceRoutingFetch(key)
	fetch.mu.Lock()
	defer fetch.mu.Unlock()
	if fetch.done {
		return cloneWorkspaceRouting(fetch.routing), nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	routing, err := r.discoverWorkspaceRouting(discoveryCtx, snapshot, accountID, baseURL, requiredBaseURL)
	if err != nil {
		return nil, err
	}
	fetch.routing = routing
	fetch.done = true
	return cloneWorkspaceRouting(routing), nil
}

func (r *RuntimeRouter) discoverWorkspaceRouting(ctx context.Context, snapshot *auth.AuthDotJSON, accountID string, baseURL string, requiredBaseURL string) (*auth.WorkspaceRouting, error) {
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, errWorkspaceRoutingDiscoveryFailed
	}
	response, err := client.GetAccountsCheck(ctx)
	if err != nil {
		var statusErr *chatgptapi.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
			return nil, errWorkspaceRoutingUnauthorized
		}
		return nil, errWorkspaceRoutingDiscoveryFailed
	}
	var entry *chatgptapi.AccountsCheckEntry
	for i := range response.Accounts {
		if strings.TrimSpace(response.Accounts[i].ID) != accountID {
			continue
		}
		if entry != nil {
			return nil, errWorkspaceRoutingDuplicateWorkspace
		}
		candidate := response.Accounts[i]
		entry = &candidate
	}
	if entry == nil {
		return nil, errWorkspaceRoutingMissingWorkspace
	}
	return resolveWorkspaceRouting(*entry, requiredBaseURL, baseURL)
}

// resolveWorkspaceRouting mirrors Rust resolve_routing: validate the discovered
// backend origin and override, reconcile them with the required backend, and
// fall back to the effective base URL when neither is set.
func resolveWorkspaceRouting(entry chatgptapi.AccountsCheckEntry, requiredBaseURL string, effectiveBaseURL string) (*auth.WorkspaceRouting, error) {
	backend := ""
	if entry.WorkspaceBackendOrigin != nil {
		backend = strings.TrimSpace(*entry.WorkspaceBackendOrigin)
	}
	if backend == "" {
		return nil, errWorkspaceRoutingMissingBackendOrigin
	}
	override := ""
	if entry.AccountRoutingOverride != nil {
		override = strings.TrimSpace(*entry.AccountRoutingOverride)
	}
	if !auth.ValidAccountRoutingOverride(override) {
		return nil, errWorkspaceRoutingInvalidOverride
	}
	var required *url.URL
	if requiredBaseURL != "" {
		parsed, err := parseWorkspaceBackendURL(requiredBaseURL)
		if err != nil {
			return nil, err
		}
		required = parsed
	}
	var discovered *url.URL
	if backend != "NO_CONSTRAINT" {
		parsed, err := parseWorkspaceBackendURL(backend)
		if err != nil {
			return nil, err
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return nil, errWorkspaceRoutingBackendNotOrigin
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errWorkspaceRoutingBackendNotOrigin
		}
		discovered = parsed
	}
	var origin string
	switch {
	case required != nil && discovered != nil:
		if workspaceOrigin(required) != workspaceOrigin(discovered) {
			return nil, errWorkspaceRoutingBackendConflict
		}
		origin = workspaceOrigin(required)
	case required != nil:
		origin = workspaceOrigin(required)
	case discovered != nil:
		origin = workspaceOrigin(discovered)
	default:
		effective, err := parseWorkspaceBackendURL(effectiveBaseURL)
		if err != nil {
			return nil, err
		}
		origin = workspaceOrigin(effective)
	}
	return &auth.WorkspaceRouting{
		ChatGPTAccountID:       entry.ID,
		BackendOrigin:          origin,
		AccountRoutingOverride: auth.AccountRoutingOverride(override),
	}, nil
}

// parseWorkspaceBackendURL mirrors Rust parse_backend_url: an absolute HTTPS
// origin without credentials and without surrounding whitespace.
func parseWorkspaceBackendURL(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errWorkspaceRoutingInvalidBackendOrigin
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, errWorkspaceRoutingInvalidBackendURL
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errWorkspaceRoutingInvalidBackendOrigin
	}
	return parsed, nil
}

// workspaceOrigin serializes an origin the way Rust's Url::origin does.
func workspaceOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host
}

func cloneWorkspaceRouting(routing *auth.WorkspaceRouting) *auth.WorkspaceRouting {
	if routing == nil {
		return nil
	}
	cloned := *routing
	return &cloned
}

// applyWorkspaceRoutingToAgent mirrors Rust RuntimeProvider::responses_api_provider:
// a first-party ChatGPT provider that may use the dedicated Codex backend
// routes has its Responses provider rewritten to the discovered workspace
// backend (and rejects redirects from then on). Providers that cannot use the
// Codex backend routes keep their configured origin.
func (r *RuntimeRouter) applyWorkspaceRoutingToAgent(ctx context.Context, provider *model.ProviderInfo, snapshot *auth.AuthDotJSON, agent *model.ResponsesAgentRunner) error {
	if r == nil || provider == nil || agent == nil || !provider.SupportsCodexBackendRoutes() {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(provider.Name), model.OpenAIProviderName) {
		return nil
	}
	routing, err := r.workspaceRoutingForAccountRead(ctx, r.requiredChatGPTBaseURL(), snapshot)
	if err != nil {
		return err
	}
	if routing == nil {
		return nil
	}
	if err := model.ApplyWorkspaceRouting(agent.Provider, routing); err != nil {
		return err
	}
	agent.RejectRedirects = true
	return nil
}
