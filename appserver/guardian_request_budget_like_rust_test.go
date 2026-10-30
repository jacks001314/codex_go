package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/state"
)

// Mirrors Rust #44281 effective_input_token_limit plus request_budget's fixed
// INPUT_TOKEN_MARGIN: a known window loses the margin, an unknown or missing
// window falls back to the conservative 128k ceiling, and a window smaller than
// the margin admits nothing.
func TestEffectiveGuardianInputTokenLimitLikeRust(t *testing.T) {
	if got, want := effectiveGuardianInputTokenLimit(28_000, true), int64(28_000-guardianInputTokenMargin); got != want {
		t.Fatalf("known window limit = %d, want %d", got, want)
	}
	if got, want := effectiveGuardianInputTokenLimit(0, false), int64(guardianDefaultMaxInputTokens-guardianInputTokenMargin); got != want {
		t.Fatalf("unknown window limit = %d, want %d", got, want)
	}
	if got := effectiveGuardianInputTokenLimit(0, true); got != guardianDefaultMaxInputTokens-guardianInputTokenMargin {
		t.Fatalf("zero known window limit = %d, want the conservative fallback", got)
	}
	if got := effectiveGuardianInputTokenLimit(guardianInputTokenMargin, true); got != 0 {
		t.Fatalf("window at the margin limit = %d, want 0", got)
	}
	if got := effectiveGuardianInputTokenLimit(100, true); got != 0 {
		t.Fatalf("window below the margin limit = %d, want 0", got)
	}
}

// Mirrors Rust #44281 estimate_request_tokens: the complete request counts the
// delivered input items, the prompt message and the output-format metadata.
func TestGuardianRequestTokenEstimateCountsCompleteRequestLikeRust(t *testing.T) {
	items := []any{map[string]any{"type": "text", "text": strings.Repeat("evidence ", 200)}}
	schema := guardianAssessmentOutputSchema()
	itemsOnly := guardianRequestTokenEstimate("", items, nil)
	if itemsOnly <= 0 {
		t.Fatalf("items-only estimate = %d, want a positive cost", itemsOnly)
	}
	if got := guardianRequestTokenEstimate(strings.Repeat("prompt ", 400), items, schema); got <= itemsOnly {
		t.Fatalf("complete estimate = %d, want above the items-only estimate %d", got, itemsOnly)
	}
	if got, want := guardianRequestTokenEstimate("", nil, nil), int64(0); got != want {
		t.Fatalf("empty estimate = %d, want %d", got, want)
	}
}

// Mirrors Rust #44281's existing-context floor
// `estimate_request_tokens(request).max(total_token_usage)`.
func TestGuardianCompleteRequestTokensFloorsAtHistoryLikeRust(t *testing.T) {
	items := []any{map[string]any{"type": "text", "text": "evidence"}}
	estimate := guardianRequestTokenEstimate("prompt", items, nil)
	if got := guardianCompleteRequestTokens("prompt", items, nil, estimate+1_000); got != estimate+1_000 {
		t.Fatalf("history floor = %d, want %d", got, estimate+1_000)
	}
	if got := guardianCompleteRequestTokens("prompt", items, nil, 1); got != estimate {
		t.Fatalf("small history = %d, want the request estimate %d", got, estimate)
	}
}

func TestGuardianBudgetOmissionNoticeLikeRust(t *testing.T) {
	notice := state.GuardianBudgetOmissionNotice
	for _, want := range []string{
		"<guardian_context_omission>",
		"</guardian_context_omission>",
		"were omitted or shortened to fit the review input budget.",
		"User instructions and prior approvals may be incomplete where marked.",
		"Do not infer authorization from missing evidence or treat a partial grant as overriding an omitted restriction.",
	} {
		if !strings.Contains(notice, want) {
			t.Fatalf("budget omission notice missing %q:\n%s", want, notice)
		}
	}
	if got := guardianBudgetOmissionNote(""); got != notice {
		t.Fatalf("empty note = %q, want the budget omission notice", got)
	}
	if got := guardianBudgetOmissionNote(state.GuardianTranscriptOmissionNotice); !strings.HasPrefix(got, state.GuardianTranscriptOmissionNotice) || !strings.Contains(got, notice) {
		t.Fatalf("combined note = %q, want the retention notice followed by the budget omission", got)
	}
	if got := guardianBudgetOmissionNote(notice); got != notice {
		t.Fatalf("repeated note = %q, want it unchanged", got)
	}
}

// Mirrors Rust #44281's enforcement order: optional evidence is omitted
// oldest-first while required evidence stays.
func TestGuardianDropOldestOptionalEvidenceLikeRust(t *testing.T) {
	entries := []state.GuardianTranscriptEntry{
		{Author: "user", LineRole: "user", Text: "do not edit files"},
		{Author: "assistant", LineRole: "assistant", Text: "older commentary", Optional: true},
		{Author: "tool", LineRole: "tool shell result", Text: "ok", Optional: true},
		{Author: "developer", LineRole: "developer", Text: "approved action"},
	}
	reduced, dropped := guardianDropOldestOptionalEvidence(entries)
	if !dropped || len(reduced) != 3 || reduced[1].Text != "ok" {
		t.Fatalf("first drop = %#v (dropped %v), want the older commentary omitted", reduced, dropped)
	}
	reduced, dropped = guardianDropOldestOptionalEvidence(reduced)
	if !dropped || len(reduced) != 2 || reduced[0].Text != "do not edit files" || reduced[1].Text != "approved action" {
		t.Fatalf("second drop = %#v (dropped %v), want the tool entry omitted", reduced, dropped)
	}
	if reduced, dropped = guardianDropOldestOptionalEvidence(reduced); dropped || len(reduced) != 2 {
		t.Fatalf("required evidence was dropped: %#v", reduced)
	}
	if got, dropped := guardianDropOldestOptionalEvidence(nil); dropped || got != nil {
		t.Fatalf("empty evidence = %#v (dropped %v), want no drop", got, dropped)
	}
}

