package appserver

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/state"
	"codex_go/telemetry"
)

const (
	guardianDenialReconsiderationFeatureKey = "guardian_denial_reconsideration"
	guardianDenialReconsiderationMaxBytes   = 1000
	guardianDenialReconsiderationPromptText = "For this review only, reconsider your previous denial of the same planned action. Think carefully about the action, the available evidence, and the user's authorization under the same security policy. This request grants no new authorization and does not change the policy. Return your final assessment using the same JSON schema."
)

func guardianReviewPlanConfig(plan guardianReviewPlan) *config.Config {
	if plan.Prepared != nil && plan.Prepared.Config != nil {
		return plan.Prepared.Config
	}
	return plan.PrepareInput.ParentConfig
}

func guardianDenialReconsiderationEnabled(plan guardianReviewPlan) bool {
	cfg := guardianReviewPlanConfig(plan)
	return cfg != nil && features.Enabled(cfg.FeatureSettings(), guardianDenialReconsiderationFeatureKey)
}

// guardianDenialReconsiderationPrompt prefers the configured developer message
// and falls back to the built-in policy-preserving request. Programmatic
// callers are clipped at a UTF-8 boundary, mirroring the Rust fragment.
func guardianDenialReconsiderationPrompt(plan guardianReviewPlan) string {
	cfg := guardianReviewPlanConfig(plan)
	if cfg != nil {
		if prompt, ok := cfg.GuardianDenialReconsiderationPrompt(); ok {
			if clipped := clipGuardianPromptToBytes(prompt, guardianDenialReconsiderationMaxBytes); clipped != "" {
				return clipped
			}
		}
	}
	return guardianDenialReconsiderationPromptText
}

func clipGuardianPromptToBytes(prompt string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(prompt) <= maxBytes {
		return prompt
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(prompt[cut]) {
		cut--
	}
	return prompt[:cut]
}

func guardianDenialReconsiderationInputItem(prompt string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []map[string]any{{
			"type": "input_text",
			"text": prompt,
		}},
	}
}

// reconsiderGuardianDenial runs the gated follow-up once in the same review
// session and under the original review deadline. Only a valid assessment
// replaces the first denial. Cancellation aborts; timeout and other failures
// keep the first denial and discard the reusable session (Rust #59107/#59118).
func (r *modelGuardianReviewer) reconsiderGuardianDenial(
	ctx context.Context,
	session *guardianSessionRunner,
	history *guardianConversationHistoryRuntime,
	base *model.AgentRequest,
	plan guardianReviewPlan,
) (*model.AgentResponse, *state.Assessment, bool) {
	if r == nil || session == nil || base == nil {
		return nil, nil, false
	}
	started := time.Now()
	followup := *base
	followup.Prompt = ""
	followup.InputItems = []any{guardianDenialReconsiderationInputItem(guardianDenialReconsiderationPrompt(plan))}
	followup.PostPromptInputItems = nil
	followup.PreviousResponseID = ""
	followup.PreviousResponseFromPrewarm = false
	followup.Store = false

	var response *model.AgentResponse
	var err error
	if history != nil && len(history.Tools) > 0 {
		response, err = runGuardianReviewWithHistory(ctx, session, &followup, history)
	} else {
		response, err = session.Run(ctx, &followup)
	}

	outcome := "failed"
	var assessment *state.Assessment
	canceled := errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))
	timedOut := errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
	switch {
	case canceled:
		outcome = "cancelled"
	case timedOut:
		outcome = "timed_out"
	case err == nil && response != nil:
		if parsed, parseErr := state.ParseAssessment([]byte(guardianAssessmentText(response))); parseErr == nil {
			assessment = parsed
			outcome = string(parsed.Outcome)
		}
	}
	if r.metrics != nil {
		tags := map[string]string{"outcome": outcome}
		r.metrics.Counter(telemetry.GuardianReviewReconsiderationMetric, 1, tags)
		r.metrics.RecordDuration(telemetry.GuardianReviewReconsiderationDurationMetric, time.Since(started), tags)
	}
	if assessment != nil {
		return response, assessment, false
	}
	session.discardReviewHistory()
	if canceled {
		return nil, nil, true
	}
	return nil, nil, false
}
