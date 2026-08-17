package execpolicy

import (
	"fmt"
	"sort"
	"strings"
)

// RequirementsPolicy wraps a parsed Policy as a managed (requirements /
// environment) command policy. It mirrors Rust
// codex_execpolicy::RequirementsExecPolicy (execpolicy/src/policy.rs,
// #38899/#38942): a restrictive overlay that can only tighten command access,
// with an order-independent fingerprint used to invalidate cached approvals
// when the policy changes.
type RequirementsPolicy struct {
	policy *Policy
	text   string
}

// Text returns the original policy DSL text when the policy was parsed from
// text (empty for programmatically built policies).
func (r *RequirementsPolicy) Text() string {
	if r == nil {
		return ""
	}
	return r.text
}

// Clone returns a deep copy of the requirements policy.
func (r *RequirementsPolicy) Clone() *RequirementsPolicy {
	if r == nil {
		return nil
	}
	return &RequirementsPolicy{policy: r.Policy(), text: r.text}
}

// NewRequirementsPolicy wraps a parsed policy. A nil policy is treated as an
// empty policy.
func NewRequirementsPolicy(policy *Policy) *RequirementsPolicy {
	if policy == nil {
		return &RequirementsPolicy{policy: &Policy{}}
	}
	return &RequirementsPolicy{policy: policy}
}

// ParseRequirementsPolicy parses policy DSL text (the same syntax accepted by
// the execpolicy check CLI) into a RequirementsPolicy.
func ParseRequirementsPolicy(identifier string, input string) (*RequirementsPolicy, error) {
	policy := &Policy{}
	if err := policy.Parse(identifier, input); err != nil {
		return nil, err
	}
	return &RequirementsPolicy{policy: policy, text: input}, nil
}

// Policy exposes the wrapped policy.
func (r *RequirementsPolicy) Policy() *Policy {
	if r == nil || r.policy == nil {
		return &Policy{}
	}
	return r.policy
}

// Fingerprint returns the order-independent policy fingerprint (sorted
// "program:rule" entries), mirroring Rust RequirementsExecPolicy::fingerprint.
// Equal policies (in any rule order) produce equal fingerprints; changed rules
// invalidate cached approvals.
func (r *RequirementsPolicy) Fingerprint() []string {
	if r == nil || r.policy == nil {
		return []string{}
	}
	var entries []string
	for _, rule := range r.policy.Rules {
		program := "?"
		if len(rule.Pattern) > 0 && len(rule.Pattern[0]) > 0 {
			program = rule.Pattern[0][0]
		}
		entries = append(entries, fmt.Sprintf("%s:%s", program, requirementsRuleEntry(rule)))
	}
	sort.Strings(entries)
	return entries
}

func requirementsRuleEntry(rule PrefixRule) string {
	var patternParts []string
	for _, alternative := range rule.Pattern {
		patternParts = append(patternParts, "["+strings.Join(alternative, ",")+"]")
	}
	return fmt.Sprintf(
		"pattern=%s decision=%s justification=%q",
		strings.Join(patternParts, "|"),
		string(rule.Decision),
		rule.Justification,
	)
}

// HasAllowRules reports whether the policy contains allow rules. Environment
// policies must not introduce command allowances (Rust
// validate_environment_config, #38942).
func (r *RequirementsPolicy) HasAllowRules() bool {
	if r == nil || r.policy == nil {
		return false
	}
	for _, rule := range r.policy.Rules {
		if rule.Decision == DecisionAllow {
			return true
		}
	}
	return false
}

// MergeOverlay merges a restrictive overlay over the receiver, mirroring Rust
// Policy::merge_overlay (#38942): overlay deny/prompt rules take precedence
// over the base policy (including saved prefix approvals), and the overlay can
// only tighten command access.
func (r *RequirementsPolicy) MergeOverlay(overlay *RequirementsPolicy) *RequirementsPolicy {
	base := r.Policy()
	merged := &Policy{
		Rules:           append([]PrefixRule(nil), base.Rules...),
		NetworkRules:    append([]NetworkRule(nil), base.NetworkRules...),
		HostExecutables: cloneHostExecutables(base.HostExecutables),
	}
	if overlay != nil {
		overlayPolicy := overlay.Policy()
		for _, rule := range overlayPolicy.Rules {
			merged.Rules = append(merged.Rules, rule)
		}
		for _, rule := range overlayPolicy.NetworkRules {
			merged.NetworkRules = append(merged.NetworkRules, rule)
		}
	}
	return NewRequirementsPolicy(merged)
}

func cloneHostExecutables(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string][]string, len(source))
	for key, values := range source {
		out[key] = append([]string(nil), values...)
	}
	return out
}
