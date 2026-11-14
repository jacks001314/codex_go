package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	codexctx "codex_go/context"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

const guardianElasticTestConfig = `
[features.guardianv2]
sync_elastic_budgeting = true

[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 900

[features.guardianv2.sync_elastic_sections.transcript_tools]
min_total_section_tokens = 1000
max_section_entry_tokens = 100
max_total_section_tokens = 1000
`

// TestGuardianReviewHistoryUsesConfiguredElasticStorageLikeRust covers Rust
// #59444: the capture policy is selected before history is recorded, so elastic
// storage keeps more than the fixed 128-entry bound.
func TestGuardianReviewHistoryUsesConfiguredElasticStorageLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(guardianElasticTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	const threadID = "thread-elastic-history"
	if err := router.threads.RegisterTurn(threadID, "turn-elastic", nil, 0, &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	items := make([]session.Item, 0, maxGuardianReviewItemsPerKind+12)
	for index := 0; index < maxGuardianReviewItemsPerKind+12; index++ {
		items = append(items, session.Item{
			ID: fmt.Sprintf("tool-%d", index), Type: "function_call_output",
			CallID: fmt.Sprintf("call-%d", index), Text: "output",
		})
	}
	router.captureGuardianReviewHistory(threadID, items)
	router.guardianReviewHistoryMu.Lock()
	state := router.guardianReviewHistory[threadID]
	router.guardianReviewHistoryMu.Unlock()
	if state == nil {
		t.Fatal("captured review history is nil")
	}
	if state.budgeting != codexctx.GuardianContextBudgetingElastic {
		t.Fatalf("review history budgeting = %v, want elastic", state.budgeting)
	}
	if len(state.items) != len(items) {
		t.Fatalf("elastic review history retained %d items, want %d", len(state.items), len(items))
	}
}

// TestGuardianSynchronousReviewUsesElasticBudgetingLikeRust covers Rust
// #59444's configured reviewer path: section limits shape the actual request,
// and the request still carries the omission notice and exact action.
func TestGuardianSynchronousReviewUsesElasticBudgetingLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(guardianElasticTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		threadID = "thread-elastic-review"
		turnID   = "turn-elastic-review"
		modelID  = "gpt-5.4"
	)
	catalog := []model.ModelInfo{{Slug: modelID, Visibility: "list", SupportedInAPI: true}}
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		clone := *request
		clone.InputItems = append([]any(nil), request.InputItems...)
		captured = &clone
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"elastic"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent:        agent,
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
	})
	params := &turn.TurnStartParams{ThreadID: threadID, Model: modelID}
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), params); err != nil {
		t.Fatal(err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: modelID})
	reviewer, ok := router.ensureGuardianReviewerWithPrewarm(agent, false).(*modelGuardianReviewer)
	if !ok {
		t.Fatal("Guardian reviewer has an unexpected type")
	}
	entries := make([]state.GuardianTranscriptEntry, 0, 40)
	for index := 0; index < 40; index++ {
		entries = append(entries, state.GuardianTranscriptEntry{
			Author: "tool", LineRole: "tool shell result", Optional: true,
			Text: fmt.Sprintf("%d %s", index, strings.Repeat("tool evidence ", 80)),
		})
	}
	reviewer.transcriptSelection = func(string, guardianTranscriptBudgeting) guardianTranscriptSelection {
		return guardianTranscriptSelection{Entries: entries}
	}
	// The injected selection replaces the router's request capture, which would
	// otherwise prepare the same review from the (empty) host transcript.
	reviewer.transcriptSnapshot = nil
	reviewer.syncImageContextWindow = func(string, string) (int64, bool) { return 50_000, true }

	decision, message, err := reviewer.Review(context.Background(), threadID, turnID, "call-elastic", state.Action{
		Type: "command", Command: "echo hello", CWD: "/workspace",
	})
	if err != nil {
		t.Fatalf("Guardian review error = %v", err)
	}
	if decision != state.DecisionApproved || captured == nil {
		t.Fatalf("decision = %q, message = %q, captured = %v", decision, message, captured != nil)
	}
	var requestText strings.Builder
	requestText.WriteString(captured.Prompt)
	for _, item := range captured.InputItems {
		if encoded, err := json.Marshal(item); err == nil {
			requestText.Write(encoded)
		}
	}
	text := requestText.String()
	if !strings.Contains(text, "echo hello") || !strings.Contains(text, "guardian_context_omission") {
		t.Fatalf("elastic review request lost required context:\n%s", text)
	}
	if count := strings.Count(text, "tool shell result"); count == 0 || count >= len(entries) {
		t.Fatalf("tool transcript entries = %d, want a section-bounded prefix", count)
	}
	if !strings.Contains(text, "<truncated") {
		t.Fatalf("elastic review request did not cap an oversized entry:\n%s", text)
	}
}
