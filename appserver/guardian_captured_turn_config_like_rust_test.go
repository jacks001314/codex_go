package appserver

import (
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

// Rust #59642: Guardian policy text, reviewer requirements and model
// requirements come from the requesting turn's captured configuration even
// after the live thread configuration changed.
func TestGuardianUsesCapturedTurnConfigurationLikeRust(t *testing.T) {
	const (
		threadID = "thread-guardian-captured-config"
		turnID   = "turn-guardian-captured-config"
		modelID  = "gpt-guardian-captured"
	)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[auto_review]\npolicy = \"LIVE POLICY\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
			Slug:                     modelID,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		}}})),
		ThreadStatus: NewThreadStatusManager(),
	})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    modelID,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	captured := &config.Config{
		Values: map[string]any{
			"model":       modelID,
			"auto_review": map[string]any{"policy": "CAPTURED POLICY"},
		},
		Requirements: &config.ConfigRequirements{
			AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
		},
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model:  modelID,
		Config: captured,
	})

	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if !strings.Contains(plan.Instructions, "CAPTURED POLICY") {
		t.Fatalf("review instructions did not use the captured policy: %q", plan.Instructions)
	}
	if strings.Contains(plan.Instructions, "LIVE POLICY") {
		t.Fatalf("review instructions used the live policy: %q", plan.Instructions)
	}
	// The captured allowlist forbids auto_review, so scored and cached
	// approvals are disabled even though the live config allows them.
	if !router.guardianReviewRequiredForTurn(threadID, turnID) {
		t.Fatal("captured reviewer allowlist did not force a fresh review")
	}
}

// A live-only config keeps the previous behavior: an unrestricted allowlist
// does not force a fresh review.
func TestGuardianLiveAllowlistDoesNotForceReviewLikeRust(t *testing.T) {
	const (
		threadID = "thread-guardian-live-config"
		turnID   = "turn-guardian-live-config"
		modelID  = "gpt-guardian-live"
	)
	router := NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(t.TempDir()),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
			Slug:                     modelID,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		}}})),
		ThreadStatus: NewThreadStatusManager(),
	})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    modelID,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model: modelID,
		Config: &config.Config{
			Values: map[string]any{"model": modelID},
		},
	})
	if router.guardianReviewRequiredForTurn(threadID, turnID) {
		t.Fatal("unrestricted captured config forced a fresh review")
	}
}
