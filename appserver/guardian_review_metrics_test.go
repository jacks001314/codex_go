package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/config"
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
		ContextMode:       state.GuardianContextModeThreadOwned,
	})
	want := map[string]string{
		"context_mode":              "thread_owned",
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
		"context_mode":              "unknown",
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

func TestGuardianContextMetricsUseExplicitSharedBoundariesLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	reviewer := &modelGuardianReviewer{metrics: metrics}
	reviewer.emitGuardianContextMetrics(
		[]state.GuardianPromptSectionCost{
			{Section: "conversation_transcript", TextBytes: 2_000_000},
			{Section: "permissions", TextBytes: 64},
		},
		"review prompt",
		"review instructions",
		[]any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_text", "text": "evidence"}}}},
	)

	sectionRecords := guardianMetricRecords(metrics, telemetry.GuardianContextSectionCostMetric)
	if len(sectionRecords) != 8 {
		t.Fatalf("section metric records = %d, want 8: %#v", len(sectionRecords), sectionRecords)
	}
	for _, record := range sectionRecords {
		if record.Tags["target"] != "sync" {
			t.Fatalf("section target = %q", record.Tags["target"])
		}
		if len(record.Boundaries) != len(telemetry.GuardianContextSectionCostBoundaries) ||
			record.Boundaries[len(record.Boundaries)-1] != 16_777_216 {
			t.Fatalf("section boundaries = %#v", record.Boundaries)
		}
	}

	requestRecords := guardianMetricRecords(metrics, telemetry.GuardianContextRequestTokensMetric)
	if len(requestRecords) != 1 {
		t.Fatalf("request metric records = %d, want 1: %#v", len(requestRecords), requestRecords)
	}
	request := requestRecords[0]
	if request.Tags["target"] != "sync" || request.Tags["component"] != "total" {
		t.Fatalf("request tags = %#v", request.Tags)
	}
	if request.Tags["token_bucket"] != telemetry.GuardianRequestTokenBucket(int(request.Value)) {
		t.Fatalf("request token bucket = %q for %v", request.Tags["token_bucket"], request.Value)
	}
	if len(request.Boundaries) != len(telemetry.GuardianContextRequestTokensBoundaries) ||
		request.Boundaries[len(request.Boundaries)-1] != 2_000_000 {
		t.Fatalf("request boundaries = %#v", request.Boundaries)
	}
}

func TestGuardianReviewRetryMetricsCountOnlyAdmittedRetries(t *testing.T) {
	metrics := state.NewTaskMetrics()
	reviewer := &modelGuardianReviewer{metrics: metrics}
	reviewer.emitGuardianReviewRetryMetrics("session_error", time.Now(), nil)
	reviewer.emitGuardianReviewRetryMetrics("session_error", time.Now(), context.DeadlineExceeded)
	reviewer.emitGuardianReviewRetryMetrics("session_error", time.Now(), context.Canceled)

	waits := guardianMetricRecords(metrics, telemetry.GuardianReviewRetryWaitMetric)
	if len(waits) != 3 {
		t.Fatalf("retry waits = %d, want 3", len(waits))
	}
	outcomes := map[string]bool{}
	for _, record := range waits {
		if record.Tags["failure_reason"] != "session_error" {
			t.Fatalf("wait tags = %#v", record.Tags)
		}
		outcomes[record.Tags["outcome"]] = true
	}
	for _, outcome := range []string{"retry", "deadline", "cancelled"} {
		if !outcomes[outcome] {
			t.Fatalf("missing wait outcome %q: %#v", outcome, outcomes)
		}
	}
	retries := guardianMetricRecords(metrics, telemetry.GuardianReviewRetryMetric)
	if len(retries) != 1 || retries[0].Tags["outcome"] != "retry" {
		t.Fatalf("retry counter = %#v", retries)
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
		reviewPlan: func(string, string) guardianReviewPlan {
			return guardianReviewPlan{Selection: model.ApprovalReviewModel{Model: "gpt-5-guardian"}}
		},
		contextMode: func(string, string) state.GuardianContextMode {
			return state.GuardianContextModeThreadOwned
		},
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
		"context_mode":              "thread_owned",
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
		counter.Tags["action"] != "apply_patch" ||
		counter.Tags["context_mode"] != "unknown" {
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
	if len(warnings) != 1 || warnings[0] != state.GuardianTimeoutRationale() {
		t.Fatalf("warnings = %#v", warnings)
	}
}

// Rust captures the review-history policy before preparation and keeps it when
// the reviewer fails to start. The started and fail-closed events both retain
// the host-only attribution used by the review and classification telemetry.
func TestModelGuardianReviewerCapturesContextModeBeforePreparationFailure(t *testing.T) {
	metrics := state.NewTaskMetrics()
	var events []*state.Event
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			t.Fatal("review agent ran after preparation failed")
			return nil, nil
		}),
		metrics: metrics,
		reviewPlan: func(string, string) guardianReviewPlan {
			return guardianReviewPlan{PreparedError: "reviewer startup failed"}
		},
		contextMode: func(string, string) state.GuardianContextMode {
			return state.GuardianContextModeThreadOwned
		},
		notify: func(_ string, event *state.Event) {
			if event == nil {
				return
			}
			cloned := *event
			events = append(events, &cloned)
		},
		warn: func(string, string) {},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type: "command", Command: "echo hi", CWD: t.TempDir(),
	})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision = %s err = %v", decision, err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	for index, event := range events {
		if event.GuardianContextMode != string(state.GuardianContextModeThreadOwned) {
			t.Fatalf("event %d status=%s context mode = %q", index, event.Status, event.GuardianContextMode)
		}
	}
	if events[0].Status != state.StatusInProgress || !events[1].Terminal() {
		t.Fatalf("event statuses = %s, %s", events[0].Status, events[1].Status)
	}
	counters := guardianMetricRecords(metrics, telemetry.GuardianReviewCountMetric)
	if len(counters) != 1 ||
		counters[0].Tags["context_mode"] != "thread_owned" ||
		counters[0].Tags["failure_reason"] != "session_error" {
		t.Fatalf("counters = %#v", counters)
	}
}

func TestGuardianContextModeForConfigFollowsParentCompactionPolicy(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		values map[string]any
		want   state.GuardianContextMode
	}{
		{
			name:   "reuse parent compaction",
			values: map[string]any{"features": map[string]any{"guardian_reuse_parent_compaction": true}},
			want:   state.GuardianContextModeThreadOwned,
		},
		{
			name:   "independent review history",
			values: map[string]any{"features": map[string]any{"guardian_reuse_parent_compaction": false}},
			want:   state.GuardianContextModeIndependent,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &config.Config{Values: testCase.values}
			if got := guardianContextModeForConfig(cfg); got != testCase.want {
				t.Fatalf("context mode = %q, want %q", got, testCase.want)
			}
		})
	}
	if got := guardianContextModeForConfig(nil); got != "" {
		t.Fatalf("nil config context mode = %q", got)
	}
}
