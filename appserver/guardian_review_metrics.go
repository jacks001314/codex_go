package appserver

import (
	"strings"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/ext/guardian-reviewer/src/metrics.rs
// (emit_guardian_review_metrics + guardian_review_metric_tags). The review
// counter, duration, time-to-first-token, and token-usage histograms all carry
// the review attribution tags.

// guardianCodexAppsMCPServer mirrors Rust's codex_mcp::CODEX_APPS_MCP_SERVER_NAME
// gate for the optional tool tag: custom MCP tool names may contain user data,
// so only OpenAI Apps tools are attributed.
const guardianCodexAppsMCPServer = "codex_apps"

// guardianReviewMetricAttribution carries the attribution for one review
// attempt. The zero value mirrors GuardianReviewAnalyticsResult::without_session
// (decision denied, terminal failed_closed, no failure reason) with Go's
// additional per-review values filled in.
type guardianReviewMetricAttribution struct {
	// Decision is Go's review decision; timed_out maps onto Rust's denied
	// decision (Rust reports a timeout as decision denied + terminal timed_out).
	Decision state.ReviewDecision
	// TerminalStatus is the explicit terminal status: approved, denied, aborted,
	// timed_out, or failed_closed.
	TerminalStatus    string
	FailureReason     string
	ApprovalSource    string
	ReviewedAction    state.Action
	SessionKind       string
	HadPriorContext   *bool
	Truncated         bool
	RiskLevel         *state.RiskLevel
	UserAuthorization *state.UserAuthorization
	Outcome           *state.Outcome
	GuardianModel     string
	TokenUsage        *model.AgentUsage
	TimeToFirstToken  *time.Duration
}

// emitGuardianReviewMetrics mirrors emit_guardian_review_metrics.
func (r *modelGuardianReviewer) emitGuardianReviewMetrics(attribution guardianReviewMetricAttribution, durationMS int64) {
	if r == nil || r.metrics == nil {
		return
	}
	tags := guardianReviewMetricTags(attribution)
	sink := r.metrics
	counterTags := tags
	if toolTag, ok := guardianReviewMCPToolTag(attribution.ReviewedAction); ok {
		counterTags = make(map[string]string, len(tags)+1)
		for key, value := range tags {
			counterTags[key] = value
		}
		counterTags["tool"] = toolTag
	}
	if durationMS < 0 {
		durationMS = 0
	}
	sink.Counter(telemetry.GuardianReviewCountMetric, 1, counterTags)
	sink.RecordDuration(telemetry.GuardianReviewDurationMetric, time.Duration(durationMS)*time.Millisecond, tags)
	if attribution.TimeToFirstToken != nil {
		ttft := *attribution.TimeToFirstToken
		if ttft < 0 {
			ttft = 0
		}
		sink.RecordDuration(telemetry.GuardianReviewTTFTDurationMetric, ttft, tags)
	}
	if attribution.TokenUsage != nil {
		for _, sample := range guardianReviewTokenSamples(*attribution.TokenUsage) {
			tokenTags := make(map[string]string, len(tags)+1)
			for key, value := range tags {
				tokenTags[key] = value
			}
			tokenTags["token_type"] = sample.tokenType
			sink.Histogram(telemetry.GuardianReviewTokenUsageMetric, sample.value, tokenTags)
		}
	}
}

// guardianReviewMetricTags mirrors guardian_review_metric_tags.
func guardianReviewMetricTags(attribution guardianReviewMetricAttribution) map[string]string {
	failureReason := strings.TrimSpace(attribution.FailureReason)
	if failureReason == "" {
		failureReason = "none"
	}
	approvalSource := strings.TrimSpace(attribution.ApprovalSource)
	if approvalSource == "" {
		approvalSource = "main_turn"
	}
	sessionKind := strings.TrimSpace(attribution.SessionKind)
	if sessionKind == "" {
		sessionKind = "none"
	}
	guardianModel := strings.TrimSpace(attribution.GuardianModel)
	if guardianModel == "" {
		guardianModel = "none"
	} else {
		guardianModel = telemetry.SanitizeMetricTagValue(guardianModel)
	}
	return map[string]string{
		"decision":                  guardianReviewDecisionTag(attribution.Decision),
		"terminal_status":           attribution.TerminalStatus,
		"failure_reason":            failureReason,
		"approval_request_source":   approvalSource,
		"action":                    guardianReviewedActionTag(attribution.ReviewedAction),
		"session_kind":              sessionKind,
		"had_prior_review_context":  guardianOptionalBoolTag(attribution.HadPriorContext),
		"reviewed_action_truncated": guardianBoolTag(attribution.Truncated),
		"risk_level":                guardianRiskLevelTag(attribution.RiskLevel),
		"user_authorization":        guardianUserAuthorizationTag(attribution.UserAuthorization),
		"outcome":                   guardianOutcomeTag(attribution.Outcome),
		// Go's review request carries no reasoning-effort override, so the tag
		// takes Rust's "none" value for an unset effort.
		"guardian_reasoning_effort": "none",
		"guardian_model":            guardianModel,
	}
}

func guardianReviewDecisionTag(decision state.ReviewDecision) string {
	switch decision {
	case state.DecisionApproved:
		return "approved"
	case state.DecisionAborted:
		return "aborted"
	default:
		// Rust reports both denied and timed-out reviews as the denied decision.
		return "denied"
	}
}

// guardianReviewedActionTag mirrors reviewed_action_tag for Go's action model:
// a "command" action is a shell/unified-exec launch by its source, and the other
// types carry Rust's label directly.
func guardianReviewedActionTag(action state.Action) string {
	switch strings.TrimSpace(action.Type) {
	case "command":
		if action.Source == state.CommandSourceUnifiedExec {
			return "unified_exec"
		}
		return "shell"
	case "execve":
		return "execve"
	case "apply_patch":
		return "apply_patch"
	case "network_access":
		return "network_access"
	case "mcp_tool_call":
		return "mcp_tool_call"
	case "request_permissions":
		return "request_permissions"
	default:
		typeName := strings.TrimSpace(action.Type)
		if typeName == "" {
			return "shell"
		}
		return typeName
	}
}

func guardianReviewMCPToolTag(action state.Action) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(action.Type), "mcp_tool_call") ||
		!strings.EqualFold(strings.TrimSpace(action.Server), guardianCodexAppsMCPServer) {
		return "", false
	}
	toolName := strings.TrimSpace(action.ToolName)
	if toolName == "" {
		return "", false
	}
	return telemetry.SanitizeMetricTagValue(toolName), true
}

