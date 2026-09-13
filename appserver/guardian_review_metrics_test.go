package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

func guardianMetricRecords(metrics *state.TaskMetrics, name string) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == name {
			out = append(out, record)
		}
	}
	return out
}

// Mirrors guardian_review_metric_tags: the label vocabulary for every
// attribution field, including the denied mapping for a timed-out decision, the
// command-source action labels, and the "none"/"unknown" fallbacks.
func TestGuardianReviewMetricTagsLikeRust(t *testing.T) {
	prior := true
	risk := state.RiskHigh
	authorization := state.AuthorizationLow
	outcome := state.OutcomeDeny
	tags := guardianReviewMetricTags(guardianReviewMetricAttribution{
		Decision:          state.DecisionTimedOut,
		TerminalStatus:    "timed_out",
		FailureReason:     "timeout",
		ApprovalSource:    "delegated_subagent",
		ReviewedAction:    state.Action{Type: "command", Source: state.CommandSourceUnifiedExec},
		SessionKind:       "trunk_reused",
		HadPriorContext:   &prior,
		Truncated:         true,
		RiskLevel:         &risk,
		UserAuthorization: &authorization,
		Outcome:           &outcome,
		GuardianModel:     "gpt-5.1-guardian",
	})
	want := map[string]string{
		"decision":                  "denied",
		"terminal_status":           "timed_out",
		"failure_reason":            "timeout",
		"approval_request_source":   "delegated_subagent",
		"action":                    "unified_exec",
		"session_kind":              "trunk_reused",
		"had_prior_review_context":  "true",
		"reviewed_action_truncated": "true",
		"risk_level":                "high",
		"user_authorization":        "low",
		"outcome":                   "deny",
		"guardian_model":            "gpt-5.1-guardian",
		"guardian_reasoning_effort": "none",
	}
	if len(tags) != len(want) {
		t.Fatalf("tags = %#v", tags)
	}
	for key, expected := range want {
		if tags[key] != expected {
			t.Fatalf("tag %s = %q, want %q (all %#v)", key, tags[key], expected, tags)
		}
	}

	// The defaults: denied/failed_closed with none-style fallbacks, a shell
	// command, a new trunk, and an unknown prior-context value.
	defaults := guardianReviewMetricTags(guardianReviewMetricAttribution{
		Decision:       state.DecisionDenied,
		TerminalStatus: "failed_closed",
		ReviewedAction: state.Action{Type: "command"},
		SessionKind:    "trunk_new",
	})
	for key, expected := range map[string]string{
		"decision":                  "denied",
		"terminal_status":           "failed_closed",
		"failure_reason":            "none",
		"approval_request_source":   "main_turn",
		"action":                    "shell",
		"session_kind":              "trunk_new",
		"had_prior_review_context":  "unknown",
		"reviewed_action_truncated": "false",
		"risk_level":                "none",
		"user_authorization":        "none",
		"outcome":                   "none",
		"guardian_model":            "none",
	} {
		if defaults[key] != expected {
			t.Fatalf("default tag %s = %q, want %q", key, defaults[key], expected)
		}
	}

	// The remaining action labels.
	for _, testCase := range []struct {
		action   state.Action
		expected string
	}{
		{state.Action{Type: "command", Source: state.CommandSourceShell}, "shell"},
		{state.Action{Type: "execve"}, "execve"},
		{state.Action{Type: "apply_patch"}, "apply_patch"},
		{state.Action{Type: "network_access"}, "network_access"},
		{state.Action{Type: "mcp_tool_call"}, "mcp_tool_call"},
		{state.Action{Type: "request_permissions"}, "request_permissions"},
	} {
		if got := guardianReviewedActionTag(testCase.action); got != testCase.expected {
			t.Fatalf("action %q = %q, want %q", testCase.action.Type, got, testCase.expected)
		}
	}
}

