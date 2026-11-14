package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// Mirrors Rust #61185's fixed root view cache: one captured snapshot serves
// every request-preparation pass, so the fixed view is prepared once and the
// elastic allocation still reads the captured originals.
func TestGuardianReviewTranscriptSnapshotCachesFixedViewLikeRust(t *testing.T) {
	profile := state.SynchronousContextProfile()
	profile.Transcript.EntryLimits.ToolTokens = 16
	original := strings.Repeat("tool evidence ", 120)
	collected := []guardianCollectedTranscriptEntry{{
		entry:    state.GuardianTranscriptEntry{Author: "tool", LineRole: "tool shell result", Text: original},
		evidence: state.TranscriptEvidenceEntry{Kind: state.GuardianTranscriptTool, Role: "tool shell result", Text: original},
		capKind:  guardianTranscriptCapTool,
	}}
	snapshot := &guardianReviewTranscriptSnapshot{profile: profile, collected: collected}

	fixed := snapshot.selection(guardianTranscriptBudgetingFixed)
	if len(fixed.Entries) != 1 {
		t.Fatalf("fixed selection entries = %d, want 1", len(fixed.Entries))
	}
	if len(fixed.Entries[0].Text) >= len(original) {
		t.Fatalf("fixed selection kept the uncapped tool text: %q", fixed.Entries[0].Text)
	}
	elastic := snapshot.selection(guardianTranscriptBudgetingElastic)
	if len(elastic.Entries) != 1 || elastic.Entries[0].Text != original {
		t.Fatalf("elastic selection did not keep the captured original: %#v", elastic.Entries)
	}
	// The fixed view is prepared once and reused: rewriting the capture after
	// preparation cannot reach the already-prepared selection.
	collected[0].entry.Text = "rewritten after preparation"
	collected[0].evidence.Text = "rewritten after preparation"
	if again := snapshot.selection(guardianTranscriptBudgetingFixed); !reflect.DeepEqual(again, fixed) {
		t.Fatalf("fixed selection was prepared again instead of reusing the cached view")
	}
}

// Mirrors Rust #61185 at the reviewer boundary: one review captures the host
// transcript once, and both the fixed and the elastic request preparation read
// that capture instead of re-collecting the host between passes.
func TestGuardianReviewCapturesTranscriptOncePerRequestLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(guardianElasticTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		threadID = "thread-snapshot-review"
		turnID   = "turn-snapshot-review"
		modelID  = "gpt-5.4"
	)
	now := fixedTime()
	items := []session.Item{{
		ID: "user", Type: "message", Role: "user", Text: "Only publish privately.", CreatedAt: now,
	}}
	for index := 0; index < 40; index++ {
		items = append(items, session.Item{
			ID:        fmt.Sprintf("tool-%d", index),
			Type:      "function_call_output",
			CallID:    fmt.Sprintf("call-%d", index),
			Text:      fmt.Sprintf("%d %s", index, strings.Repeat("tool evidence ", 80)),
			CreatedAt: now,
		})
	}
	store := session.NewStore(home)
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items:     items,
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	catalog := []model.ModelInfo{{Slug: modelID, Visibility: "list", SupportedInAPI: true}}
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		clone := *request
		clone.InputItems = append([]any(nil), request.InputItems...)
		captured = &clone
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"snapshot"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent:        agent,
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
		ThreadRouter: NewRouter(store),
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
	original := reviewer.transcriptSnapshot
	if original == nil {
		t.Fatal("router-built reviewer has no transcript snapshot hook")
	}
	captures := 0
	reviewer.transcriptSnapshot = func(threadID string) *guardianReviewTranscriptSnapshot {
		captures++
		return original(threadID)
	}
	reviewer.syncImageContextWindow = func(string, string) (int64, bool) { return 50_000, true }

	decision, message, err := reviewer.Review(context.Background(), threadID, turnID, "call-snapshot", state.Action{
		Type: "command", Command: "echo hello", CWD: "/workspace",
	})
	if err != nil {
		t.Fatalf("Guardian review error = %v", err)
	}
	if decision != state.DecisionApproved || captured == nil {
		t.Fatalf("decision = %q, message = %q, captured = %v", decision, message, captured != nil)
	}
	if captures != 1 {
		t.Fatalf("review captured the host transcript %d times, want 1", captures)
	}
	var requestText strings.Builder
	requestText.WriteString(captured.Prompt)
	for _, item := range captured.InputItems {
		if encoded, marshalErr := json.Marshal(item); marshalErr == nil {
			requestText.Write(encoded)
		}
	}
	text := requestText.String()
	if !strings.Contains(text, "echo hello") {
		t.Fatalf("review request lost the planned action:\n%s", text)
	}
	// The elastic section policy ran from the same capture, so its budget
	// notice and bounded entry set still shape the delivered request.
	if !strings.Contains(text, "guardian_context_omission") {
		t.Fatalf("elastic review request lost the section omission notice:\n%s", text)
	}
	if count := strings.Count(text, "tool result"); count == 0 || count >= len(items)-1 {
		t.Fatalf("tool transcript entries = %d, want a section-bounded subset", count)
	}
	if !strings.Contains(text, "Only publish privately.") {
		t.Fatalf("review request lost the retained user instruction:\n%s", text)
	}
}
