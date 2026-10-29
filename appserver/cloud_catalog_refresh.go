package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/skillprovider"
)

const (
	// cloudCatalogRetryBaseDelay and cloudCatalogRetryMaxDelay bound the
	// per-thread cooldown after a failed cloud catalog discovery, mirroring
	// Rust's CatalogRefreshBackoff (#55968): 30 seconds doubling to 5 minutes.
	cloudCatalogRetryBaseDelay = 30 * time.Second
	cloudCatalogRetryMaxDelay  = 5 * time.Minute
)

// cloudCatalogRefreshBackoff is one catalog's pending cooldown. The scope
// captures the authentication and provider generation the failure belongs to,
// so an account change or configuration reset permits an immediate retry
// (Rust's McpResourceClientAuthKey/McpResourceServerCacheKey scoping).
type cloudCatalogRefreshBackoff struct {
	scope   string
	delay   time.Duration
	retryAt time.Time
}

func (b *cloudCatalogRefreshBackoff) pending(scope string, now time.Time) bool {
	if b == nil || b.scope != scope {
		return false
	}
	return now.Before(b.retryAt)
}

func nextCloudCatalogBackoff(scope string, previous *cloudCatalogRefreshBackoff, now time.Time) *cloudCatalogRefreshBackoff {
	delay := cloudCatalogRetryBaseDelay
	if previous != nil && previous.scope == scope {
		delay = previous.delay * 2
		if delay > cloudCatalogRetryMaxDelay {
			delay = cloudCatalogRetryMaxDelay
		}
	}
	return &cloudCatalogRefreshBackoff{scope: scope, delay: delay, retryAt: now.Add(delay)}
}

// cloudCatalogRefreshStore holds one refresh state per thread, mirroring
// Rust's per-thread PluginsThreadState and SkillCatalogState (#55968): a
// failure on one thread must not suppress discovery for another.
type cloudCatalogRefreshStore struct {
	mu       sync.Mutex
	byThread map[string]*cloudCatalogRefreshState
}

func (s *cloudCatalogRefreshStore) forThread(threadID string) *cloudCatalogRefreshState {
	key := strings.TrimSpace(threadID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byThread == nil {
		s.byThread = map[string]*cloudCatalogRefreshState{}
	}
	state := s.byThread[key]
	if state == nil {
		state = &cloudCatalogRefreshState{}
		s.byThread[key] = state
	}
	return state
}

func (s *cloudCatalogRefreshStore) dropThread(threadID string) {
	key := strings.TrimSpace(threadID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byThread, key)
}

// cloudCatalogRefreshState tracks one thread's catalog cooldowns and the
// successful discovery a turn can reuse across model steps.
type cloudCatalogRefreshState struct {
	mu sync.Mutex
	// refreshMu serializes discovery requests for this thread.
	refreshMu     sync.Mutex
	pluginBackoff *cloudCatalogRefreshBackoff
	skillBackoff  *cloudCatalogRefreshBackoff
	// refreshedTurn/refreshedScope record the last successful plugin discovery
	// so later steps of the same turn and auth scope reuse it (Rust #55957).
	refreshedTurn  string
	refreshedScope string
	// skillWarningTurn deduplicates the cloud skill discovery warning to once
	// per turn (Rust #55957).
	skillWarningTurn string
	now              func() time.Time
}

func (s *cloudCatalogRefreshState) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *cloudCatalogRefreshState) pluginBackoffPending(scope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pluginBackoff.pending(scope, s.clock())
}

func (s *cloudCatalogRefreshState) recordPluginFailure(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginBackoff = nextCloudCatalogBackoff(scope, s.pluginBackoff, s.clock())
	s.refreshedTurn = ""
	s.refreshedScope = ""
}

func (s *cloudCatalogRefreshState) recordPluginSuccess(turnID string, scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginBackoff = nil
	s.refreshedTurn = strings.TrimSpace(turnID)
	s.refreshedScope = scope
}

func (s *cloudCatalogRefreshState) resetPlugin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginBackoff = nil
	s.refreshedTurn = ""
	s.refreshedScope = ""
}

func (s *cloudCatalogRefreshState) pluginRefreshed(turnID string, scope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnID = strings.TrimSpace(turnID)
	return turnID != "" && s.refreshedTurn == turnID && s.refreshedScope == scope
}

func (s *cloudCatalogRefreshState) skillBackoffPending(scope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skillBackoff.pending(scope, s.clock())
}

