package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func catalogSettingsModelInfo(slug string) *model.ModelInfo {
	return &model.ModelInfo{
		Slug:                     slug,
		DisplayName:              slug,
		Visibility:               model.VisibilityVisible,
		SupportedInAPI:           true,
		SupportedReasoningLevels: []string{"low", "medium"},
		DefaultReasoningLevel:    "medium",
		ServiceTiers:             []string{"priority"},
	}
}

// TestNormalizeSavedReasoningEffortLikeRust pins Rust #59111: an unsupported
// ranked effort falls back to the highest supported level at or below the
// selection; ultra and persistent use the supported default; custom efforts and
// a catalog without levels stay untouched.
func TestNormalizeSavedReasoningEffortLikeRust(t *testing.T) {
	info := catalogSettingsModelInfo("gpt-narrow")
	cases := []struct {
		name   string
		info   *model.ModelInfo
		effort string
		want   string
		change bool
	}{
		{name: "supported stays", info: info, effort: "medium", want: "medium"},
		{name: "rounds down", info: info, effort: "xhigh", want: "medium", change: true},
		{name: "highest supported below", info: info, effort: "max", want: "medium", change: true},
		{name: "nothing below stays", info: info, effort: "minimal", want: "minimal"},
		{name: "ultra uses default", info: info, effort: "ultra", want: "medium", change: true},
		{name: "persistent uses default", info: info, effort: "persistent", want: "medium", change: true},
		{name: "custom stays", info: info, effort: "custom-x", want: "custom-x"},
		{name: "empty catalog stays", info: &model.ModelInfo{Slug: "gpt"}, effort: "xhigh", want: "xhigh"},
		{name: "nil info stays", info: nil, effort: "xhigh", want: "xhigh"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, changed := normalizeSavedReasoningEffort(testCase.info, testCase.effort)
			if got != testCase.want || changed != testCase.change {
				t.Fatalf("normalizeSavedReasoningEffort() = %q/%v, want %q/%v", got, changed, testCase.want, testCase.change)
			}
		})
	}
	// The unsupported ultra selection needs a supported default to fall back to.
	ultra := &model.ModelInfo{Slug: "gpt", SupportedReasoningLevels: []string{"low"}}
	if got, changed := normalizeSavedReasoningEffort(ultra, "ultra"); got != "ultra" || changed {
		t.Fatalf("ultra without default = %q/%v", got, changed)
	}
}

// TestNormalizeSavedServiceTierLikeRust pins the tier rule: only unsupported
// priority and ultrafast selections reset, and they reset to default.
func TestNormalizeSavedServiceTierLikeRust(t *testing.T) {
	info := catalogSettingsModelInfo("gpt-narrow")
	cases := []struct {
		tier   string
		want   string
		change bool
	}{
		{tier: "priority", want: "priority"},
		{tier: "ultrafast", want: "default", change: true},
		{tier: "flex", want: "flex"},
		{tier: "default", want: "default"},
	}
	for _, testCase := range cases {
		got, changed := normalizeSavedServiceTier(info, testCase.tier)
		if got != testCase.want || changed != testCase.change {
			t.Fatalf("normalizeSavedServiceTier(%q) = %q/%v, want %q/%v", testCase.tier, got, changed, testCase.want, testCase.change)
		}
	}
	if got, changed := normalizeSavedServiceTier(&model.ModelInfo{Slug: "gpt"}, "priority"); got != "default" || !changed {
		t.Fatalf("priority without catalog support = %q/%v", got, changed)
	}
}

// TestThreadSettingsUpdateNormalizesAgainstCatalogLikeRust pins the incoming
// write path: an unsupported effort and tier are corrected before they are
// committed and persisted.
func TestThreadSettingsUpdateNormalizesAgainstCatalogLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-narrow", DisplayName: "gpt-narrow", Visibility: model.VisibilityVisible, SupportedInAPI: true,
				SupportedReasoningLevels: []string{"low", "medium"}, DefaultReasoningLevel: "medium", ServiceTiers: []string{"priority"}},
		}})),
	})
	defer router.Close()
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-narrow"}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	effort := "xhigh"
	unsupported := "ultrafast"
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadSettingsUpdate, SettingsUpdateParams{
		ThreadID:    threadID,
		Effort:      &effort,
		ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &unsupported},
	}))
	if response.Error != nil {
		t.Fatalf("thread/settings/update error = %+v", response.Error)
	}
	committed := router.threadSettingsForTurn(threadID)
	if committed.Effort == nil || *committed.Effort != "medium" {
		t.Fatalf("committed effort = %v, want medium", committed.Effort)
	}
	if committed.ServiceTier == nil || *committed.ServiceTier != model.ServiceTierDefaultRequestValue {
		t.Fatalf("committed tier = %v, want default", committed.ServiceTier)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	overrides := threadRecordConfigOverrides(record)
	if overrides["model_reasoning_effort"] != "medium" || overrides["service_tier"] != "default" {
		t.Fatalf("persisted overrides = %#v", overrides)
	}
}

// TestSavedChatSettingsNormalizeAfterRefreshLikeRust pins the refreshed-catalog
// path: a saved chat keeps an incompatible effort or tier only until the
// provider catalog proves it unsupported.
func TestSavedChatSettingsNormalizeAfterRefreshLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-narrow", DisplayName: "gpt-narrow", Visibility: model.VisibilityVisible, SupportedInAPI: true,
				SupportedReasoningLevels: []string{"low", "medium"}, DefaultReasoningLevel: "medium", ServiceTiers: []string{}},
		}})),
	})
	defer router.Close()
	record := &session.Record{
		ID: "thread-refresh",
		Metadata: session.Metadata{
			CWD: t.TempDir(), Model: "gpt-narrow",
			Extra: map[string]any{"config": map[string]any{
				"model_reasoning_effort": "max",
				"service_tier":           "priority",
			}},
		},
	}
	if !router.normalizeSavedChatSettingsForRecord(record) {
		t.Fatal("saved settings were not normalized")
	}
	overrides := threadRecordConfigOverrides(record)
	if overrides["model_reasoning_effort"] != "medium" || overrides["service_tier"] != "default" {
		t.Fatalf("normalized overrides = %#v", overrides)
	}
	// A compatible selection stays untouched.
	stable := &session.Record{ID: "thread-stable", Metadata: session.Metadata{Model: "gpt-narrow", Extra: map[string]any{"config": map[string]any{
		"model_reasoning_effort": "low",
		"service_tier":           "flex",
	}}}}
	if router.normalizeSavedChatSettingsForRecord(stable) {
		t.Fatal("a compatible selection was rewritten")
	}
}
