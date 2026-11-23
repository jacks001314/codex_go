package appserver

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func guardianPolicyTestSnapshot(revision int64, policy string, reviewers []config.ApprovalsReviewer) *config.GuardianPolicySnapshot {
	return &config.GuardianPolicySnapshot{
		Revision: revision,
		Requirements: &config.ConfigRequirements{
			GuardianPolicyConfig:      &policy,
			AllowedApprovalsReviewers: reviewers,
		},
	}
}

// Mirrors Rust #62496: a refresh pauses thread registration while it loads and
// publishes policy, and releases the pause once publication is done.
func TestGuardianPolicyRefreshPausesRegistrationLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	loader := guardianPolicyTestLoader(func(revision int64) (*config.GuardianPolicySnapshot, error) {
		once.Do(func() { close(entered) })
		<-unblock
		return guardianPolicyTestSnapshot(revision, "managed policy", nil), nil
	})

	done := make(chan error, 1)
	go func() {
		done <- manager.RefreshGuardianPolicy(loader, nil)
	}()
	<-entered

	registered := make(chan struct{})
	go func() {
		release := manager.BeginGuardianPolicyRegistration()
		release()
		close(registered)
	}()
	select {
	case <-registered:
		t.Fatal("registration proceeded while a Guardian policy refresh was still loading")
	case <-time.After(50 * time.Millisecond):
	}

	close(unblock)
	if err := <-done; err != nil {
		t.Fatalf("RefreshGuardianPolicy() error = %v", err)
	}
	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("registration stayed paused after the Guardian policy was published")
	}
	if revision := manager.PublishedGuardianPolicyRevision(); revision == 0 {
		t.Fatal("refresh did not publish a revision")
	}
}

// Mirrors Rust #62496: a thread starting after a publication registers with the
// published policy instead of the older managed value its configuration load
// resolved.
func TestGuardianPolicyStartupUsesPublishedPolicyLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	loader := guardianPolicyTestLoader(func(revision int64) (*config.GuardianPolicySnapshot, error) {
		return guardianPolicyTestSnapshot(revision, "first policy", nil), nil
	})
	if err := manager.RefreshGuardianPolicy(loader, nil); err != nil {
		t.Fatalf("RefreshGuardianPolicy() error = %v", err)
	}

	cfg := &config.Config{Values: map[string]any{}}
	if err := manager.PrepareGuardianPolicyForStartup(cfg, loader); err != nil {
		t.Fatalf("PrepareGuardianPolicyForStartup() error = %v", err)
	}
	if policy, ok := cfg.GuardianPolicyConfig(); !ok || policy != "first policy" {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want the published policy", policy, ok)
	}
	if cfg.GuardianPolicyRevision() != manager.PublishedGuardianPolicyRevision() {
		t.Fatalf("configuration revision = %d, want published %d", cfg.GuardianPolicyRevision(), manager.PublishedGuardianPolicyRevision())
	}
}

// Mirrors Rust #62496: when publication overtakes a thread's startup load, the
// thread registers with the published policy rather than its superseded
// observation.
func TestGuardianPolicyStartupPrefersPublicationOverOvertakenLoadLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	slowLoader := guardianPolicyTestLoader(func(revision int64) (*config.GuardianPolicySnapshot, error) {
		once.Do(func() { close(entered) })
		<-unblock
		return guardianPolicyTestSnapshot(revision, "superseded policy", nil), nil
	})
	publishedLoader := guardianPolicyTestLoader(func(revision int64) (*config.GuardianPolicySnapshot, error) {
		return guardianPolicyTestSnapshot(revision, "published policy", nil), nil
	})

	startupDone := make(chan error, 1)
	cfg := &config.Config{Values: map[string]any{}}
	go func() {
		startupDone <- manager.PrepareGuardianPolicyForStartup(cfg, slowLoader)
	}()
	<-entered

	if err := manager.RefreshGuardianPolicy(publishedLoader, nil); err != nil {
		t.Fatalf("RefreshGuardianPolicy() error = %v", err)
	}
	close(unblock)
	if err := <-startupDone; err != nil {
		t.Fatalf("PrepareGuardianPolicyForStartup() error = %v", err)
	}
	if policy, ok := cfg.GuardianPolicyConfig(); !ok || policy != "published policy" {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want the publication that overtook the startup load", policy, ok)
	}
}

