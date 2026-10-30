package appserver

import (
	"strings"

	"codex_go/execpolicy"
	"codex_go/safety"
	"codex_go/shell"
	"codex_go/turn"
)

// managedCommandPolicyForThread merges the managed `[rules]` requirement with
// every ready environment attachment's exec policy. Rust #58045 evaluates the
// turn's environment policies together with the managed requirements, and an
// overlay can only tighten the result, so the strictest decision still wins.
func (r *RuntimeRouter) managedCommandPolicyForThread(threadID string) *execpolicy.RequirementsPolicy {
	if r == nil {
		return nil
	}
	var policy *execpolicy.RequirementsPolicy
	if r.services.Config != nil {
		if read := r.services.Config.Requirements(); read != nil && read.Requirements != nil {
			policy = read.Requirements.Rules
		}
	}
	for _, selection := range r.threadEnvironmentSelections(threadID) {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil || state.Kind != EnvironmentConfigReady || state.Config == nil || state.Config.ExecPolicy == nil {
			continue
		}
		if policy == nil {
			policy = state.Config.ExecPolicy
			continue
		}
		policy = policy.MergeOverlay(state.Config.ExecPolicy)
	}
	return policy
}

// managedHumanApprovalCheckpoint reports the reason when the managed command
// policy requires a fresh human decision for this command. Every parsed shell
// segment is evaluated, matching Rust's per-segment exec-policy check.
func (r *RuntimeRouter) managedHumanApprovalCheckpoint(threadID string, command string) (string, bool) {
	policy := r.managedCommandPolicyForThread(threadID)
	if policy == nil || policy.Policy() == nil {
		return "", false
	}
	for _, segment := range humanApprovalCheckpointSegments(command) {
		evaluation := policy.Policy().EvaluateCommand(segment)
		if evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionPromptUser {
			continue
		}
		joined := strings.Join(segment, " ")
		for _, match := range evaluation.MatchedRules {
			if match.PrefixRuleMatch.Decision != execpolicy.DecisionPromptUser {
				continue
			}
			if justification := strings.TrimSpace(match.PrefixRuleMatch.Justification); justification != "" {
				return "`" + joined + "` requires human approval: " + justification, true
			}
			return "`" + joined + "` requires human approval by policy", true
		}
		return "`" + joined + "` requires human approval by policy", true
	}
	return "", false
}

// humanApprovalCheckpointSegments splits a model-provided command into the
// token segments an exec policy can evaluate. Segments that use shell forms
// whose runtime expansion the policy cannot see (redirection, substitutions,
// escapes outside quotes) are skipped instead of being matched on source text.
func humanApprovalCheckpointSegments(command string) [][]string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	var segments []string
	var current strings.Builder
	var quote rune
	for _, ch := range command {
		if quote != 0 {
			current.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			current.WriteRune(ch)
		case ';', '\n':
			segments = append(segments, current.String())
			current.Reset()
		case '&', '|':
			// `&&`, `||`, or a pipe: the next token starts a new segment.
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}
	segments = append(segments, current.String())

	out := make([][]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" || unsupportedCheckpointSegment(segment) {
			continue
		}
		tokens := shell.SplitCommandLine(segment)
		if len(tokens) == 0 {
			continue
		}
		out = append(out, tokens)
	}
	return out
}

func unsupportedCheckpointSegment(segment string) bool {
	if strings.ContainsAny(segment, "<>`") || strings.Contains(segment, "$(") {
		return true
	}
	// An unterminated quote means the shell sees a different token stream than
	// the splitter; do not match rules against it.
	var quote rune
	for _, ch := range segment {
		switch {
		case quote == 0 && (ch == '\'' || ch == '"'):
			quote = ch
		case quote != 0 && ch == quote:
			quote = 0
		}
	}
	return quote != 0
}

// humanApprovalRejectionForTurn applies Rust #58045's pre-checks: a managed
// checkpoint cannot be answered when the approval policy forbids prompts or
// when the session is pinned to automatic review.
func (r *RuntimeRouter) humanApprovalRejectionForTurn(threadID string, turnID string) (string, bool) {
	if r == nil {
		return "", false
	}
	params := r.activeTurnParams(threadID)
	if params == nil {
		params = &turn.TurnStartParams{ThreadID: strings.TrimSpace(threadID)}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		cfg = nil
	}
	if reason, rejected := safety.PromptRejectedByPolicy(r.terminalWriteSafetyPolicy(threadID, cfg), true); rejected {
		return reason, true
	}
	reviewer := r.approvalsReviewerForTurn(threadID, turnID)
	if reviewer.RoutesToGuardian() {
		return "Managed command policy requires human approval, but this session requires automatic review.", true
	}
	modelID := strings.TrimSpace(params.Model)
	if modelID == "" && cfg != nil && cfg.Values != nil {
		if value, ok := cfg.Values["model"].(string); ok {
			modelID = strings.TrimSpace(value)
		}
	}
	if modelID != "" && autoReviewRequiredForModel(cfg, modelID) {
		return "Managed command policy requires human approval, but this session requires automatic review.", true
	}
	return "", false
}
