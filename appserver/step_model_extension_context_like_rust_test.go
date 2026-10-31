package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

// Rust #44202: extension context reads the model captured for each sampling
// step, so a mid-turn model switch re-budgets the skill catalog and recomputes
// the usage instructions instead of keeping the turn's initial model metadata.
func TestStepSkillCatalogUsesCapturedModelSettingsLikeRust(t *testing.T) {
	skillsRoot := t.TempDir()
	skillDir := filepath.Join(skillsRoot, "repo-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: repo-helper\ndescription: Repo helper\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		Skills: NewSkillsService([]string{skillsRoot}),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			// The first step declares only max_context_window, which Rust's
			// resolved_context_window falls back to for the catalog budget.
			{Slug: "step-model-a", MaxContextWindow: 400000, EffectiveContextWindowPercent: 50},
			{Slug: "step-model-b", ContextWindow: 200000, IncludeSkillsUsageInstructions: true},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })

	threadID := "thread-step-model-context"
	turnID := "turn-step-model-context"
	now := time.Now().UTC()
	if err := router.threads.RegisterTurn(threadID, turnID, nil, now.UnixMilli(), &turn.TurnStartParams{ThreadID: threadID, Model: "step-model-a"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	cfg := &config.Config{Values: map[string]any{}}
	runConfig := &appTurnRunConfig{Config: cfg, Instructions: "base instructions", StepInstructionBase: "base instructions"}
	provider := router.turnStepInstructionsProvider(threadID, turnID, &turn.TurnStartParams{ThreadID: threadID}, runConfig)
	if provider == nil {
		t.Fatal("step instructions provider is nil")
	}

	first := provider()
	if !strings.Contains(first, "repo-helper") {
		t.Fatalf("first step instructions missing skill:\n%s", first)
	}
	if strings.Contains(first, "### How to use skills") {
		t.Fatalf("first step instructions carry the next model's usage guidance:\n%s", first)
	}
	if window := router.skillContextWindowForTurn(cfg, &turn.TurnStartParams{ThreadID: threadID, Model: "step-model-a"}); window != 400000 {
		t.Fatalf("step-model-a skill window = %d, want 400000 (max_context_window fallback)", window)
	}

	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{ThreadID: threadID, TurnID: turnID, Model: stringPtr("step-model-b")}) {
		t.Fatal("mid-turn model switch was not applied")
	}

	second := provider()
	if !strings.Contains(second, "repo-helper") || !strings.Contains(second, "### How to use skills") {
		t.Fatalf("second step instructions do not follow the captured model:\n%s", second)
	}
	if window := router.skillContextWindowForTurn(cfg, &turn.TurnStartParams{ThreadID: threadID, Model: "step-model-b"}); window != 200000 {
		t.Fatalf("step-model-b skill window = %d, want 200000", window)
	}
}
