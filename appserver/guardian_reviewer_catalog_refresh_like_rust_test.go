package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

// guardianCatalogManager is a catalog manager whose online refresh can be
// observed, blocked, and made to recover the default reviewer.
type guardianCatalogManager struct {
	mu        sync.Mutex
	models    []model.ModelInfo
	refreshes int
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func newGuardianCatalogManager(models ...model.ModelInfo) *guardianCatalogManager {
	return &guardianCatalogManager{models: append([]model.ModelInfo(nil), models...)}
}

func (m *guardianCatalogManager) ListModels(strategy model.RefreshStrategy) []model.ModelPreset {
	if strategy == model.RefreshOnline {
		m.mu.Lock()
		m.refreshes++
		m.mu.Unlock()
		if m.started != nil {
			m.startOnce.Do(func() { close(m.started) })
		}
		if m.release != nil {
			<-m.release
		}
		m.mu.Lock()
		m.models = append(m.models, model.ModelInfo{
			Slug:                     model.DefaultApprovalReviewPreferredModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		})
		m.mu.Unlock()
	}
	return m.presets()
}

func (m *guardianCatalogManager) presets() []model.ModelPreset {
	m.mu.Lock()
	models := append([]model.ModelInfo(nil), m.models...)
	m.mu.Unlock()
	return model.BuildAvailableModels(models)
}

func (m *guardianCatalogManager) refreshCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshes
}

func (m *guardianCatalogManager) RawModelCatalog(model.RefreshStrategy) model.ModelsResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	return model.ModelsResponse{Models: append([]model.ModelInfo(nil), m.models...)}
}

// ModelCatalogWithOrigins reports the reviewer recovered by an online refresh as
// a provider entry and everything else as bundled (Rust #57770).
func (m *guardianCatalogManager) ModelCatalogWithOrigins(strategy model.RefreshStrategy) []model.CatalogModel {
	promote := strategy == model.RefreshOnline
	_ = promote
	m.mu.Lock()
	models := append([]model.ModelInfo(nil), m.models...)
	m.mu.Unlock()
	catalog := make([]model.CatalogModel, 0, len(models))
	for _, info := range models {
		origin := model.ModelOriginBundled
		if info.Slug == model.DefaultApprovalReviewPreferredModel {
			origin = model.ModelOriginProvider
		}
		catalog = append(catalog, model.CatalogModel{Model: info, Origin: origin})
	}
	return catalog
}

func (m *guardianCatalogManager) GetRemoteModels() []model.ModelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.ModelInfo(nil), m.models...)
}

func (m *guardianCatalogManager) GetDefaultModel(slug string, _ bool, _ model.RefreshStrategy) string {
	return slug
}

func (m *guardianCatalogManager) GetModelInfo(slug string, _ *model.ModelsManagerConfig) model.ModelInfo {
	for _, info := range m.GetRemoteModels() {
		if info.Slug == slug {
			return info
		}
	}
	return model.ModelInfo{Slug: slug}
}

func (m *guardianCatalogManager) GetCatalogModel(slug string, _ *model.ModelsManagerConfig) model.CatalogModel {
	origin := model.ModelOriginSynthetic
	for _, info := range m.GetRemoteModels() {
		if info.Slug == slug {
			origin = model.ModelOriginBundled
			break
		}
	}
	return model.CatalogModel{Model: m.GetModelInfo(slug, nil), Origin: origin}
}

func (m *guardianCatalogManager) RefreshIfNewETag(string) {}
func (m *guardianCatalogManager) RefreshAfterAuthChange() {}
func (m *guardianCatalogManager) CatalogIdentity() string { return "guardian-catalog-test" }

const guardianCatalogParentModel = "gpt-parent"

func newGuardianCatalogRouter(manager model.ModelsManager) *RuntimeRouter {
	return NewRuntimeRouter(RuntimeServices{
		Models:       model.NewModelService(manager),
		ThreadStatus: NewThreadStatusManager(),
	})
}

func registerGuardianCatalogTurn(t *testing.T, router *RuntimeRouter, threadID, turnID string) {
	t.Helper()
	longCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    guardianCatalogParentModel,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	_ = longCtx
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: guardianCatalogParentModel})
}

func guardianCatalogParentModelInfo() model.ModelInfo {
	return model.ModelInfo{
		Slug:                     guardianCatalogParentModel,
		Visibility:               "list",
		SupportedInAPI:           true,
		DefaultReasoningLevel:    "high",
		SupportedReasoningLevels: []string{"low", "medium", "high"},
	}
}

// TestGuardianReviewerRefreshesCatalogOnceLikeRust covers Rust #57789: a missing
// default reviewer triggers exactly one shared catalog refresh per parent
// session, and the recovered reviewer is used afterwards.
func TestGuardianReviewerRefreshesCatalogOnceLikeRust(t *testing.T) {
	manager := newGuardianCatalogManager(guardianCatalogParentModelInfo())
	router := newGuardianCatalogRouter(manager)
	const threadID, turnID = "thread-catalog-refresh", "turn-catalog-refresh"
	registerGuardianCatalogTurn(t, router, threadID, turnID)

	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if plan.Selection.Model != model.DefaultApprovalReviewPreferredModel {
		t.Fatalf("selection = %#v, want the recovered default reviewer", plan.Selection)
	}
	if !plan.Selection.CatalogContainsAutoReview {
		t.Fatalf("selection = %#v, want the refreshed catalog to list the reviewer", plan.Selection)
	}
	if got := manager.refreshCount(); got != 1 {
		t.Fatalf("refresh count = %d, want exactly one shared refresh", got)
	}

	if again := router.guardianReviewPlanForTurn(threadID, turnID); again.Selection.Model != model.DefaultApprovalReviewPreferredModel {
		t.Fatalf("second selection = %#v", again.Selection)
	}
	if got := manager.refreshCount(); got != 1 {
		t.Fatalf("refresh count after the second lookup = %d, want 1", got)
	}
}

