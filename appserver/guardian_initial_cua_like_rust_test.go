package appserver

import (
	"context"
	"fmt"
	"testing"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

type initialCUATestReviewer struct {
	reviewer      *modelGuardianReviewer
	reviews       *int
	fastDecisions *int
	metrics       *state.TaskMetrics
}

// fastDecisionMetricRecords keeps only the Guardian v2 fast-decision counters;
// a full review records its own routing metrics on the same sink.
func fastDecisionMetricRecords(metrics *state.TaskMetrics) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == telemetry.GuardianV2FastDecisionMetric {
			out = append(out, record)
		}
	}
	return out
}

func newInitialCUATestReviewer(t *testing.T, allow bool, requiredReview bool) initialCUATestReviewer {
	t.Helper()
	reviews := 0
	fastDecisions := 0
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		reviews++
		return &model.AgentResponse{
			Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`,
		}, nil
	})).(*modelGuardianReviewer)
	reviewer.allowsInitialCUACall = func(_, _ string) bool { return allow }
	if requiredReview {
		reviewer.requiredReview = func(_, _ string) bool { return true }
	}
	reviewer.fastDecision = func(context.Context, string, string, string) { fastDecisions++ }
	metrics := state.NewTaskMetrics()
	reviewer.metrics = metrics
	return initialCUATestReviewer{reviewer: reviewer, reviews: &reviews, fastDecisions: &fastDecisions, metrics: metrics}
}

func (r initialCUATestReviewer) review(t *testing.T, threadID, callID string, action state.Action) (state.ReviewDecision, string) {
	t.Helper()
	decision, reason, err := r.reviewer.Review(context.Background(), threadID, "turn-initial-cua", callID, action)
	if err != nil {
		t.Fatalf("Review(%s) error = %v", action.ToolName, err)
	}
	return decision, reason
}

// Mirrors Rust #41666: the first `js` execution from a Node REPL-backed server
// skips the synchronous Guardian wait, while reset/setup tools keep the
// allowance available and later executions follow the normal policy.
func TestGuardianFastApprovesFirstNodeReplJSExecutionLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		server    string
		connector string
		setup     []string
	}{
		{name: "browser startup", server: "node_repl"},
		{name: "computer use startup", server: "cua_repl"},
		{name: "browser reset before execution", server: "node_repl", setup: []string{"js_reset"}},
		{name: "computer use reset before execution", server: "cua_repl", setup: []string{"js_reset"}},
		{name: "browser setup before execution", server: "node_repl", setup: []string{"js_add_node_module_dir"}},
		{name: "computer use setup before execution", server: "cua_repl", setup: []string{"js_add_node_module_dir"}},
		{name: "browser connector", server: mcp.RuntimeCodexAppsMCPServerName, connector: mcp.BrowserConnectorID},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newInitialCUATestReviewer(t, true, false)
			threadID := "thread-" + testCase.name
			callIndex := 0
			for _, setupTool := range testCase.setup {
				callIndex++
				decision, reason := harness.review(t, threadID, fmt.Sprintf("setup-%d", callIndex), state.Action{
					Type: "mcp_tool_call", Server: testCase.server, ConnectorID: testCase.connector, ToolName: setupTool,
				})
				if decision != state.DecisionApproved || reason != "reviewed" {
					t.Fatalf("%s setup review = %q, %q; want a full review", setupTool, decision, reason)
				}
			}
			firstDecision, firstReason := harness.review(t, threadID, "exec-1", state.Action{
				Type: "mcp_tool_call", Server: testCase.server, ConnectorID: testCase.connector, ToolName: "js",
			})
			if firstDecision != state.DecisionApproved || firstReason != "initial computer use call" {
				t.Fatalf("first js execution = %q, %q; want a fast approval", firstDecision, firstReason)
			}
			secondDecision, secondReason := harness.review(t, threadID, "exec-2", state.Action{
				Type: "mcp_tool_call", Server: testCase.server, ConnectorID: testCase.connector, ToolName: "js",
			})
			if secondDecision != state.DecisionApproved || secondReason != "reviewed" {
				t.Fatalf("second js execution = %q, %q; want a full review", secondDecision, secondReason)
			}
			if got, want := *harness.reviews, len(testCase.setup)+1; got != want {
				t.Fatalf("full reviews = %d, want %d", got, want)
			}
			if *harness.fastDecisions != 1 {
				t.Fatalf("fast decisions = %d, want 1", *harness.fastDecisions)
			}
			records := fastDecisionMetricRecords(harness.metrics)
			if len(records) != 1 || records[0].Inc != 1 || records[0].Tags["decision"] != "approved" ||
				records[0].Tags["reason"] != "initial_cua_call" {
				t.Fatalf("fast decision metric = %#v", records)
			}
		})
	}
}

// The allowance is opt-in through the model's Guardian policy and only covers
// Node REPL-backed `js`; everything else keeps the normal review policy.
func TestGuardianInitialCUAFastPathRequiresPolicyAndNodeReplJS(t *testing.T) {
	cases := []struct {
		name           string
		allow          bool
		requiredReview bool
		action         state.Action
	}{
		{
			name:   "policy disallows the allowance",
			action: state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
		},
		{
			name:           "protected model without the allowance",
			requiredReview: true,
			action:         state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
		},
		{
			name:   "non node repl server",
			allow:  true,
			action: state.Action{Type: "mcp_tool_call", Server: "other", ToolName: "js"},
		},
		{
			name:   "node repl setup tool",
			allow:  true,
			action: state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js_reset"},
		},
		{
			name:   "non mcp action",
			allow:  true,
			action: state.Action{Type: "command", Command: "ls", CWD: "/tmp"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newInitialCUATestReviewer(t, testCase.allow, testCase.requiredReview)
			decision, reason := harness.review(t, "thread-"+testCase.name, "call-1", testCase.action)
			if decision != state.DecisionApproved || reason != "reviewed" {
				t.Fatalf("review = %q, %q; want a full review", decision, reason)
			}
			if *harness.reviews != 1 {
				t.Fatalf("full reviews = %d, want 1", *harness.reviews)
			}
			if *harness.fastDecisions != 0 {
				t.Fatalf("fast decisions = %d, want 0", *harness.fastDecisions)
			}
			if records := fastDecisionMetricRecords(harness.metrics); len(records) != 0 {
				t.Fatalf("unexpected fast decision metrics = %#v", records)
			}
		})
	}

	// A protected model keeps the allowance: Rust exempts the first computer-use
	// call from the synchronous-review requirement.
	harness := newInitialCUATestReviewer(t, true, true)
	decision, reason := harness.review(t, "thread-protected-allowance", "call-1", state.Action{
		Type: "mcp_tool_call", Server: "node_repl", ToolName: "js",
	})
	if decision != state.DecisionApproved || reason != "initial computer use call" {
		t.Fatalf("protected first execution = %q, %q; want a fast approval", decision, reason)
	}
	if *harness.reviews != 0 {
		t.Fatalf("full reviews = %d, want 0", *harness.reviews)
	}
}

// Review retries for one invocation must not consume the allowance twice.
func TestGuardianInitialCUAExecutionDedupesReviewRetriesLikeRust(t *testing.T) {
	harness := newInitialCUATestReviewer(t, true, false)
	action := state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"}
	for attempt := 0; attempt < 3; attempt++ {
		decision, reason := harness.review(t, "thread-retry", "call-retry", action)
		if decision != state.DecisionApproved || reason != "initial computer use call" {
			t.Fatalf("attempt %d = %q, %q; want a fast approval", attempt, decision, reason)
		}
	}
	decision, reason := harness.review(t, "thread-retry", "call-next", action)
	if decision != state.DecisionApproved || reason != "reviewed" {
		t.Fatalf("next execution = %q, %q; want a full review", decision, reason)
	}
}
