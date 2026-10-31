package execpolicy

import (
	"fmt"
	"path/filepath"
	"reflect"
)

// InheritedExecPolicy is a trusted host’s point-in-time session policy and the
// managed requirements it was loaded with (Rust core exec_policy/inheritance.rs
// InheritedExecPolicy, #58406). A host transports both together and reapplies
// them on cold resume.
type InheritedExecPolicy struct {
	Policy             *Policy
	SourceRequirements *RequirementsPolicy
}

// InheritedExecPolicyDestination describes the destination session that would
// install a transported snapshot: the managed-only rule sources of its config
// stack and its own managed exec policy requirement (Rust ConfigLayerStack +
// RequirementsExecPolicy).
type InheritedExecPolicyDestination struct {
	// IgnoreUserAndProjectRules mirrors
	// ConfigLayerStack::ignore_user_and_project_exec_policy_rules.
	IgnoreUserAndProjectRules bool
	// PolicyFiles lists the user/project *.rules files the destination would
	// load (Rust exec_policy::policy_files).
	PolicyFiles []string
	// Requirements is the destination’s managed exec policy requirement.
	Requirements *RequirementsPolicy
}

// InstallInheritedExecPolicy mirrors ExecPolicyManager::from_inherited (#58406):
// a transported snapshot installs only on a managed-only destination whose
// managed requirements match the snapshot’s source requirements and are still
// preserved by it. The returned policy is an independent copy, so later
// amendments in either session do not leak into the other.
func InstallInheritedExecPolicy(snapshot *InheritedExecPolicy, destination InheritedExecPolicyDestination) (*Policy, error) {
	if !destination.IgnoreUserAndProjectRules || len(destination.PolicyFiles) > 0 {
		return nil, fmt.Errorf("inherited execution policy requires a managed-only destination")
	}
	if snapshot == nil || snapshot.Policy == nil {
		return nil, fmt.Errorf("inherited execution policy snapshot is missing")
	}
	source := snapshot.SourceRequirements
	target := destination.Requirements
	compatible := source == nil && target == nil
	if source != nil && target != nil {
		compatible = sameRequirementsPolicy(source, target)
	}
	if !compatible {
		return nil, fmt.Errorf("inherited execution policy requirements differ from the destination")
	}
	if target != nil && !snapshot.Policy.PreservesManagedRequirements(target) {
		return nil, fmt.Errorf("inherited execution policy does not preserve destination requirements")
	}
	return snapshot.Policy.Clone(), nil
}

// sameRequirementsPolicy mirrors Rust RequirementsExecPolicy equality: the rule
// set, the managed network rules, and the host executable mappings must all
// match before a snapshot may be installed.
func sameRequirementsPolicy(source *RequirementsPolicy, target *RequirementsPolicy) bool {
	if !reflect.DeepEqual(source.Fingerprint(), target.Fingerprint()) {
		return false
	}
	if !reflect.DeepEqual(source.Policy().NetworkRules, target.Policy().NetworkRules) {
		return false
	}
	return reflect.DeepEqual(source.Policy().HostExecutables, target.Policy().HostExecutables)
}

// PreservesManagedRequirements reports whether a transported snapshot still
// enforces the destination’s managed rules (Rust
// Policy::preserves_managed_requirements, #58406). Exact path matches suppress
// basename matching, and a later network grant can override a managed denial
// for the same host, so both are checked explicitly.
func (p *Policy) PreservesManagedRequirements(requirements *RequirementsPolicy) bool {
	if p == nil {
		return false
	}
	managed := requirements.Policy()
	if !reflect.DeepEqual(p.HostExecutables, managed.HostExecutables) {
		return false
	}
	for _, rule := range managed.Rules {
		program := ruleProgram(rule)
		if program == "" {
			continue
		}
		if !p.containsPrefixRule(program, rule) {
			return false
		}
	}
	for _, rule := range p.Rules {
		program := ruleProgram(rule)
		if program == "" || !filepath.IsAbs(program) {
			continue
		}
		_, fallbackRules, ok := managed.hostExecutableRulesForPath(program)
		if !ok {
			continue
		}
		pathRules := p.rulesForProgram(program)
		for _, candidate := range pathRules {
			if managed.containsPrefixRule(program, candidate) {
				continue
			}
			for _, fallback := range fallbackRules {
				if canWeakenFallback(candidate, fallback, pathRules) {
					return false
				}
			}
		}
	}
	_, requiredDenials := managed.compiledNetworkDomains()
	_, snapshotDenials := p.compiledNetworkDomains()
	for _, host := range requiredDenials {
		if !containsString(snapshotDenials, host) {
			return false
		}
	}
	return true
}

