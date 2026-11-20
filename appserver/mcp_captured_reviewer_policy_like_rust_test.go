package appserver

import (
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/turn"
)

// Mirrors Rust #62276: MCP tool and elicitation reviewers resolve against the
// issuing turn's policy, so a live Guardian policy change cannot waive a review
// the turn already requires.
func TestMCPToolApprovalReviewerUsesCapturedTurnPolicyLikeRust(t *testing.T) {
	const (
		threadID = "thread-captured-reviewer"
		turnID   = "turn-captured-reviewer"
		modelID  = "gpt-captured-reviewer"
	)
	home := t.TempDir()
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
	t.Cleanup(func() { _ = router.Close() })
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    modelID,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	captured := &config.Config{
		Values: map[string]any{"model": modelID, "approvals_reviewer": string(config.ApprovalsReviewerAutoReview)},
		Requirements: &config.ConfigRequirements{
			AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerAutoReview},
		},
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: modelID, Config: captured})

	handler := &appserverMCPToolApprovalHandler{router: router, threadID: threadID, turnID: turnID}
	request := &mcp.MCPToolApprovalRequest{Server: "docs", Tool: "search"}
	if got := handler.mcpToolApprovalReviewer(request); !got.RoutesToGuardian() {
		t.Fatalf("reviewer = %q, want the captured policy's guardian review", got)
	}
	if got := router.approvalsReviewerForTurn(threadID, turnID); !got.RoutesToGuardian() {
		t.Fatalf("turn reviewer = %q, want the captured policy's guardian review", got)
	}

	// The live configuration alone allows the user reviewer, so a turn without a
	// captured policy keeps prompting the user.
	const liveThreadID = "thread-live-reviewer"
	const liveTurnID = "turn-live-reviewer"
	if err := router.registerActiveRuntimeTurn(liveThreadID, liveTurnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: liveThreadID,
		Model:    modelID,
	}); err != nil {
		t.Fatalf("register live turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(liveThreadID, liveTurnID, "", &appTurnRunConfig{
		Model:  modelID,
		Config: &config.Config{Values: map[string]any{"model": modelID}},
	})
	liveHandler := &appserverMCPToolApprovalHandler{router: router, threadID: liveThreadID, turnID: liveTurnID}
	if got := liveHandler.mcpToolApprovalReviewer(request); got.RoutesToGuardian() {
		t.Fatalf("live reviewer = %q, want the user reviewer", got)
	}
}

// Mirrors Rust #62276: a Guardian policy update publishes reviewer policy
// without validating or restarting MCP, so the published runtime fingerprint is
// unchanged.
func TestGuardianPolicyUpdateDoesNotRotateMCPRuntimeLikeRust(t *testing.T) {
	policy := "managed policy"
	base := &config.Config{Values: map[string]any{"model": "gpt-5"}, Requirements: &config.ConfigRequirements{}}
	updated := &config.Config{
		Values: map[string]any{"model": "gpt-5"},
		Requirements: &config.ConfigRequirements{
			AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerAutoReview},
			GuardianPolicyConfig:      &policy,
		},
	}
	if mcpRuntimeConfigFingerprint(base) != mcpRuntimeConfigFingerprint(updated) {
		t.Fatal("a Guardian policy update rotated the published MCP runtime fingerprint")
	}
}
