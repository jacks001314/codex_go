package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/internal/appserver"
	"codex_go/internal/model"
)

func TestSkillLoadWarningStateSuppressesRepeatedActiveErrorsMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid description")

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("first NewlyActiveErrors() = %#v, want error", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); len(got) != 0 {
		t.Fatalf("repeated NewlyActiveErrors() = %#v, want empty", got)
	}
}

func TestSkillLoadWarningStateReemitsAfterErrorClearsMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid description")

	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	if got := state.NewlyActiveErrors(nil); len(got) != 0 {
		t.Fatalf("cleared NewlyActiveErrors() = %#v, want empty", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("reemitted NewlyActiveErrors() = %#v, want error", got)
	}
}

func TestSkillLoadWarningStateDisplaysNewMessageForActivePathMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	initial := startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid description")
	changed := startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid frontmatter")

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{initial}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{initial}) {
		t.Fatalf("initial NewlyActiveErrors() = %#v, want initial", got)
	}
	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{changed}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{changed}) {
		t.Fatalf("changed NewlyActiveErrors() = %#v, want changed", got)
	}
}

func TestSkillLoadWarningStateClearAllowsActiveErrorAgainMatchRust(t *testing.T) {
	state := NewSkillLoadWarningState()
	skillError := startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid description")

	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError})
	state.Clear()

	if got := state.NewlyActiveErrors([]appserver.SkillErrorInfo{skillError}); !reflect.DeepEqual(got, []appserver.SkillErrorInfo{skillError}) {
		t.Fatalf("after clear NewlyActiveErrors() = %#v, want error", got)
	}
}

func TestSkillLoadWarningMessagesMatchRust(t *testing.T) {
	errors := []appserver.SkillErrorInfo{
		startupSkillError("/repo/.codex/skills/abc/SKILL.md", "invalid description"),
		startupSkillError("/repo/.codex/skills/xyz/SKILL.md", "missing name"),
	}
	want := []string{
		"Skipped loading 2 skill(s) due to invalid SKILL.md files.",
		"/repo/.codex/skills/abc/SKILL.md: invalid description",
		"/repo/.codex/skills/xyz/SKILL.md: missing name",
	}
	if got := SkillLoadWarningMessages(errors); !reflect.DeepEqual(got, want) {
		t.Fatalf("SkillLoadWarningMessages() = %#v, want %#v", got, want)
	}
	if got := SkillLoadWarningMessages(nil); got != nil {
		t.Fatalf("SkillLoadWarningMessages(nil) = %#v, want nil", got)
	}
}

func TestSelectModelAvailabilityNUXMatchRust(t *testing.T) {
	first := model.ModelSummary{
		ID:              "gpt-5",
		Model:           "gpt-5",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "new model"},
	}
	second := model.ModelSummary{
		ID:              "gpt-5.1",
		Model:           "gpt-5.1",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "newer model"},
	}

	got, ok := SelectModelAvailabilityNUX([]model.ModelSummary{first, second}, map[string]uint32{"gpt-5": ModelAvailabilityNUXMaxShowCount})
	if !ok {
		t.Fatal("SelectModelAvailabilityNUX() ok = false, want true")
	}
	if got != (StartupTooltipOverride{ModelSlug: "gpt-5.1", Message: "newer model"}) {
		t.Fatalf("SelectModelAvailabilityNUX() = %#v", got)
	}

	if _, ok := SelectModelAvailabilityNUX([]model.ModelSummary{first}, map[string]uint32{"gpt-5": ModelAvailabilityNUXMaxShowCount}); ok {
		t.Fatal("SelectModelAvailabilityNUX(max shown) ok = true, want false")
	}
	if _, ok := SelectModelAvailabilityNUX([]model.ModelSummary{{ID: "gpt-5", Model: "gpt-5"}}, nil); ok {
		t.Fatal("SelectModelAvailabilityNUX(no nux) ok = true, want false")
	}
}

func TestSelectModelAvailabilityNUXFallsBackToID(t *testing.T) {
	got, ok := SelectModelAvailabilityNUX([]model.ModelSummary{{
		ID:              "gpt-id",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "message"},
	}}, nil)
	if !ok {
		t.Fatal("SelectModelAvailabilityNUX() ok = false, want true")
	}
	if got.ModelSlug != "gpt-id" || got.Message != "message" {
		t.Fatalf("SelectModelAvailabilityNUX() = %#v", got)
	}
}