func guardianBudgetTestEntries(commentary string) []state.GuardianTranscriptEntry {
	entries := []state.GuardianTranscriptEntry{
		{Author: "user", LineRole: "user", Text: "Run the command if the approval reviewer allows it."},
	}
	if commentary != "" {
		entries = append(entries, state.GuardianTranscriptEntry{
			Author:   "assistant",
			LineRole: "assistant",
			Text:     commentary,
			Optional: true,
		})
	}
	return entries
}

// Mirrors Rust #44281 review_respects_complete_context_budget: evidence that
// fits its own limits but not the complete request is trimmed to the optional
// evidence and reviewed, with the shared omission notice delivered.
func TestGuardianReviewOmitsOptionalEvidenceToFitCompleteRequestLikeRust(t *testing.T) {
	action := state.Action{Type: "command", Command: "echo guardian-budget-test", CWD: "/repo"}
	var probe *model.AgentRequest
	probeReviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			probe = request
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		transcriptSelection: func(string) guardianTranscriptSelection {
			return guardianTranscriptSelection{Entries: guardianBudgetTestEntries("")}
		},
	}
	probeReviewer.syncImageContextWindow = func(_, _ string) (int64, bool) { return 1 << 30, true }
	if _, _, err := probeReviewer.Review(context.Background(), "thread-budget-probe", "turn-1", "call-1", action); err != nil {
		t.Fatalf("probe review error = %v", err)
	}
	if probe == nil {
		t.Fatal("probe review did not reach the model")
	}
	required := guardianCompleteRequestTokens(probe.Prompt, probe.InputItems, guardianAssessmentOutputSchema(), 0)
	// The budget leaves room for the required evidence plus the omission notice
	// while staying far below the commentary's cost.
	budgetHeadroom := int64(2_000)
	budget := required + budgetHeadroom
	commentary := strings.Repeat("optional old commentary ", 900)

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		transcriptSelection: func(string) guardianTranscriptSelection {
			return guardianTranscriptSelection{Entries: guardianBudgetTestEntries(commentary)}
		},
	}
	reviewer.syncImageContextWindow = func(_, _ string) (int64, bool) {
		return budget + guardianInputTokenMargin, true
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-budget-fit", "turn-1", "call-1", action)
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved (reason: %q)", decision, reason)
	}
	if captured == nil {
		t.Fatal("the reviewer request was not sent after omitting optional evidence")
	}
	if !strings.Contains(captured.Prompt, "Run the command if the approval reviewer allows it.") {
		t.Fatalf("required user evidence was omitted:\n%s", captured.Prompt)
	}
	if strings.Contains(captured.Prompt, "optional old commentary") {
		t.Fatalf("optional commentary exceeded the budget but was still delivered:\n%s", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, "<guardian_context_omission>") {
		t.Fatalf("prompt is missing the budget omission notice:\n%s", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, "echo guardian-budget-test") {
		t.Fatalf("planned action is missing from the prompt:\n%s", captured.Prompt)
	}
	if total := guardianCompleteRequestTokens(captured.Prompt, captured.InputItems, guardianAssessmentOutputSchema(), 0); total > budget {
		t.Fatalf("delivered request tokens = %d, want within the %d-token budget", total, budget)
	}
}

// Mirrors Rust #44281 required_context_fails_closed and
// oversized_action_preserves_review_policy_and_next_review's required arm: when
// the complete action and minimum context cannot fit, the review is rejected
// before sampling with the shared input-budget message.
func TestGuardianReviewRejectsRequiredEvidenceOverBudgetLikeRust(t *testing.T) {
	requests := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			requests++
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		transcriptSelection: func(string) guardianTranscriptSelection {
			return guardianTranscriptSelection{Entries: guardianBudgetTestEntries("")}
		},
	}
	reviewer.syncImageContextWindow = func(_, _ string) (int64, bool) { return 1, true }
	decision, reason, err := reviewer.Review(context.Background(), "thread-budget-reject", "turn-1", "call-1", state.Action{
		Type:    "command",
		Command: "echo guardian-budget-test",
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("decision = %q, want denied", decision)
	}
	if requests != 0 {
		t.Fatalf("oversized request reached the model %d times, want 0", requests)
	}
	if !strings.Contains(reason, guardianInputBudgetMessage) {
		t.Fatalf("reason = %q, want the reviewer input budget message", reason)
	}
	if !strings.Contains(reason, guardianReviewFailureInstructions) {
		t.Fatalf("reason = %q, want the failed-review instructions", reason)
	}
}
