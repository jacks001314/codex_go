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
	if req.Kind != ExecNeedsApproval || req.Amendment == nil {
		t.Fatalf("RequirementFromEvaluation(prompt) = %#v", req)
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