// Mirrors Rust #62585: refreshed reviewer and model restrictions replace the
// captured turn configuration's Guardian values, keeping the rest of the turn.
func TestGuardianPolicyRefreshUpdatesLiveTurnLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	now := time.Now()
	if err := manager.RegisterTurn("thread-1", "turn-1", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	captured := &config.Config{Values: map[string]any{"model": "gpt-5.4", "guardian_policy_config": nil}}
	manager.UpdateTurn("thread-1", "turn-1", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{Model: "gpt-5.4", ApprovalsReviewer: string(config.ApprovalsReviewerUser), Config: captured}
	})

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	router.threads = manager
	router.active = manager.active
	router.turnsMu = &manager.turnsMu
	snapshot := guardianPolicyTestSnapshot(7, "refreshed policy", []config.ApprovalsReviewer{config.ApprovalsReviewerAutoReview})
	rejection := router.applyGuardianPolicyToThread("thread-1", snapshot)
	if rejection != nil {
		t.Fatalf("applyGuardianPolicyToThread() rejection = %v, want none", rejection)
	}
	active := manager.ActiveTurn("thread-1")
	if active == nil || active.RunConfig == nil || active.RunConfig.Config == nil {
		t.Fatal("live turn lost its captured configuration")
	}
	if policy, ok := active.RunConfig.Config.GuardianPolicyConfig(); !ok || policy != "refreshed policy" {
		t.Fatalf("captured policy = %q, %v, want the refreshed policy", policy, ok)
	}
	if active.RunConfig.ApprovalsReviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("reviewer = %q, want the constrained auto_review default", active.RunConfig.ApprovalsReviewer)
	}
	if active.GuardianPolicyRevision != 7 {
		t.Fatalf("turn policy revision = %d, want 7", active.GuardianPolicyRevision)
	}
	if active.GuardianPolicyBlocked {
		t.Fatal("a compliant reviewer must not block the turn")
	}
	if model := active.RunConfig.Model; model != "gpt-5.4" {
		t.Fatalf("turn model = %q, want the captured model to stay", model)
	}
}

// Mirrors Rust #62585: a policy that rules out the current model is still
// published, and the live thread rejects the update so the refresh reports it.
func TestGuardianPolicyRefreshBlocksCurrentModelLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	if err := manager.RegisterTurn("thread-2", "turn-2", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-2"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	manager.UpdateTurn("thread-2", "turn-2", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{
			Model:             "gpt-5.4",
			ApprovalsReviewer: string(config.ApprovalsReviewerUser),
			Config:            &config.Config{Values: map[string]any{}},
		}
	})
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	router.threads = manager
	router.active = manager.active
	router.turnsMu = &manager.turnsMu

	policy := "blocked policy"
	snapshot := &config.GuardianPolicySnapshot{
		Revision: 9,
		Requirements: &config.ConfigRequirements{
			GuardianPolicyConfig:      &policy,
			AutoReview:                &config.AutoReviewRequirements{RequiredOnModels: []string{"gpt-5.4"}},
			AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
		},
	}
	rejection := router.applyGuardianPolicyToThread("thread-2", snapshot)
	if rejection == nil || !strings.Contains(rejection.Reason, "gpt-5.4") {
		t.Fatalf("rejection = %v, want the blocked model reported", rejection)
	}
	active := manager.ActiveTurn("thread-2")
	if !active.GuardianPolicyBlocked {
		t.Fatal("live turn was not marked blocked by the published policy")
	}
	if policy, ok := active.RunConfig.Config.GuardianPolicyConfig(); !ok || policy != "blocked policy" {
		t.Fatalf("captured policy = %q, %v, want the blocking policy published anyway", policy, ok)
	}
	if err := router.guardianPolicyStepCheck("thread-2", nil)(); err == nil || !strings.Contains(err.Error(), "gpt-5.4") {
		t.Fatalf("step check error = %v, want the Guardian policy block", err)
	}
}

