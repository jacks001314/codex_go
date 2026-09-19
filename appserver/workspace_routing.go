package appserver

import (
	"context"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/model"
)

// Rust parity: codex-rs/app-server/src/request_processors/account_processor/
// workspace_routing.rs. The discovery/resolution core lives in the model
// package (shared with the exec provider path); this file owns the app-server's
// per-credential-generation cache and the account/read + turn-provider wiring.

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
// WorkspaceRoutingFetch plus the weak-entry map).
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
	accountID, ok := chatGPTWorkspaceAccountID(snapshot)
	if !ok {
		return nil, nil
	}
	if accountID == "" {
		return nil, model.ErrWorkspaceRoutingMissingAccountID
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

// chatGPTWorkspaceAccountID mirrors Rust is_chatgpt_auth plus get_account_id:
// ChatGPT, ChatGPT auth tokens, and personal access tokens all represent an
// authenticated human ChatGPT account.
func chatGPTWorkspaceAccountID(snapshot *auth.AuthDotJSON) (string, bool) {
	if snapshot == nil {
		return "", false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token":
	default:
		return "", false
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	return accountID, true
}

func (r *RuntimeRouter) discoverWorkspaceRouting(ctx context.Context, snapshot *auth.AuthDotJSON, accountID string, baseURL string, requiredBaseURL string) (*auth.WorkspaceRouting, error) {
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, model.ErrWorkspaceRoutingDiscoveryFailed
	}
	return model.DiscoverWorkspaceRouting(ctx, client, accountID, requiredBaseURL, baseURL)
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

// notifyWorkspaceRoutingToConnection mirrors Rust
// AccountRequestProcessor::notify_workspace_routing_to_connection: once a
// connection has initialized, discover the workspace routing (best effort) and,
// when it resolved and the credential owner is unchanged, tell that connection
// the account was updated. Discovery runs off the request path, like Rust's
// spawned task.
func (r *RuntimeRouter) notifyWorkspaceRoutingToConnection(connectionID string) {
	if r == nil || r.authChangeTracker == nil {
		return
	}
	ownerGeneration := r.authChangeTracker.Snapshot().OwnerGeneration
	go func() {
		if r.authChangeTracker.Snapshot().OwnerGeneration != ownerGeneration {
			return
		}
		var snapshot *auth.AuthDotJSON
		if resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout()); err == nil && resolved != nil {
			snapshot = &resolved.Auth
		}
		if snapshot == nil {
			return
		}
		routing, err := r.workspaceRoutingForAccountRead(context.Background(), r.requiredChatGPTBaseURL(), snapshot)
		if err != nil || routing == nil {
			return
		}
		if r.authChangeTracker.Snapshot().OwnerGeneration != ownerGeneration {
			return
		}
		r.notifyToConnection(connectionID, NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	}()
}
