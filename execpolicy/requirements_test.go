package execpolicy

import (
	"reflect"
	"testing"
)

const (
	envPolicyForbiddenText = `prefix_rule(
    pattern = ["echo", "blocked"],
    decision = "forbidden",
    justification = "managed environment restriction",
)
`
	envPolicyPromptText = `prefix_rule(
    pattern = ["echo"],
    decision = "prompt",
)
`
	envPolicyAllowText = `prefix_rule(
    pattern = ["echo"],
    decision = "allow",
)
`
)

// TestRequirementsPolicyFingerprintOrderIndependent mirrors Rust
// RequirementsExecPolicy::fingerprint (#38899/#38942): equal rule sets in any
// order produce equal sorted fingerprints, and changed rules change the
// fingerprint (which invalidates cached approvals).
func TestRequirementsPolicyFingerprintOrderIndependent(t *testing.T) {
	first, err := ParseRequirementsPolicy("env", envPolicyForbiddenText+envPolicyPromptText)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := ParseRequirementsPolicy("env", envPolicyPromptText+envPolicyForbiddenText)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Fingerprint(), reordered.Fingerprint()) {
		t.Fatalf("order-independent fingerprints differ:\n%#v\n%#v", first.Fingerprint(), reordered.Fingerprint())
	}
	changed, err := ParseRequirementsPolicy("env", envPolicyForbiddenText)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first.Fingerprint(), changed.Fingerprint()) {
		t.Fatalf("changed policy must produce a different fingerprint: %#v", first.Fingerprint())
	}
	if len(first.Fingerprint()) != 2 {
		t.Fatalf("fingerprint length = %d, want 2", len(first.Fingerprint()))
	}
}

// TestRequirementsPolicyHasAllowRules mirrors Rust
// validate_environment_config (#38942): environment policies containing allow
// rules are rejected.
func TestRequirementsPolicyHasAllowRules(t *testing.T) {
	forbidden, err := ParseRequirementsPolicy("env", envPolicyForbiddenText)
	if err != nil {
		t.Fatal(err)
	}
	if forbidden.HasAllowRules() {
		t.Fatal("forbidden-only policy must not have allow rules")
	}
	allow, err := ParseRequirementsPolicy("env", envPolicyAllowText)
	if err != nil {
		t.Fatal(err)
	}
	if !allow.HasAllowRules() {
		t.Fatal("allow policy must be detected")
	}
}

// TestRequirementsPolicyMergeOverlayTightensAccess mirrors Rust
// Policy::merge_overlay (#38942): overlay deny/prompt rules are appended over
// the base policy (including saved prefix approvals) and the merged policy
// retains the strictest decision for a matching command.
func TestRequirementsPolicyMergeOverlayTightensAccess(t *testing.T) {
	base, err := ParseRequirementsPolicy("env", `prefix_rule(pattern=["echo", "blocked"], decision="allow")`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := ParseRequirementsPolicy("env", envPolicyForbiddenText)
	if err != nil {
		t.Fatal(err)
	}
	merged := base.MergeOverlay(overlay)
	if len(merged.Policy().Rules) != 2 {
		t.Fatalf("merged rule count = %d, want 2", len(merged.Policy().Rules))
	}
	// The merged policy's strictest decision for the matching command is
	// forbidden (the overlay wins over the base allow).
	decision := merged.Policy().effectiveDecisionForCommand([]string{"echo", "blocked"})
	if decision != DecisionForbidden {
		t.Fatalf("merged decision = %q, want forbidden", decision)
	}
}

func (p *Policy) effectiveDecisionForCommand(command []string) Decision {
	matches := p.matchesForCommand(command, false)
	if len(matches) == 0 {
		return ""
	}
	return strictestDecision(matches)
}