func guardianOptionalBoolTag(value *bool) string {
	switch {
	case value == nil:
		return "unknown"
	case *value:
		return "true"
	default:
		return "false"
	}
}

func guardianBoolTag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func guardianRiskLevelTag(level *state.RiskLevel) string {
	if level == nil || strings.TrimSpace(string(*level)) == "" {
		return "none"
	}
	return string(*level)
}

func guardianUserAuthorizationTag(authorization *state.UserAuthorization) string {
	if authorization == nil || strings.TrimSpace(string(*authorization)) == "" {
		return "none"
	}
	return string(*authorization)
}

func guardianOutcomeTag(outcome *state.Outcome) string {
	if outcome == nil || strings.TrimSpace(string(*outcome)) == "" {
		return "none"
	}
	return string(*outcome)
}

type guardianTokenSample struct {
	tokenType string
	value     int
}

// newGuardianReviewAttribution builds the per-review attribution: Rust's
// GuardianReviewAnalyticsResult session half plus the reviewed action and
// approval-request source. The zero outcome mirrors
// GuardianReviewAnalyticsResult::without_session (denied / failed_closed).
func (r *modelGuardianReviewer) newGuardianReviewAttribution(threadID, turnID string, action state.Action) guardianReviewMetricAttribution {
	attribution := guardianReviewMetricAttribution{
		Decision:       state.DecisionDenied,
		TerminalStatus: "failed_closed",
		FailureReason:  "none",
		ReviewedAction: action,
		GuardianModel:  r.modelForTurn(threadID, turnID),
	}
	if runner, ok := r.agent.(*guardianSessionRunner); ok {
		attribution.SessionKind, attribution.HadPriorContext = runner.reviewSessionAttribution()
	} else {
		attribution.SessionKind = "none"
	}
	attribution.ApprovalSource = "main_turn"
	if r.subagentThread != nil && r.subagentThread(threadID) {
		attribution.ApprovalSource = "delegated_subagent"
	}
	return attribution
}

// reviewMetricsDurationMS mirrors Rust's completion latency:
// completed_at_ms - tracking.started_at_ms.
func guardianReviewMetricsDurationMS(startedAtMS int64, completedAt time.Time) int64 {
	if startedAtMS <= 0 {
		return 0
	}
	duration := completedAt.UTC().UnixMilli() - startedAtMS
	if duration < 0 {
		return 0
	}
	return duration
}

// guardianReviewTokenSamples mirrors emit_guardian_token_usage_histograms,
// including Rust's non_cached_input derivative.
func guardianReviewTokenSamples(usage model.AgentUsage) []guardianTokenSample {
	nonCached := usage.InputTokens - usage.CachedInputTokens
	if nonCached < 0 {
		nonCached = 0
	}
	nonNegative := func(value int64) int {
		if value < 0 {
			return 0
		}
		return int(value)
	}
	return []guardianTokenSample{
		{tokenType: "total", value: nonNegative(usage.TotalTokens)},
		{tokenType: "input", value: nonNegative(usage.InputTokens)},
		{tokenType: "cached_input", value: nonNegative(usage.CachedInputTokens)},
		{tokenType: "cache_write_input", value: nonNegative(usage.CacheWriteInputTokens)},
		{tokenType: "non_cached_input", value: nonNegative(nonCached)},
		{tokenType: "output", value: nonNegative(usage.OutputTokens)},
		{tokenType: "reasoning_output", value: nonNegative(usage.ReasoningOutputTokens)},
	}
}