// Mirrors Rust #62585: an invalid managed Guardian configuration is rejected by
// the live thread and surfaces as a refresh error.
func TestGuardianPolicyRefreshRejectsInvalidConfigurationLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	if err := manager.RegisterTurn("thread-3", "turn-3", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-3"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	manager.UpdateTurn("thread-3", "turn-3", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{Model: "gpt-5.4", Config: &config.Config{Values: map[string]any{}}}
	})
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	router.threads = manager
	router.active = manager.active
	router.turnsMu = &manager.turnsMu

	snapshot := &config.GuardianPolicySnapshot{
		Revision: 11,
		ManagedConfig: []config.GuardianFallbackLayer{
			{Source: config.LayerSource{Type: config.LayerSourceEnterpriseManaged, Name: "broken"}, Value: 1},
		},
	}
	rejection := router.applyGuardianPolicyToThread("thread-3", snapshot)
	if rejection == nil || !strings.Contains(rejection.Reason, "auto_review") {
		t.Fatalf("rejection = %v, want the invalid Guardian configuration reported", rejection)
	}
}

// Mirrors Rust #62614: a user config reload refreshes Guardian policy even
// though the reload itself only published the local layers.
func TestReloadUserConfigRefreshesGuardianPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", filepath.Join(home, "missing-managed.toml"))
	bundle := &config.CloudConfigBundle{
		RequirementsTOML: config.CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []config.CloudConfigFragment{
			{ID: "policy", Name: "policy", Contents: "guardian_policy_config = 'reload policy'"},
		}},
	}
	policy := config.NewCloudConfigBundlePolicy()
	policy.ObserveRemoteBundle(bundle)
	published := config.CloudConfigBundleSnapshot{Bundle: bundle}
	policy.PublishSnapshot(&published)
	loader := config.NewCloudConfigLoader(func() (*config.CloudConfigBundle, error) { return bundle, nil }).
		WithEMAPolicySnapshots(policy, func() config.CloudConfigBundleSnapshot { return published })

	service := config.NewConfigService(home)
	service.SetCloudConfigLoader(loader)
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: service})
	if revision := router.threads.PublishedGuardianPolicyRevision(); revision != 0 {
		t.Fatalf("published revision before reload = %d, want none", revision)
	}

	router.reloadUserConfigForLoadedThreads()

	if revision := router.threads.PublishedGuardianPolicyRevision(); revision == 0 {
		t.Fatal("config reload did not refresh and publish Guardian policy")
	}
	requirements := service.Requirements()
	if requirements == nil || requirements.Requirements == nil || requirements.Requirements.GuardianPolicyConfig == nil {
		t.Fatal("configRequirements/read lost the refreshed Guardian policy")
	}
	if value := strings.TrimSpace(*requirements.Requirements.GuardianPolicyConfig); value != "reload policy" {
		t.Fatalf("published Guardian policy = %q, want the reloaded policy", value)
	}
}

type guardianPolicyTestLoaderFunc func(revision int64) (*config.GuardianPolicySnapshot, error)

func (f guardianPolicyTestLoaderFunc) LoadGuardianPolicySnapshot(revision int64) (*config.GuardianPolicySnapshot, error) {
	return f(revision)
}

func guardianPolicyTestLoader(fn guardianPolicyTestLoaderFunc) GuardianPolicyLoader {
	return fn
}