func TestPrepareStartupTooltipOverrideDecisionMatchRust(t *testing.T) {
	models := []model.ModelSummary{{
		ID:              "gpt-5.1",
		Model:           "gpt-5.1",
		AvailabilityNux: &model.ModelAvailabilityNux{Message: "new"},
	}}
	if decision := PrepareStartupTooltipOverrideDecision(true, true, models, nil); decision.Show {
		t.Fatalf("first run decision = %#v", decision)
	}
	if decision := PrepareStartupTooltipOverrideDecision(false, false, models, nil); decision.Show {
		t.Fatalf("tooltips disabled decision = %#v", decision)
	}
	decision := PrepareStartupTooltipOverrideDecision(false, true, models, map[string]uint32{"gpt-5.1": 2})
	if !decision.Show || decision.Override.ModelSlug != "gpt-5.1" || decision.UpdatedShownCount["gpt-5.1"] != 3 {
		t.Fatalf("tooltip decision = %#v", decision)
	}
}

func TestModelMigrationPromptDecisionMatchRust(t *testing.T) {
	target := "gpt-5.1"
	models := []model.ModelSummary{
		{ID: "gpt-5", Model: "gpt-5", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1", DefaultReasoningEffort: "high"},
	}
	if !ShouldShowModelMigrationPrompt("gpt-5", "gpt-5.1", nil, models) {
		t.Fatal("expected migration prompt")
	}
	if ShouldShowModelMigrationPrompt("gpt-5.1", "gpt-5.1", nil, models) {
		t.Fatal("same model should not prompt")
	}
	if ShouldShowModelMigrationPrompt("gpt-5", "gpt-5.1", map[string]string{"gpt-5": "gpt-5.1"}, models) {
		t.Fatal("seen migration should not prompt")
	}
	hiddenTarget := []model.ModelSummary{
		{ID: "gpt-5", Model: "gpt-5", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1", Hidden: true},
	}
	if ShouldShowModelMigrationPrompt("gpt-5", "gpt-5.1", nil, hiddenTarget) {
		t.Fatal("hidden target should not prompt")
	}
	viaOtherPreset := []model.ModelSummary{
		{ID: "legacy", Model: "legacy"},
		{ID: "other", Model: "other", Upgrade: &target},
		{ID: "gpt-5.1", Model: "gpt-5.1"},
	}
	if !ShouldShowModelMigrationPrompt("legacy", "gpt-5.1", nil, viaOtherPreset) {
		t.Fatal("upgrade target referenced by any preset should prompt")
	}
}

func TestMigrationPromptHiddenTargetPresetAndAcceptedActionsMatchRust(t *testing.T) {
	if !MigrationPromptHidden(map[string]bool{HideGPT51MigrationPromptConfig: true}, HideGPT51MigrationPromptConfig) {
		t.Fatal("migration prompt hidden key should hide")
	}
	if MigrationPromptHidden(map[string]bool{"unknown": true}, "unknown") {
		t.Fatal("unknown hidden key should not hide")
	}

	target, ok := TargetPresetForUpgrade([]model.ModelSummary{
		{ID: "hidden", Model: "gpt-hidden", Hidden: true},
		{ID: "target", Model: "gpt-target", DefaultReasoningEffort: "medium"},
	}, "gpt-target")
	if !ok || target.ID != "target" {
		t.Fatalf("target preset = %#v ok=%v", target, ok)
	}
	if _, ok := TargetPresetForUpgrade([]model.ModelSummary{{ID: "hidden", Model: "gpt-hidden", Hidden: true}}, "gpt-hidden"); ok {
		t.Fatal("hidden target should not be selected")
	}

	actions := ApplyAcceptedModelMigrationActions("gpt-old", target)
	if actions.FromModel != "gpt-old" || actions.TargetModel != "gpt-target" || actions.TargetReasoningEffort != "medium" || !actions.PersistAcknowledgement || !actions.UpdateModel || !actions.UpdateReasoningEffort || !actions.PersistModelSelection {
		t.Fatalf("actions = %#v", actions)
	}
}

func TestProjectConfigWarningAndHarnessOverrideNormalizationMatchRust(t *testing.T) {
	warnings := BuildProjectConfigWarningMessages([]ProjectConfigDisabledFolder{
		{Folder: "/repo/.codex", Reason: "not trusted"},
		{Folder: "/repo/sub/.codex", Reason: "owner mismatch"},
	})
	want := []string{"Project-local config, hooks, and exec policies are disabled in the following folders until the project is trusted, but skills still load.\n    1. /repo/.codex\n       not trusted\n    2. /repo/sub/.codex\n       owner mismatch"}
	if !reflect.DeepEqual(warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", warnings, want)
	}
	if got := BuildProjectConfigWarningMessages(nil); got != nil {
		t.Fatalf("empty warnings = %#v, want nil", got)
	}

	base := t.TempDir()
	normalized := NormalizeAdditionalWritableRootsForCWD([]string{"rel", "", filepath.Join(base, "abs")}, base)
	if len(normalized) != 2 || normalized[0] != filepath.Clean(filepath.Join(base, "rel")) {
		t.Fatalf("normalized = %#v", normalized)
	}
}

func startupSkillError(path string, message string) appserver.SkillErrorInfo {
	return appserver.SkillErrorInfo{Path: path, Message: message}
}
