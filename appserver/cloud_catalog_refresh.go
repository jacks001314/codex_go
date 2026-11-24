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
	"codex_go/telemetry"
)

const (
	// cloudCatalogRetryBaseDelay and cloudCatalogRetryMaxDelay bound the
	// per-thread cooldown after an incomplete cloud catalog discovery,
	// mirroring Rust's CatalogRefreshBackoff (#61400): 5 seconds doubling to
	// 5 minutes.
	cloudCatalogRetryBaseDelay = 5 * time.Second
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

// cloudCatalogRefreshAcquireLockSpanName mirrors Rust's
// `cloud_catalog.refresh.acquire_lock` span (#63475), which keeps the refresh
// mutex wait separate from catalog preparation and refresh work.
const cloudCatalogRefreshAcquireLockSpanName = "cloud_catalog.refresh.acquire_lock"

// lockCloudCatalogRefresh waits for the per-thread refresh mutex and records the
// wait in the acquire_lock span, nesting under the caller's span when one is
// carried by ctx, and marks `acquired` once the lock is held.
func (r *RuntimeRouter) lockCloudCatalogRefresh(ctx context.Context, state *cloudCatalogRefreshState) {
	var span *telemetry.Span
	if parent := telemetry.SpanFromContext(ctx); parent != nil {
		span = parent.StartChildSpan(cloudCatalogRefreshAcquireLockSpanName, nil)
	} else if r != nil {
		if tracer := r.requestTracer(); tracer != nil {
			span = tracer.StartSpan(cloudCatalogRefreshAcquireLockSpanName, nil)
		}
	}
	state.refreshMu.Lock()
	if span != nil {
		span.SetAttribute("acquired", "true")
		span.End()
	}
}

// cloudCatalogRefreshState tracks one thread's catalog cooldowns and the
// successful discovery a turn can reuse across model steps.
type cloudCatalogRefreshState struct {
	mu sync.Mutex
	// refreshMu serializes discovery requests for this thread.
	refreshMu     sync.Mutex
	pluginBackoff *cloudCatalogRefreshBackoff
	skillBackoff  *cloudCatalogRefreshBackoff
	// pluginCompleteScope records the auth/provider generation of the last
	// complete, warning-free plugin catalog. Later steps and turns in that scope
	// reuse it; partial or warning-bearing catalogs keep retrying (Rust #61386
	// and #61400).
	pluginCompleteScope string
	pluginComplete      bool
	// skillWarningTurn deduplicates the cloud skill discovery warning to once
	// per turn (Rust #55957).
	skillWarningTurn string
	// startupRetryPending leaves a failed MCP startup check for the next holder
	// of refreshMu if this discovery is cancelled before it completes. It is
	// protected by mu and mirrors Rust's CatalogRefreshState (#62876).
	startupRetryPending bool
	now                 func() time.Time
}

// queueMCPStartupRetry marks the current catalog refresh as responsible for
// checking failed MCP startups when it finishes.
func (s *cloudCatalogRefreshState) queueMCPStartupRetry() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.startupRetryPending = true
	s.mu.Unlock()
}

// takeMCPStartupRetry consumes the pending startup check exactly once.
func (s *cloudCatalogRefreshState) takeMCPStartupRetry() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.startupRetryPending
	s.startupRetryPending = false
	return pending
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
	s.pluginComplete = false
	s.pluginCompleteScope = ""
}

func (s *cloudCatalogRefreshState) recordPluginResult(scope string, retry bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if retry {
		s.pluginBackoff = nextCloudCatalogBackoff(scope, s.pluginBackoff, s.clock())
		s.pluginComplete = false
		s.pluginCompleteScope = ""
		return
	}
	s.pluginBackoff = nil
	s.pluginComplete = true
	s.pluginCompleteScope = scope
}

func (s *cloudCatalogRefreshState) resetPlugin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pluginBackoff = nil
	s.pluginComplete = false
	s.pluginCompleteScope = ""
}

