package appserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #53670 ("Account for token usage from incomplete responses") splits the
// incomplete-response accounting over the two files this batch owns:
//
//  1. `core/src/session/turn.rs::try_run_sampling_request` accounts for the usage a
//     terminal incomplete response reported before it returns that response's
//     error (`sess.record_incomplete_response_usage`).
//  2. `core/src/session/mod.rs::record_incomplete_response_usage` with
//     `TokenUsageContext::SpendingOnly` adds that usage to the cumulative total,
//     charges the shared rollout budget and lets the token-usage contributors
//     see it, while the stored context baseline (`last_token_usage`,
//     `auto_compact_window_prefill*`, `model_context_window`) keeps its previous
//     value. Counting the partial response as a new baseline would double-count
//     its input and compact again on the next turn. The accounting is written
//     as `let _ = ...`, so exhausting the budget still leaves the original
//     terminal error in place.
//
// The cases drive a real SSE `response.incomplete` event through the model layer,
// the agent loop and the router, mirroring the upstream regression
// `core/tests/suite/client.rs::incomplete_response_is_terminal` and the same
// {reported, missing, malformed} usage arms the model package pins on its own.
func TestIncompleteResponseUsageIsSpentWithoutReplacingTheBaselineLikeRust(t *testing.T) {
	cases := []struct {
		name  string
		usage string
		spent int64
	}{
		{
			name:  "reported_usage",
			usage: `,"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}`,
			spent: 150,
		},
		{name: "missing_usage"},
		{name: "malformed_usage", usage: `,"usage":{"malformed":true}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			threadID := "thread-incomplete-" + testCase.name
			store := session.NewStore(t.TempDir())
			now := fixedTime()
			baseline := map[string]any{"input_tokens": int64(1000), "output_tokens": int64(100), "total_tokens": int64(1100)}
			if err := store.Create(&session.Record{
				ID: session.ThreadID(threadID), SessionID: threadID,
				CreatedAt: now, UpdatedAt: now, RecencyAt: now,
				Metadata: session.Metadata{Model: "gpt-test", Extra: map[string]any{
					"total_token_usage":                           baseline,
					"last_token_usage":                            baseline,
					"model_context_window":                        int64(200000),
					"auto_compact_window_prefill":                 int64(1000),
					"auto_compact_window_prefill_server_observed": true,
					"token_usage_info": map[string]any{
						"total_token_usage":    baseline,
						"last_token_usage":     baseline,
						"model_context_window": int64(200000),
					},
				}},
			}); err != nil {
				t.Fatalf("Create record error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
			// A budget small enough that charging the reported usage exhausts it:
			// the turn still has to fail with its own terminal error.
			budget := runtimeutil.NewBudget()
			budget.Configure(runtimeutil.BudgetConfig{LimitTokens: 1})
			router.rolloutBudgetOnce.Do(func() {})
			router.rolloutBudget = budget
			params := &turn.TurnStartParams{Config: map[string]any{}}

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte(incompleteResponsesSSE(testCase.usage)))
			}))
			defer server.Close()

			loop := turn.NewAgentLoop(&turn.AgentLoopOptions{
				Agent: model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
					Provider: &model.APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 2},
					Stream:   true,
				}),
			})
			result, err := loop.Run(context.Background(), &turn.AgentLoopRequest{
				Prompt:   "trigger output limit",
				Model:    "gpt-test",
				ThreadID: threadID,
				TurnID:   "turn-incomplete",
				OnIncompleteResponseUsage: func(usage model.AgentUsage) {
					router.recordIncompleteResponseUsage(threadID, "turn-incomplete", "gpt-test", params, usage)
				},
			})

			// The terminal error survives the accounting, budget exhaustion included.
			if result != nil {
				t.Fatalf("Run() result = %#v, want nil for a failed step", result)
			}
			if !model.IsResponseIncompleteError(err) {
				t.Fatalf("Run() error = %#v, want the terminal incomplete-response error", err)
			}
			if errors.Is(err, ErrSessionBudgetExceeded) {
				t.Fatalf("Run() error = %v, want the original terminal error", err)
			}
			record, readErr := store.Read(session.ThreadID(threadID), true, true)
			if readErr != nil {
				t.Fatalf("Read record error = %v", readErr)
			}
			extra := record.Metadata.Extra

			// Branch 1: the reported usage is added to the cumulative total.
			total := tokenUsageBreakdownFromMetadata(extra["total_token_usage"])
			if total == nil || total.TotalTokens != 1100+testCase.spent {
				t.Fatalf("total_token_usage = %#v, want %d", extra["total_token_usage"], 1100+testCase.spent)
			}

			// Branch 2: the context baseline keeps every previous value.
			last := tokenUsageBreakdownFromMetadata(extra["last_token_usage"])
			if last == nil || last.TotalTokens != 1100 {
				t.Fatalf("last_token_usage = %#v, want the untouched baseline (1100)", extra["last_token_usage"])
			}
			if got := intFromAny(extra["auto_compact_window_prefill"]); got != 1000 {
				t.Fatalf("auto_compact_window_prefill = %d, want the untouched baseline (1000)", got)
			}
			if got := intFromAny(extra["model_context_window"]); got != 200000 {
				t.Fatalf("model_context_window = %d, want the untouched window (200000)", got)
			}
			info, ok := extra["token_usage_info"].(map[string]any)
			if !ok {
				t.Fatalf("token_usage_info = %#v", extra["token_usage_info"])
			}
			infoTotal := tokenUsageBreakdownFromMetadata(firstMapValue(info, "total_token_usage", "totalTokenUsage", "total"))
			if infoTotal == nil || infoTotal.TotalTokens != 1100+testCase.spent {
				t.Fatalf("token_usage_info.total_token_usage = %#v, want %d", info["total_token_usage"], 1100+testCase.spent)
			}
			infoLast := tokenUsageBreakdownFromMetadata(firstMapValue(info, "last_token_usage", "lastTokenUsage", "last"))
			if infoLast == nil || infoLast.TotalTokens != 1100 {
				t.Fatalf("token_usage_info.last_token_usage = %#v, want the untouched baseline", info["last_token_usage"])
			}

			// Branch 3: a missing or malformed usage is ignored rather than
			// estimated, so nothing is charged and the budget is never touched.
			if testCase.spent == 0 {
				if router.rolloutBudgetCharged.Load() {
					t.Fatal("rolloutBudgetCharged = true, want no charge for a response that reported no usage")
				}
				if router.rolloutBudgetExhausted.Load() {
					t.Fatal("rolloutBudgetExhausted = true, want no charge for a response that reported no usage")
				}
				return
			}
			if !router.rolloutBudgetCharged.Load() || !router.rolloutBudgetExhausted.Load() {
				t.Fatalf("budget charged=%v exhausted=%v, want the reported usage charged against the shared budget",
					router.rolloutBudgetCharged.Load(), router.rolloutBudgetExhausted.Load())
			}
		})
	}
}

// incompleteResponsesSSE renders the terminal stream a server sends when the
// model stops early: the response admission followed by the
// `response.incomplete` event, optionally carrying the usage reported for the
// partial response.
func incompleteResponsesSSE(usage string) string {
	return "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-incomplete\"}}\n\n" +
		"event: response.incomplete\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp-incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}" + usage + "}}\n\n"
}
