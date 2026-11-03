package safety

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPromptRejectedByPolicy(t *testing.T) {
	if got, ok := PromptRejectedByPolicy(ApprovalPolicy{Mode: ApprovalNever}, false); !ok || got != PromptConflictReason {
		t.Fatalf("PromptRejectedByPolicy(never) = %q/%v", got, ok)
	}
	if got, ok := PromptRejectedByPolicy(ApprovalPolicy{Mode: ApprovalGranular, Rules: false, SandboxApproval: true}, true); !ok || got != RejectRulesApprovalReason {
		t.Fatalf("PromptRejectedByPolicy(granular rules) = %q/%v", got, ok)
	}
	if got, ok := PromptRejectedByPolicy(ApprovalPolicy{Mode: ApprovalGranular, Rules: true, SandboxApproval: false}, false); !ok || got != RejectSandboxApprovalReason {
		t.Fatalf("PromptRejectedByPolicy(granular sandbox) = %q/%v", got, ok)
	}
	if _, ok := PromptRejectedByPolicy(ApprovalPolicy{Mode: ApprovalOnRequest}, true); ok {
		t.Fatalf("PromptRejectedByPolicy(on-request) ok = true, want false")
	}
}

func TestRequirementFromEvaluation(t *testing.T) {
	req := RequirementFromEvaluation(
		[]string{"npm", "install"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionPrompt, MatchedRules: []RuleMatch{{Decision: DecisionPrompt, IsPolicy: true, Prefix: []string{"npm", "install"}}}},
		nil,
		true,
	)
	if req.Kind != ExecNeedsApproval || req.Amendment != nil {
		t.Fatalf("RequirementFromEvaluation(prompt) = %#v", req)
	}
	heuristic := RequirementFromEvaluation(
		[]string{"rm", "-rf", "build"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionPrompt, MatchedRules: []RuleMatch{{Decision: DecisionPrompt, Prefix: []string{"rm", "-rf", "build"}}}},
		nil,
		true,
	)
	if heuristic.Kind != ExecNeedsApproval || heuristic.Amendment == nil {
		t.Fatalf("RequirementFromEvaluation(heuristic prompt) = %#v", heuristic)
	}
	if !reflect.DeepEqual(heuristic.Amendment.PrefixRule, []string{"rm", "-rf", "build"}) {
		t.Fatalf("heuristic amendment = %#v", heuristic.Amendment.PrefixRule)
	}
	forbidden := RequirementFromEvaluation(
		[]string{"npm", "install"},
		ApprovalPolicy{Mode: ApprovalNever},
		Evaluation{Decision: DecisionPrompt, MatchedRules: []RuleMatch{{Decision: DecisionPrompt, IsPolicy: true}}},
		nil,
		true,
	)
	if forbidden.Kind != ExecForbidden || forbidden.Reason != PromptConflictReason {
		t.Fatalf("RequirementFromEvaluation(rejected) = %#v", forbidden)
	}
	skip := RequirementFromEvaluation(
		[]string{"go", "test"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionAllow, MatchedRules: []RuleMatch{{Decision: DecisionAllow, IsPolicy: true}}},
		nil,
		true,
	)
	if skip.Kind != ExecSkip || !skip.BypassSandbox {
		t.Fatalf("RequirementFromEvaluation(allow) = %#v", skip)
	}
	if skip.Amendment != nil {
		t.Fatalf("policy allow amendment = %#v, want none", skip.Amendment)
	}
}

// TestAllowRequirementCarriesHeuristicsAmendmentLikeRust pins Rust's
// try_derive_execpolicy_amendment_for_allow_rules: a heuristically allowed
// command keeps an amendment for a later sandbox-denial retry prompt, while a
// policy allow rule suppresses it.
func TestAllowRequirementCarriesHeuristicsAmendmentLikeRust(t *testing.T) {
	heuristic := RequirementFromEvaluation(
		[]string{"go", "test"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionAllow, MatchedRules: []RuleMatch{{Decision: DecisionAllow, Prefix: []string{"go", "test"}}}},
		nil,
		true,
	)
	if heuristic.Kind != ExecSkip || heuristic.Amendment == nil {
		t.Fatalf("heuristics allow requirement = %#v", heuristic)
	}
	if want := []string{"go", "test"}; !reflect.DeepEqual(heuristic.Amendment.PrefixRule, want) {
		t.Fatalf("heuristics allow amendment = %#v, want %#v", heuristic.Amendment.PrefixRule, want)
	}

	policyAllowed := RequirementFromEvaluation(
		[]string{"go", "test"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionAllow, MatchedRules: []RuleMatch{{Decision: DecisionAllow, IsPolicy: true, Prefix: []string{"go"}}}},
		nil,
		true,
	)
	if policyAllowed.Amendment != nil {
		t.Fatalf("policy allow amendment = %#v, want none", policyAllowed.Amendment)
	}

	ignored := RequirementFromEvaluation(
		[]string{"go", "test"},
		ApprovalPolicy{Mode: ApprovalOnRequest},
		Evaluation{Decision: DecisionAllow, MatchedRules: []RuleMatch{{Decision: DecisionAllow, Prefix: []string{"go", "test"}}}},
		nil,
		false,
	)
	if ignored.Amendment != nil {
		t.Fatalf("ignored prefix rules amendment = %#v, want none", ignored.Amendment)
	}
}