// containsPrefixRule reports whether the policy holds an identical prefix rule
// for the program (Rust Policy::contains_prefix_rule).
func (p *Policy) containsPrefixRule(program string, rule PrefixRule) bool {
	for _, candidate := range p.rulesForProgram(program) {
		if prefixRulesEqual(candidate, rule) {
			return true
		}
	}
	return false
}

// rulesForProgram returns the rules keyed by the same program token Rust uses
// as its MultiMap key: the first token of the pattern.
func (p *Policy) rulesForProgram(program string) []PrefixRule {
	var rules []PrefixRule
	for _, rule := range p.Rules {
		if ruleProgram(rule) != program {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func ruleProgram(rule PrefixRule) string {
	if len(rule.Pattern) == 0 || len(rule.Pattern[0]) == 0 {
		return ""
	}
	return rule.Pattern[0][0]
}

// prefixRulesEqual mirrors Rust PrefixRule equality: pattern, decision, and
// justification. Go’s example fields are parse-time validation only.
func prefixRulesEqual(a PrefixRule, b PrefixRule) bool {
	return reflect.DeepEqual(a.Pattern, b.Pattern) &&
		a.Decision == b.Decision &&
		a.Justification == b.Justification
}

// hostExecutableRulesForPath mirrors Policy::host_executable_rules: the basename
// rules that would match the absolute program when the policy does not map that
// basename to other paths.
func (p *Policy) hostExecutableRulesForPath(path string) (string, []PrefixRule, bool) {
	basename := executableLookupKey(filepath.Base(path))
	if basename == "" {
		return "", nil, false
	}
	if paths, ok := p.HostExecutables[basename]; ok && !containsString(paths, path) {
		return "", nil, false
	}
	rules := p.rulesForProgram(basename)
	if len(rules) == 0 {
		return "", nil, false
	}
	return basename, rules, true
}

// compiledNetworkDomains mirrors Policy::compiled_network_domains: a later rule
// for the same host overrides an earlier one.
func (p *Policy) compiledNetworkDomains() ([]string, []string) {
	var allowed []string
	var denied []string
	for _, rule := range p.NetworkRules {
		switch rule.Decision {
		case DecisionAllow:
			denied = removePolicyString(denied, rule.Host)
			allowed = upsertPolicyString(allowed, rule.Host)
		case DecisionForbidden:
			allowed = removePolicyString(allowed, rule.Host)
			denied = upsertPolicyString(denied, rule.Host)
		}
	}
	return allowed, denied
}

func upsertPolicyString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removePolicyString(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing != value {
			out = append(out, existing)
		}
	}
	return out
}

// canWeakenFallback mirrors Rust can_weaken_fallback: a path-specific rule
// weakens a managed basename restriction when it lowers the decision over a
// command overlap that no other path rule already covers.
func canWeakenFallback(exact PrefixRule, fallback PrefixRule, pathRules []PrefixRule) bool {
	if decisionRank(exact.Decision) >= decisionRank(fallback.Decision) {
		return false
	}
	exactRest := ruleRest(exact)
	fallbackRest := ruleRest(fallback)
	shorter, longer := exactRest, fallbackRest
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	sharedArgumentsAt := func(index int) []string {
		var shared []string
		if index >= len(longer) {
			return shared
		}
		for _, argument := range longer[index] {
			if index < len(shorter) && !containsString(shorter[index], argument) {
				continue
			}
			shared = append(shared, argument)
		}
		return shared
	}
	for index := range shorter {
		if len(sharedArgumentsAt(index)) == 0 {
			return false
		}
	}
	for _, rule := range pathRules {
		if decisionRank(rule.Decision) < decisionRank(fallback.Decision) {
			continue
		}
		rest := ruleRest(rule)
		if len(rest) > len(longer) {
			continue
		}
		covers := true
		for index := range rest {
			for _, argument := range sharedArgumentsAt(index) {
				if !containsString(rest[index], argument) {
					covers = false
					break
				}
			}
			if !covers {
				break
			}
		}
		if covers {
			return false
		}
	}
	return true
}

func ruleRest(rule PrefixRule) [][]string {
	if len(rule.Pattern) <= 1 {
		return nil
	}
	return rule.Pattern[1:]
}
