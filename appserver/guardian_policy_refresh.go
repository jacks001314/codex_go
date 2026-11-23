package appserver

// Rust parity: codex-rs/core/src/thread_manager/registry.rs (#62496),
// codex-rs/core/src/session/config_refresh.rs (#62585) and
// codex-rs/app-server/src/request_processors/config_processor.rs (#62614).
//
// Go keeps one process-wide configuration and rebuilds a turn's configuration
// from it, so the Go equivalents of Rust's per-session policy snapshot are:
//
//   - one published observation with a monotonic revision, shared by every
//     thread through the ThreadManager;
//   - a registration pause while a refresh loads and publishes, so a turn that
//     starts during a refresh cannot register under superseded policy;
//   - a startup apply for every freshly built turn configuration, preferring a
//     publication that overtook the turn's own load;
//   - a per-live-turn application that replaces the captured turn
//     configuration's Guardian-owned values and re-resolves its reviewer.
//
// A publication keeps the rest of each captured configuration, matching Rust's
// ConfigRefreshOutcome::Published: only the reviewer restrictions, the managed
// policy fallbacks and the policy revision change.

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"codex_go/config"
)

// GuardianPolicyLoader reads the current managed Guardian requirements using
// the host's configuration authority (Rust GuardianPolicyLoader).
type GuardianPolicyLoader interface {
	LoadGuardianPolicySnapshot(revision int64) (*config.GuardianPolicySnapshot, error)
}

// guardianPolicyRejection reports one live thread that refused a published
// policy. Rust returns an error if any live thread rejects the update.
type guardianPolicyRejection struct {
	ThreadID string
	Reason   string
}

// errGuardianPolicySourceChanged reports a managed policy source that moved
// while an observation was loading (Rust "Guardian policy source changed").
var errGuardianPolicySourceChanged = errors.New("Guardian policy source changed")

// ReserveGuardianPolicyRevision allocates the next observation revision. The
// revision is allocated before loading so publication can reject older
// in-flight work even when the observation itself did not change.
func (m *ThreadManager) ReserveGuardianPolicyRevision() (int64, error) {
	if m == nil {
		return 0, errors.New("guardian policy: thread manager is nil")
	}
	m.guardianMu.Lock()
	defer m.guardianMu.Unlock()
	return m.reserveGuardianPolicyRevisionLocked()
}

func (m *ThreadManager) reserveGuardianPolicyRevisionLocked() (int64, error) {
	if m.guardianLastRevision == int64(^uint64(0)>>1) {
		return 0, errors.New("Guardian observation revisions exhausted")
	}
	m.guardianLastRevision++
	return m.guardianLastRevision, nil
}

// BeginGuardianPolicyRegistration blocks a new turn registration while a
// refresh is loading and publishing policy, and returns the release function
// its caller must run once the registration boundary is over.
func (m *ThreadManager) BeginGuardianPolicyRegistration() func() {
	if m == nil {
		return func() {}
	}
	for {
		m.guardianMu.Lock()
		if !m.guardianRefreshActive {
			m.guardianMu.Unlock()
			return func() {}
		}
		done := m.guardianRefreshDone
		m.guardianMu.Unlock()
		if done == nil {
			return func() {}
		}
		<-done
	}
}

// PublishedGuardianPolicyRevision reports the newest published observation.
func (m *ThreadManager) PublishedGuardianPolicyRevision() int64 {
	if m == nil {
		return 0
	}
	m.guardianMu.Lock()
	defer m.guardianMu.Unlock()
	return m.guardianPublishedRevision
}

// PrepareGuardianPolicyForStartup applies the current managed policy to a
// configuration a thread is about to register with (Rust
// ThreadRegistry::prepare_startup_guardian_policy).
func (m *ThreadManager) PrepareGuardianPolicyForStartup(cfg *config.Config, loader GuardianPolicyLoader) error {
	if m == nil || cfg == nil {
		return nil
	}
	snapshot, err := m.guardianPolicyForStartup(cfg, loader)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	return cfg.ApplyGuardianPolicy(snapshot)
}