// A completed review emits the counter, duration, and the seven token-usage
// histograms, with the optional tool tag only for a codex_apps MCP tool.
func TestModelGuardianReviewerEmitsMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	reviewer := &modelGuardianReviewer{
		agent: &guardianSessionRunner{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{
				Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"denied"}`,
				Usage:   model.AgentUsage{InputTokens: 10, CachedInputTokens: 4, OutputTokens: 3, ReasoningOutputTokens: 2, TotalTokens: 13},
			}, nil
		})},
		model:   func(string, string) string { return "gpt-5-guardian" },
		metrics: metrics,
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type: "mcp_tool_call", Server: guardianCodexAppsMCPServer, ToolName: "drive.search",
	})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision = %s err = %v", decision, err)
	}

	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("counters = %#v", counters)
	}
	counter := counters[0]
	for key, expected := range map[string]string{
		"decision":                  "denied",
		"terminal_status":           "denied",
		"failure_reason":            "none",
		"approval_request_source":   "main_turn",
		"action":                    "mcp_tool_call",
		"session_kind":              "trunk_new",
		"had_prior_review_context":  "false",
		"reviewed_action_truncated": "false",
		"risk_level":                "high",
		"user_authorization":        "low",
		"outcome":                   "deny",
		"guardian_model":            "gpt-5-guardian",
		"guardian_reasoning_effort": "none",
		"tool":                      "drive.search",
	} {
		if counter.Tags[key] != expected {
			t.Fatalf("counter tag %s = %q, want %q (%#v)", key, counter.Tags[key], expected, counter.Tags)
		}
	}
	durations := guardianMetricRecords(metrics, telemetry.GuardianReviewDurationMetric)
	if len(durations) != 1 || durations[0].Kind != "duration" || durations[0].DurationMS < 0 {
		t.Fatalf("durations = %#v", durations)
	}
	tokens := guardianMetricRecords(metrics, telemetry.GuardianReviewTokenUsageMetric)
	if len(tokens) != 7 {
		t.Fatalf("token histograms = %#v", tokens)
	}
	byTokenType := map[string]int{}
	for _, record := range tokens {
		byTokenType[record.Tags["token_type"]] = record.Value
	}
	for tokenType, expected := range map[string]int{
		"total": 13, "input": 10, "cached_input": 4, "cache_write_input": 0,
		"non_cached_input": 6, "output": 3, "reasoning_output": 2,
	} {
		if byTokenType[tokenType] != expected {
			t.Fatalf("token %s = %d, want %d (all %#v)", tokenType, byTokenType[tokenType], expected, byTokenType)
		}
	}
}

// A malformed assessment records the aborted / parse_error attribution, and a
// subagent thread reports the delegated approval source.
func TestModelGuardianReviewerFailureMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	var warnings []string
	reviewer := &modelGuardianReviewer{
		agent: &guardianSessionRunner{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: "not-json"}, nil
		})},
		metrics:        metrics,
		subagentThread: func(string) bool { return true },
		warn:           func(_ string, message string) { warnings = append(warnings, message) },
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type: "apply_patch", CWD: t.TempDir(), Files: []string{"a.txt"},
	})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision = %s err = %v", decision, err)
	}
	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("counters = %#v", counters)
	}
	counter := counters[0]
	if counter.Tags["decision"] != "denied" || counter.Tags["terminal_status"] != "failed_closed" ||
		counter.Tags["failure_reason"] != "parse_error" ||
		counter.Tags["approval_request_source"] != "delegated_subagent" ||
		counter.Tags["action"] != "apply_patch" {
		t.Fatalf("counter tags = %#v", counter.Tags)
	}
	if _, ok := counter.Tags["tool"]; ok {
		t.Fatalf("a non-codex_apps action carried the tool tag: %#v", counter.Tags)
	}
	if records := guardianMetricRecords(metrics, telemetry.GuardianReviewTokenUsageMetric); len(records) != 0 {
		t.Fatalf("token histograms = %#v", records)
	}
	// Rust warns the session about the failed review.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Automatic approval review failed:") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

// A timed-out review reports Rust's denied decision with the timed_out terminal
// status.
func TestModelGuardianReviewerTimeoutMetricsLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	var warnings []string
	reviewer := &modelGuardianReviewer{
		timeout: time.Millisecond,
		agent: &guardianSessionRunner{agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})},
		metrics: metrics,
		warn:    func(_ string, message string) { warnings = append(warnings, message) },
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type: "command", Source: state.CommandSourceShell, Command: "echo hi", CWD: t.TempDir(),
	}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 {
		t.Fatalf("counters = %#v", counters)
	}
	if counters[0].Tags["decision"] != "denied" || counters[0].Tags["terminal_status"] != "timed_out" ||
		counters[0].Tags["failure_reason"] != "timeout" {
		t.Fatalf("counter tags = %#v", counters[0].Tags)
	}
	if len(warnings) != 1 || warnings[0] != state.GuardianTimeoutMessage() {
		t.Fatalf("warnings = %#v", warnings)
	}
}