func (s *cloudCatalogRefreshState) recordSkillFailure(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillBackoff = nextCloudCatalogBackoff(scope, s.skillBackoff, s.clock())
}

func (s *cloudCatalogRefreshState) clearSkillBackoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillBackoff = nil
}

func (s *cloudCatalogRefreshState) shouldWarnSkillFailure(turnID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return true
	}
	if s.skillWarningTurn == turnID {
		return false
	}
	s.skillWarningTurn = turnID
	return true
}

// cloudPluginCatalogScope identifies the authentication and provider generation
// a cached cloud catalog belongs to.
func (r *RuntimeRouter) cloudPluginCatalogScope() string {
	if r == nil || r.services.PluginCatalogProvider == nil {
		return ""
	}
	codexHome := ""
	if r.services.Config != nil {
		codexHome = strings.TrimSpace(r.services.Config.CodexHome())
	}
	identity := remoteInstalledPluginsAuthIdentityFromSnapshot(r.accountAuthSnapshot(codexHome))
	return fmt.Sprintf("%T\x00%s", r.services.PluginCatalogProvider, remoteInstalledPluginsAuthIdentityKey(identity))
}

// cloudCatalogDiscoveryCanceled reports whether a discovery failure was caused
// by cancellation, which Rust retries immediately instead of backing off.
func cloudCatalogDiscoveryCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// cloudSkillCatalogScope identifies the authentication and MCP resource
// generation a cloud skill catalog belongs to, mirroring Rust's
// McpResourceClientAuthKey plus McpResourceServerCacheKey pair (#55968). A
// changed account or reconnected server therefore permits an immediate retry.
func (r *RuntimeRouter) cloudSkillCatalogScope(service *mcp.MCPService) string {
	codexHome := ""
	if r != nil && r.services.Config != nil {
		codexHome = strings.TrimSpace(r.services.Config.CodexHome())
	}
	identity := remoteInstalledPluginsAuthIdentityFromSnapshot(r.accountAuthSnapshot(codexHome))
	generation := uint64(0)
	// A provider that never reads MCP resources (a frozen snapshot, #55631)
	// keeps one generation so reconnects do not invalidate its catalog.
	if service != nil && skillprovider.UsesMCPResources(r.services.CloudSkills) {
		generation = service.ResourceCacheGeneration()
	}
	return fmt.Sprintf("%s\x00%d", remoteInstalledPluginsAuthIdentityKey(identity), generation)
}

// refreshCloudPluginCatalogForStep refreshes the cloud plugin catalog before a
// model step when the authentication scope changed mid-turn. A successful
// discovery is reused for the rest of the turn, failures back off, and
// concurrent refreshes for one thread are serialized (Rust #55957/#55968).
func (r *RuntimeRouter) refreshCloudPluginCatalogForStep(ctx context.Context, cfg *config.Config, threadID string, turnID string) {
	if r == nil || r.services.Plugins == nil {
		return
	}
	state := r.cloudCatalogs.forThread(threadID)
	if cfg == nil || !features.Enabled(cfg.FeatureSettings(), "plugins") || r.services.PluginCatalogProvider == nil {
		r.services.Plugins.SetCloudPluginCatalog(nil, "")
		state.resetPlugin()
		return
	}
	scope := r.cloudPluginCatalogScope()
	if scope == "" {
		return
	}
	if state.pluginRefreshed(turnID, scope) {
		return
	}
	if state.pluginBackoffPending(scope) {
		return
	}
	state.refreshMu.Lock()
	defer state.refreshMu.Unlock()
	if state.pluginRefreshed(turnID, scope) || state.pluginBackoffPending(scope) {
		return
	}
	catalog, err := plugin.ListPluginCatalog(ctx, r.services.PluginCatalogProvider, plugin.PluginListQuery{
		ThreadID: strings.TrimSpace(threadID),
		TurnID:   strings.TrimSpace(turnID),
	})
	if err != nil {
		if !cloudCatalogDiscoveryCanceled(err) {
			state.recordPluginFailure(scope)
		}
		return
	}
	if r.cloudPluginCatalogScope() != scope {
		return
	}
	r.services.Plugins.SetCloudPluginCatalog(catalog, scope)
	state.recordPluginSuccess(turnID, scope)
	// Rust #56530 measures the published cloud plugin catalog.
	r.recordCatalogEntries(pluginCatalogEntriesMetric, catalogStageCloud, len(catalog.Entries), catalog.IsPartial, len(catalog.Warnings))
}