func (m *ThreadManager) guardianPolicyForStartup(cfg *config.Config, loader GuardianPolicyLoader) (*config.GuardianPolicySnapshot, error) {
	revision := cfg.GuardianPolicyRevision()
	binding := cfg.GuardianPolicyBinding()
	staleBinding := revision != 0 && binding != nil && binding.Status() != config.CloudConfigBundleBindingCurrent
	m.guardianMu.Lock()
	published := m.guardianPublished
	publishedRevision := m.guardianPublishedRevision
	fallbackLoader := m.guardianLoader
	m.guardianMu.Unlock()
	if loader == nil {
		loader = fallbackLoader
	}
	if published != nil && publishedRevision > revision {
		return published, nil
	}
	if revision != 0 && !staleBinding {
		return nil, nil
	}
	if loader == nil {
		return nil, nil
	}
	reserved, err := m.ReserveGuardianPolicyRevision()
	if err != nil {
		return nil, err
	}
	snapshot, err := loader.LoadGuardianPolicySnapshot(reserved)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("guardian policy: loader returned no observation")
	}
	if snapshot.Binding != nil && snapshot.Binding.Status() != config.CloudConfigBundleBindingCurrent {
		return nil, errGuardianPolicySourceChanged
	}
	// A publication may have overtaken this startup load. Register the thread
	// with the published policy instead of the superseded observation.
	m.guardianMu.Lock()
	if m.guardianPublished != nil && m.guardianPublishedRevision > snapshot.Revision {
		snapshot = m.guardianPublished
	}
	m.guardianMu.Unlock()
	return snapshot, nil
}

// RefreshGuardianPolicy loads one observation, publishes it, and then applies
// it to every live thread concurrently (Rust ThreadRegistry::refresh_guardian_policy).
// Registration is paused until publication, releases even when the refresh
// caller is cancelled, and the returned error reports a rejected update.
func (m *ThreadManager) RefreshGuardianPolicy(
	loader GuardianPolicyLoader,
	applyLive func(*config.GuardianPolicySnapshot) []guardianPolicyRejection,
) error {
	if m == nil {
		return nil
	}
	if loader == nil {
		m.guardianMu.Lock()
		loader = m.guardianLoader
		m.guardianMu.Unlock()
	}
	if loader == nil {
		return nil
	}
	var done chan struct{}
	var revision int64
	for {
		m.guardianMu.Lock()
		if m.guardianRefreshActive {
			wait := m.guardianRefreshDone
			m.guardianMu.Unlock()
			if wait != nil {
				<-wait
			}
			continue
		}
		reserved, err := m.reserveGuardianPolicyRevisionLocked()
		if err != nil {
			m.guardianMu.Unlock()
			return err
		}
		revision = reserved
		m.guardianRefreshActive = true
		done = make(chan struct{})
		m.guardianRefreshDone = done
		m.guardianMu.Unlock()
		break
	}
	// Release the registration pause at publication or when the load fails.
	published := false
	release := func() {
		m.guardianMu.Lock()
		if m.guardianRefreshActive && m.guardianRefreshDone == done {
			m.guardianRefreshActive = false
			m.guardianRefreshDone = nil
			close(done)
		}
		m.guardianMu.Unlock()
	}
	snapshot, err := loader.LoadGuardianPolicySnapshot(revision)
	if err != nil {
		release()
		return err
	}
	if snapshot == nil {
		release()
		return fmt.Errorf("guardian policy: loader returned no observation")
	}
	if snapshot.Binding != nil && snapshot.Binding.Status() != config.CloudConfigBundleBindingCurrent {
		// The managed policy source changed before publication: keep serving
		// the previous policy instead of publishing a stale observation.
		release()
		return nil
	}
	m.guardianMu.Lock()
	publishedRevision, err := m.reserveGuardianPolicyRevisionLocked()
	if err != nil {
		m.guardianMu.Unlock()
		release()
		return err
	}
	snapshot.Revision = publishedRevision
	m.guardianPublished = snapshot
	m.guardianPublishedRevision = publishedRevision
	published = true
	m.guardianMu.Unlock()
	release()
	if !published || applyLive == nil {
		return nil
	}
	rejections := applyLive(snapshot)
	if len(rejections) == 0 {
		return nil
	}
	for _, rejection := range rejections {
		slog.Warn("Guardian policy refresh rejected for thread", "thread_id", rejection.ThreadID, "reason", rejection.Reason)
	}
	return fmt.Errorf("Guardian policy refresh rejected by one or more live threads: %s", formatGuardianPolicyRejections(rejections))
}