// TestPromptAmendmentSurvivesPolicyAllowOnAnotherSegmentLikeRust pins Rust's
// prompt-rule suppression: only a policy prompt rule suppresses the heuristics
// amendment, so an allow rule on a different script segment must not.
func TestPromptAmendmentSurvivesPolicyAllowOnAnotherSegmentLikeRust(t *testing.T) {
	requirement := RequirementFromEvaluation(
		[]string{"bash", "-lc", "git status && npm install"},
		ApprovalPolicy{Mode: ApprovalUnlessTrusted},
		Evaluation{Decision: DecisionPrompt, MatchedRules: []RuleMatch{
			{Decision: DecisionAllow, IsPolicy: true, Prefix: []string{"git", "status"}},
			{Decision: DecisionPrompt, Prefix: []string{"npm", "install"}},
		}},
		nil,
		true,
	)
	if requirement.Amendment == nil {
		t.Fatalf("requirement = %#v, want the prompt command amendment", requirement)
	}
	if want := []string{"npm", "install"}; !reflect.DeepEqual(requirement.Amendment.PrefixRule, want) {
		t.Fatalf("amendment = %#v, want %#v", requirement.Amendment.PrefixRule, want)
	}
}

func TestBannedPrefixSuggestion(t *testing.T) {
	if !BannedPrefixSuggestion([]string{"python", "-c"}) {
		t.Fatalf("BannedPrefixSuggestion(python -c) = false, want true")
	}
	if BannedPrefixSuggestion([]string{"npm", "run", "build"}) {
		t.Fatalf("BannedPrefixSuggestion(npm run build) = true, want false")
	}
}

func TestAssessPatchSafety(t *testing.T) {
	cwd := filepath.Join("tmp", "repo")
	action := &PatchAction{Changes: []FileChange{{Kind: FileUpdate, Path: "README.md"}}}
	check := AssessPatchSafety(action, ApprovalPolicy{Mode: ApprovalOnRequest}, PermissionManaged, FileSystemPolicy{WritableRoots: []string{"."}}, cwd, "seatbelt")
	if check.Kind != SafetyAutoApprove || check.SandboxType != "seatbelt" {
		t.Fatalf("AssessPatchSafety(writable) = %#v", check)
	}
	outside := &PatchAction{Changes: []FileChange{{Kind: FileUpdate, Path: filepath.Join("..", "outside.txt")}}}
	check = AssessPatchSafety(outside, ApprovalPolicy{Mode: ApprovalNever}, PermissionManaged, FileSystemPolicy{WritableRoots: []string{"."}}, cwd, "seatbelt")
	if check.Kind != SafetyReject || check.Reason != PatchRejectedOutsideProjectReason {
		t.Fatalf("AssessPatchSafety(outside) = %#v", check)
	}
	check = AssessPatchSafety(action, ApprovalPolicy{Mode: ApprovalUnlessTrusted}, PermissionManaged, FileSystemPolicy{WritableRoots: []string{"."}}, cwd, "seatbelt")
	if check.Kind != SafetyAskUser {
		t.Fatalf("AssessPatchSafety(unless trusted) = %#v", check)
	}
}

func TestWritePatchConstrainedToWritablePathsChecksMoveDestination(t *testing.T) {
	cwd := filepath.Join("tmp", "repo")
	action := &PatchAction{Changes: []FileChange{{Kind: FileUpdate, Path: "a.txt", MovePath: filepath.Join("..", "b.txt")}}}
	if WritePatchConstrainedToWritablePaths(action, &FileSystemPolicy{WritableRoots: []string{"."}}, cwd) {
		t.Fatalf("WritePatchConstrainedToWritablePaths(move outside) = true, want false")
	}
}

func TestNetworkPolicyHelpers(t *testing.T) {
	context, ok := NetworkApprovalContextFromPayload(&NetworkDecisionPayload{Decision: "ask", Protocol: ProtocolHTTPS, Host: " example.com "})
	if !ok || context.Host != "example.com" {
		t.Fatalf("NetworkApprovalContextFromPayload() = %#v/%v", context, ok)
	}
	msg, ok := DeniedNetworkPolicyMessage(&BlockedRequest{Decision: "deny", Host: "example.com", Reason: "not_allowed"})
	if !ok || !strings.Contains(msg, "not on the allowlist") {
		t.Fatalf("DeniedNetworkPolicyMessage() = %q/%v", msg, ok)
	}
	amendment := ExecpolicyNetworkRuleAmendment(&NetworkPolicyAmendment{Action: NetworkRuleDeny}, context, "example.com")
	if amendment.Decision != DecisionForbidden || !strings.Contains(amendment.Justification, "Deny https access") {
		t.Fatalf("ExecpolicyNetworkRuleAmendment() = %#v", amendment)
	}
}

func TestPermissionProfileTags(t *testing.T) {
	if got := PermissionProfilePolicyTag(PermissionDisabled, FileSystemPolicy{}, "."); got != "danger-full-access" {
		t.Fatalf("PermissionProfilePolicyTag(disabled) = %q", got)
	}
	if got := PermissionProfilePolicyTag(PermissionManaged, FileSystemPolicy{}, "."); got != "read-only" {
		t.Fatalf("PermissionProfilePolicyTag(read-only) = %q", got)
	}
	if got := PermissionProfilePolicyTag(PermissionManaged, FileSystemPolicy{WritableRoots: []string{"."}}, "."); got != "workspace-write" {
		t.Fatalf("PermissionProfilePolicyTag(workspace-write) = %q", got)
	}
	got := []string{
		PermissionProfileSandboxTag(PermissionDisabled, true, false, "seatbelt"),
		PermissionProfileSandboxTag(PermissionExternal, true, false, "seatbelt"),
		PermissionProfileSandboxTag(PermissionManaged, false, false, "seatbelt"),
		PermissionProfileSandboxTag(PermissionManaged, true, true, "seatbelt"),
		PermissionProfileSandboxTag(PermissionManaged, true, false, "seatbelt"),
	}
	want := []string{"none", "external", "none", "windows_elevated", "seatbelt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PermissionProfileSandboxTag() = %v, want %v", got, want)
	}
}