func (s *cloudCatalogRefreshState) pluginCatalogComplete(scope string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return scope != "" && s.pluginComplete && s.pluginCompleteScope == scope
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

// finishCloudCatalogStartup performs the retry check that a cancelled
// discovery left for the next catalog lock holder. Only completed, eligible
// startup failures schedule another prewarm (Rust #62876).
func (r *RuntimeRouter) finishCloudCatalogStartup(threadID string, state *cloudCatalogRefreshState) {
	if r == nil || state == nil || r.mcpRuntimes == nil || !state.takeMCPStartupRetry() {
		return
	}
	service := r.mcpRuntimes.publishedService(threadID)
	if service == nil || !service.HasFailedStartupForRetry() {
		return
	}
	r.requestMCPRuntimeReprojection(threadID)
}

// refreshCloudPluginCatalogForStep refreshes the cloud plugin catalog before a
// model step when the authentication scope changed mid-turn. A successful
// discovery is reused for the rest of the turn, failures back off, concurrent
// refreshes for one thread are serialized (Rust #55957/#55968), and MCP startup
// is queued as soon as publication changes the contributor set (Rust #62876).
func (r *RuntimeRouter) refreshCloudPluginCatalogForStep(ctx context.Context, cfg *config.Config, threadID string, turnID string) {
	if r == nil || r.services.Plugins == nil {
		return
	}
	state := r.cloudCatalogs.forThread(threadID)
	if cfg == nil || !features.Enabled(cfg.FeatureSettings(), "plugins") || r.services.PluginCatalogProvider == nil {
		hadCatalog := r.services.Plugins.CloudPluginCatalogKey() != "" || r.services.Plugins.CloudPluginCatalog() != nil
		r.services.Plugins.SetCloudPluginCatalog(nil, "")
		state.resetPlugin()
		if hadCatalog {
			r.requestMCPRuntimeReprojection(threadID)
		}
		return
	}
	scope := r.cloudPluginCatalogScope()
	if scope == "" {
		return
	}
	if state.pluginCatalogComplete(scope) || state.pluginBackoffPending(scope) {
		return
	}
	// Leave the retry flag set while waiting for the lock so cancellation keeps
	// the startup check for the next discovery.
	state.queueMCPStartupRetry()
	r.lockCloudCatalogRefresh(ctx, state)
	defer state.refreshMu.Unlock()
	r.finishCloudCatalogStartup(threadID, state)
	if state.pluginCatalogComplete(scope) || state.pluginBackoffPending(scope) {
		return
	}
	// Re-arm the check for this discovery. It must survive a cancellation that
	// returns before the end-of-refresh check below.
	state.queueMCPStartupRetry()
	catalog, err := plugin.ListPluginCatalog(ctx, r.services.PluginCatalogProvider, plugin.PluginListQuery{
		ThreadID: strings.TrimSpace(threadID),
		TurnID:   strings.TrimSpace(turnID),
	})
	if err != nil {
		if cloudCatalogDiscoveryCanceled(err) {
			// The pending flag survives for the next lock holder.
			return
		}
		state.recordPluginFailure(scope)
		r.finishCloudCatalogStartup(threadID, state)
		return
	}
	if r.cloudPluginCatalogScope() != scope {
		r.finishCloudCatalogStartup(threadID, state)
		return
	}
	r.services.Plugins.SetCloudPluginCatalog(catalog, scope)
	// Queue the prewarm before metrics or a slower sibling's completion so the
	// MCP servers can start while cloud discovery is still winding down.
	r.requestMCPRuntimeReprojection(threadID)
	needsRetry := catalog == nil || catalog.IsPartial || len(catalog.Warnings) > 0
	state.recordPluginResult(scope, needsRetry)
	// Rust #56530 measures the published cloud plugin catalog.
	r.recordCatalogEntries(pluginCatalogEntriesMetric, catalogStageCloud, len(catalog.Entries), catalog.IsPartial, len(catalog.Warnings))
	r.finishCloudCatalogStartup(threadID, state)
}