func formatGuardianPolicyRejections(rejections []guardianPolicyRejection) string {
	parts := make([]string, 0, len(rejections))
	for _, rejection := range rejections {
		parts = append(parts, fmt.Sprintf("%s: %s", rejection.ThreadID, rejection.Reason))
	}
	return strings.Join(parts, "; ")
}

// SetGuardianPolicyLoader installs the host's policy source for startup and
// on-demand refresh (Rust ThreadManager::with_guardian_policy_loader).
func (m *ThreadManager) SetGuardianPolicyLoader(loader GuardianPolicyLoader) {
	if m == nil {
		return
	}
	m.guardianMu.Lock()
	m.guardianLoader = loader
	m.guardianMu.Unlock()
}

// configServiceGuardianPolicyLoader reads managed Guardian policy through the
// app server's configuration service (Rust #62585 ConfigManager as
// GuardianPolicyLoader). A service without a managed configuration loader has
// no managed policy source, so it reports no loader at all.
type configServiceGuardianPolicyLoader struct {
	service *config.ConfigService
}

func (l configServiceGuardianPolicyLoader) LoadGuardianPolicySnapshot(revision int64) (*config.GuardianPolicySnapshot, error) {
	if l.service == nil {
		return nil, errors.New("guardian policy: configuration service is unavailable")
	}
	loader := l.service.CloudConfigLoader()
	if loader == nil {
		return nil, errors.New("guardian policy: managed configuration loader is unavailable")
	}
	snapshot, err := config.LoadGuardianPolicySnapshot(l.service.CodexHome(), loader, revision)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// guardianPolicyLoader returns the policy source this router refreshes from.
func (r *RuntimeRouter) guardianPolicyLoader() GuardianPolicyLoader {
	if r == nil || r.services.Config == nil || r.services.Config.CloudConfigLoader() == nil {
		return nil
	}
	return configServiceGuardianPolicyLoader{service: r.services.Config}
}

// RefreshGuardianPolicy refreshes managed Guardian policy and reports a live
// thread rejection to the caller (Rust ThreadManager::refresh_guardian_policy).
func (r *RuntimeRouter) RefreshGuardianPolicy() error {
	if r == nil || r.threads == nil {
		return nil
	}
	loader := r.guardianPolicyLoader()
	if loader == nil {
		return nil
	}
	return r.threads.RefreshGuardianPolicy(loader, r.applyGuardianPolicyToLiveThreads)
}

// refreshGuardianPolicyBestEffort mirrors Rust refresh_guardian_policy in the
// request processors: a failure is logged and the server keeps serving.
func (r *RuntimeRouter) refreshGuardianPolicyBestEffort() {
	if err := r.RefreshGuardianPolicy(); err != nil {
		slog.Warn("failed to refresh Guardian policy", "error", err)
	}
}

// applyGuardianPolicyToLiveThreads publishes the observation and updates every
// live turn concurrently (Rust applies live sessions with join_all).
func (r *RuntimeRouter) applyGuardianPolicyToLiveThreads(snapshot *config.GuardianPolicySnapshot) []guardianPolicyRejection {
	if r == nil || snapshot == nil {
		return nil
	}
	if r.services.Config != nil {
		r.services.Config.PublishGuardianPolicyRequirements(snapshot.Requirements)
	}
	threadIDs := r.threads.LiveThreadIDs()
	rejections := make([]guardianPolicyRejection, 0, len(threadIDs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range threadIDs {
		threadID := string(id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rejection := r.applyGuardianPolicyToThread(threadID, snapshot)
			if rejection == nil {
				return
			}
			mu.Lock()
			rejections = append(rejections, *rejection)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(rejections, func(i, j int) bool { return rejections[i].ThreadID < rejections[j].ThreadID })
	return rejections
}

// applyGuardianPolicyToThread replaces one live turn's captured Guardian values
// and re-resolves its reviewer. The updated values stay with the turn the way
// Rust's ConfigRefreshOutcome publication keeps the rest of the captured
// configuration (#59642); the next sampling step reads the new configuration.
func (r *RuntimeRouter) applyGuardianPolicyToThread(threadID string, snapshot *config.GuardianPolicySnapshot) *guardianPolicyRejection {
	active := r.threads.ActiveTurn(threadID)
	if active == nil || active.RunConfig == nil || active.RunConfig.Config == nil {
		return nil
	}
	var (
		blockedReason string
		applied       bool
	)
	r.threads.UpdateTurn(threadID, active.TurnID, func(live *activeRuntimeTurn) {
		if live == nil || live.RunConfig == nil || live.RunConfig.Config == nil {
			return
		}
		next := live.RunConfig.Config.CloneForGuardianPolicy()
		if err := next.ApplyGuardianPolicy(snapshot); err != nil {
			blockedReason = err.Error()
			live.GuardianPolicyRevision = snapshot.Revision
			live.GuardianPolicyBlocked = true
			live.GuardianPolicyBlockedReason = blockedReason
			applied = true
			return
		}
		model := strings.TrimSpace(live.RunConfig.Model)
		if model == "" && live.Params != nil {
			model = strings.TrimSpace(live.Params.Model)
		}
		requested := ""
		if live.Params != nil && live.Params.ApprovalsReviewer != nil {
			requested = strings.TrimSpace(*live.Params.ApprovalsReviewer)
		}
		if requested == "" {
			requested = strings.TrimSpace(live.RunConfig.ApprovalsReviewer)
		}
		resolved, resolveErr := resolveGuardianReviewer(requested, model, next.Requirements)
		reviewer := resolved
		if resolveErr != nil {
			// Publish the restrictions even when they rule out the current
			// model, and keep the constrained default as the reviewer
			// (Rust #62585).
			reviewer = guardianPolicyConstrainedReviewer(next.Requirements)
			blockedReason = fmt.Sprintf("Guardian policy blocks model %q: %v", model, resolveErr)
		}
		if live.Params != nil {
			value := reviewer
			live.Params.ApprovalsReviewer = &value
		}
		live.RunConfig.Config = next
		live.RunConfig.ApprovalsReviewer = reviewer
		live.GuardianPolicyRevision = snapshot.Revision
		live.GuardianPolicyBlocked = blockedReason != ""
		live.GuardianPolicyBlockedReason = blockedReason
		applied = true
	})
	if !applied {
		return nil
	}
	if blockedReason == "" {
		return nil
	}
	return &guardianPolicyRejection{ThreadID: threadID, Reason: blockedReason}
}

// guardianPolicyConstrainedReviewer mirrors Rust
// requirements.approvals_reviewer.value(): the constrained default after a
// managed allow-list, or the user reviewer when nothing constrains it.
func guardianPolicyConstrainedReviewer(requirements *config.ConfigRequirements) string {
	if requirements != nil && len(requirements.AllowedApprovalsReviewers) > 0 {
		return normalizeStepApprovalsReviewer(string(requirements.AllowedApprovalsReviewers[0]))
	}
	return normalizeStepApprovalsReviewer(string(config.ApprovalsReviewerUser))
}

// guardianPolicyStepCheck refuses a sampling step whose model the published
// Guardian policy blocks (Rust #62585 applies the refreshed restrictions to
// running threads). Go keeps the turn's model fixed, so a blocked turn reports
// the restriction instead of sampling under a disallowed model; a replacement
// model can be supplied by a new turn.
func (r *RuntimeRouter) guardianPolicyStepCheck(threadID string, next func() error) func() error {
	return func() error {
		if r != nil && r.threads != nil {
			if active := r.threads.ActiveTurn(threadID); active != nil && active.GuardianPolicyBlocked {
				reason := strings.TrimSpace(active.GuardianPolicyBlockedReason)
				if reason == "" {
					reason = "the current model is not allowed by the published Guardian policy"
				}
				return errors.New(reason)
			}
		}
		if next != nil {
			return next()
		}
		return nil
	}
}