// TestGuardianReviewerCatalogRefreshSurvivesCancelledWaiterLikeRust covers the
// shared-attempt rule: a waiter cancelled mid-refresh does not abort the fetch,
// and the next review observes the recovered reviewer without refreshing again.
func TestGuardianReviewerCatalogRefreshSurvivesCancelledWaiterLikeRust(t *testing.T) {
	manager := newGuardianCatalogManager(guardianCatalogParentModelInfo())
	manager.started = make(chan struct{})
	manager.release = make(chan struct{})
	router := newGuardianCatalogRouter(manager)
	const threadID, turnID = "thread-catalog-cancel", "turn-catalog-cancel"
	registerGuardianCatalogTurn(t, router, threadID, turnID)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan guardianReviewPlan, 1)
	go func() { done <- router.guardianReviewPlanForTurnContext(ctx, threadID, turnID) }()
	select {
	case <-manager.started:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog refresh did not start")
	}
	cancel()
	select {
	case plan := <-done:
		if plan.Selection.Model != guardianCatalogParentModel {
			t.Fatalf("cancelled waiter selection = %#v, want the parent fallback", plan.Selection)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	close(manager.release)

	deadline := time.Now().Add(2 * time.Second)
	for !router.guardianCatalogRefreshCompleted(threadID) {
		if time.Now().After(deadline) {
			t.Fatal("refresh did not complete after the waiter was cancelled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if plan.Selection.Model != model.DefaultApprovalReviewPreferredModel {
		t.Fatalf("selection after recovery = %#v, want the recovered reviewer", plan.Selection)
	}
	if got := manager.refreshCount(); got != 1 {
		t.Fatalf("refresh count = %d, want one shared attempt", got)
	}
}

// TestGuardianReviewerSkipsCatalogRefreshForOverridesLikeRust covers the guards:
// an explicit reviewer override, a custom catalog, and a Guardian session never
// refresh the catalog.
func TestGuardianReviewerSkipsCatalogRefreshForOverridesLikeRust(t *testing.T) {
	preferred := model.DefaultApprovalReviewPreferredModel
	cases := map[string]struct {
		selection  model.ApprovalReviewModel
		customJSON bool
		guardian   bool
		want       bool
	}{
		"missing default reviewer": {
			selection: model.ApprovalReviewModel{DefaultReviewModelID: preferred},
			want:      true,
		},
		"reviewer override": {
			selection: model.ApprovalReviewModel{DefaultReviewModelID: preferred, ModelOverridden: true, ModelOverride: "gpt-review"},
			want:      false,
		},
		"catalog already lists reviewer": {
			selection: model.ApprovalReviewModel{DefaultReviewModelID: preferred, CatalogContainsAutoReview: true},
			want:      false,
		},
		"custom model catalog": {
			selection:  model.ApprovalReviewModel{DefaultReviewModelID: preferred},
			customJSON: true,
			want:       false,
		},
		"guardian session": {
			selection: model.ApprovalReviewModel{DefaultReviewModelID: preferred},
			guardian:  true,
			want:      false,
		},
	}
	for name, testCase := range cases {
		if got := guardianReviewModelNeedsCatalogRefresh(testCase.selection, testCase.customJSON, nil, testCase.guardian); got != testCase.want {
			t.Fatalf("%s: needs refresh = %v, want %v", name, got, testCase.want)
		}
	}
	provider := &model.ProviderInfo{Name: model.OpenAIProviderName, ModelCatalogURL: "https://example.test/models"}
	if guardianReviewModelNeedsCatalogRefresh(model.ApprovalReviewModel{DefaultReviewModelID: preferred}, false, provider, false) {
		t.Fatal("a provider-supplied catalog must not be refreshed for the reviewer")
	}
}

// TestGuardianReviewerRetainsFallbackWhenRefreshFailsLikeRust pins the failure
// path: a refresh that recovers nothing keeps the parent fallback and is not
// retried for that parent session.
func TestGuardianReviewerRetainsFallbackWhenRefreshFailsLikeRust(t *testing.T) {
	failing := &failingGuardianCatalogManager{guardianCatalogManager: newGuardianCatalogManager(guardianCatalogParentModelInfo())}
	router := newGuardianCatalogRouter(failing)
	const threadID, turnID = "thread-catalog-failure", "turn-catalog-failure"
	registerGuardianCatalogTurn(t, router, threadID, turnID)

	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if plan.Selection.Model != guardianCatalogParentModel {
		t.Fatalf("selection = %#v, want the parent fallback", plan.Selection)
	}
	_ = router.guardianReviewPlanForTurn(threadID, turnID)
	if got := failing.refreshCount(); got != 1 {
		t.Fatalf("refresh count = %d, want one attempt even after a failed fetch", got)
	}
}

type failingGuardianCatalogManager struct {
	*guardianCatalogManager
}

func (m *failingGuardianCatalogManager) ListModels(strategy model.RefreshStrategy) []model.ModelPreset {
	if strategy == model.RefreshOnline {
		m.mu.Lock()
		m.refreshes++
		m.mu.Unlock()
		return m.presets()
	}
	return m.guardianCatalogManager.ListModels(strategy)
}
