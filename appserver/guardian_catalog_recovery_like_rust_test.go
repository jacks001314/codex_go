package appserver

import (
	"sync/atomic"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

// guardianCatalogTestManager reports a pending initialization and blocks its
// background fetch until the test releases it.
type guardianCatalogTestManager struct {
	*model.StaticModelsManager
	needsInit int32
	fetches   int32
	started   chan struct{}
	release   chan struct{}
	finished  chan struct{}
}

func (m *guardianCatalogTestManager) NeedsInitialization() bool {
	return atomic.LoadInt32(&m.needsInit) == 1
}

func (m *guardianCatalogTestManager) RawModelCatalog(strategy model.RefreshStrategy) model.ModelsResponse {
	atomic.AddInt32(&m.fetches, 1)
	if m.started != nil {
		select {
		case <-m.started:
		default:
			close(m.started)
		}
	}
	if m.release != nil {
		<-m.release
	}
	atomic.StoreInt32(&m.needsInit, 0)
	if m.finished != nil {
		close(m.finished)
	}
	return model.ModelsResponse{}
}

func newGuardianCatalogTestManager() *guardianCatalogTestManager {
	return &guardianCatalogTestManager{
		StaticModelsManager: model.NewStaticModelsManager(model.BundledModelsResponse()),
		needsInit:           1,
		started:             make(chan struct{}),
		release:             make(chan struct{}),
		finished:            make(chan struct{}),
	}
}

func guardianProviderRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	return NewRuntimeRouter(RuntimeServices{
		Config:                config.NewConfigService(t.TempDir()),
		GuardianModelProvider: &GuardianInferenceProvider{ID: "guardian", Info: model.ProviderInfo{EnvKey: "GUARDIAN_API_KEY"}},
	})
}

// Mirrors Rust #62627: the Guardian catalog recovery runs in the background,
// one retry at a time, without delaying the observed tool call.
func TestGuardianCatalogRecoverySchedulesBackgroundRetryLikeRust(t *testing.T) {
	manager := newGuardianCatalogTestManager()
	router := guardianProviderRouter(t)
	cfg := &config.Config{Values: map[string]any{}}
	reviewer := string(config.ApprovalsReviewerAutoReview)
	params := &turn.TurnStartParams{ThreadID: "thread-1", ApprovalsReviewer: &reviewer}

	if !router.guardianCatalogRecoveryDue(cfg, params, manager) {
		t.Fatal("an uninitialized Guardian catalog outside full access must schedule recovery")
	}
	if !router.retryGuardianCatalogInitializationInBackground(manager) {
		t.Fatal("recovery did not start")
	}
	<-manager.started
	if router.retryGuardianCatalogInitializationInBackground(manager) {
		t.Fatal("a second retry overlapped the running one")
	}
	close(manager.release)
	<-manager.finished
	if fetches := atomic.LoadInt32(&manager.fetches); fetches != 1 {
		t.Fatalf("catalog fetches = %d, want one background retry", fetches)
	}
}

// Mirrors Rust #62627's gate: recovery waits for auto review outside full
// access, a managed policy that allows Guardian scoring, and an actual
// initialization need.
func TestGuardianCatalogRecoveryGateLikeRust(t *testing.T) {
	router := guardianProviderRouter(t)
	cfg := &config.Config{Values: map[string]any{}}
	autoReview := string(config.ApprovalsReviewerAutoReview)
	userReview := string(config.ApprovalsReviewerUser)

	if router.guardianCatalogRecoveryDue(cfg, &turn.TurnStartParams{ThreadID: "t", ApprovalsReviewer: &userReview}, newGuardianCatalogTestManager()) {
		t.Fatal("a user reviewer must not schedule Guardian catalog recovery")
	}
	if router.guardianCatalogRecoveryDue(cfg, &turn.TurnStartParams{ThreadID: "t", ApprovalsReviewer: &autoReview}, nil) {
		t.Fatal("a missing catalog must not schedule recovery")
	}
	initialized := newGuardianCatalogTestManager()
	atomic.StoreInt32(&initialized.needsInit, 0)
	if router.guardianCatalogRecoveryDue(cfg, &turn.TurnStartParams{ThreadID: "t", ApprovalsReviewer: &autoReview}, initialized) {
		t.Fatal("an initialized catalog must not schedule recovery")
	}
	disabled := &config.Config{
		Values:       map[string]any{},
		Requirements: &config.ConfigRequirements{AllowedApprovalsReviewers: []config.ApprovalsReviewer{}},
	}
	if router.guardianCatalogRecoveryDue(disabled, &turn.TurnStartParams{ThreadID: "t", ApprovalsReviewer: &autoReview}, newGuardianCatalogTestManager()) {
		t.Fatal("requirements that disable Guardian scoring must not schedule recovery")
	}
	actorOnly := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	if actorOnly.guardianCatalogRecoveryDue(cfg, &turn.TurnStartParams{ThreadID: "t", ApprovalsReviewer: &autoReview}, newGuardianCatalogTestManager()) {
		t.Fatal("a router without a Guardian provider must not schedule recovery")
	}
}

// Mirrors Rust #62588: the Guardian catalog belongs to the selected provider's
// model_catalog_url and is shared between observations.
func TestGuardianModelsManagerUsesSelectedProviderCatalogLikeRust(t *testing.T) {
	t.Setenv("GUARDIAN_API_KEY", "test-key")
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(t.TempDir()),
		GuardianModelProvider: &GuardianInferenceProvider{
			ID: "guardian",
			Info: model.ProviderInfo{
				BaseURL:         "https://guardian.example/v1",
				ModelCatalogURL: "https://guardian.example/v1/models",
				EnvKey:          "GUARDIAN_API_KEY",
			},
		},
	})
	cfg := &config.Config{Values: map[string]any{}}
	manager := router.guardianModelsManager(cfg)
	if manager == nil {
		t.Fatal("guardianModelsManager() returned no catalog")
	}
	if !manager.NeedsInitialization() {
		t.Fatal("a fresh provider catalog must need initialization")
	}
	if again := router.guardianModelsManager(cfg); again != manager {
		t.Fatal("the Guardian catalog must be shared across observations")
	}
	if actorManager := router.requireModels().Manager(); actorManager == manager {
		t.Fatal("Guardian must not reuse the actor's catalog")
	}
}
