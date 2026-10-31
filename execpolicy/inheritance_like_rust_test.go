package execpolicy

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func inheritedHostExecutableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func inheritedAbsoluteGitPath() string {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		volume := os.Getenv("SystemDrive")
		if volume == "" {
			volume = "C:"
		}
		root = volume + string(filepath.Separator)
	}
	return filepath.Join(root, "usr", "bin", inheritedHostExecutableName("git"))
}

func inheritedPrefixRule(tokens ...[]string) PrefixRule {
	pattern := make([][]string, 0, len(tokens))
	for _, token := range tokens {
		pattern = append(pattern, token)
	}
	return PrefixRule{Pattern: pattern}
}

func inheritedRule(decision Decision, tokens ...[]string) PrefixRule {
	rule := inheritedPrefixRule(tokens...)
	rule.Decision = decision
	return rule
}

func token(value string) []string {
	return []string{value}
}

func parseManagedPolicy(t *testing.T, text string) *RequirementsPolicy {
	t.Helper()
	policy, err := ParseRequirementsPolicy("managed.rules", text)
	if err != nil {
		t.Fatalf("parse managed policy: %v", err)
	}
	return policy
}

// Mirrors Rust #58406 inherited_absolute_amendments_preserve_managed_basename_restrictions:
// an absolute-path amendment may bypass a basename rule only if its
// restriction is preserved.
func TestInheritedAbsoluteAmendmentsPreserveManagedBasenameRestrictionsLikeRust(t *testing.T) {
	managed := parseManagedPolicy(t, `prefix_rule(pattern = ["git", ["push", "send-pack"]], decision = "forbidden")`)
	git := inheritedAbsoluteGitPath()
	cases := []struct {
		name     string
		args     []string
		decision Decision
		accepted bool
	}{
		{name: "unrelated args", args: []string{"status"}, decision: DecisionAllow, accepted: true},
		{name: "bare program", args: nil, decision: DecisionAllow, accepted: false},
		{name: "denied alias", args: []string{"send-pack", "origin"}, decision: DecisionAllow, accepted: false},
		{name: "same decision", args: []string{"push"}, decision: DecisionForbidden, accepted: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := managed.Policy().Clone()
			tokens := [][]string{token(git)}
			for _, arg := range test.args {
				tokens = append(tokens, token(arg))
			}
			rule := inheritedPrefixRule(tokens...)
			rule.Decision = test.decision
			snapshot.Rules = append(snapshot.Rules, rule)
			if got := snapshot.PreservesManagedRequirements(managed); got != test.accepted {
				t.Fatalf("PreservesManagedRequirements() = %v, want %v", got, test.accepted)
			}
		})
	}
}

// Mirrors Rust #58406 inherited_absolute_amendment_respects_existing_exact_denials.
func TestInheritedAbsoluteAmendmentRespectsExistingExactDenialsLikeRust(t *testing.T) {
	git := inheritedAbsoluteGitPath()
	cases := []struct {
		name     string
		denied   []string
		accepted bool
	}{
		{name: "exact denial covers the fallback overlap", denied: []string{"push"}, accepted: true},
		{name: "narrower exact denial leaves the fallback exposed", denied: []string{"push", "origin"}, accepted: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			managed := NewRequirementsPolicy(&Policy{})
			policy := managed.Policy().Clone()
			policy.Rules = append(policy.Rules, inheritedRule(DecisionForbidden, token("git"), token("push")))
			exact := [][]string{token(git)}
			for _, arg := range test.denied {
				exact = append(exact, token(arg))
			}
			policy.Rules = append(policy.Rules, inheritedRule(DecisionForbidden, exact...))
			managed = NewRequirementsPolicy(policy)

			snapshot := managed.Policy().Clone()
			snapshot.Rules = append(snapshot.Rules, inheritedRule(DecisionAllow, token(git)))
			if got := snapshot.PreservesManagedRequirements(managed); got != test.accepted {
				t.Fatalf("PreservesManagedRequirements() = %v, want %v", got, test.accepted)
			}
		})
	}
}

// A snapshot that drops a managed rule, changes the host executable mapping, or
// grants a denied host cannot replace the destination requirements.
func TestInheritedPreservesManagedRequirementsRejectsWeakenedPoliciesLikeRust(t *testing.T) {
	hostPath := inheritedAbsoluteGitPath()
	// The policy DSL string literal treats backslashes as escapes.
	dslHostPath := filepath.ToSlash(hostPath)
	managed := parseManagedPolicy(t, "prefix_rule(pattern = [\"git\", \"push\"], decision = \"forbidden\")\n"+
		"prefix_rule(pattern = [\"curl\"], decision = \"forbidden\")\n"+
		"network_rule(host = \"blocked.example.com\", protocol = \"https\", decision = \"deny\")\n")
	managed.Policy().HostExecutables = map[string][]string{"git": {dslHostPath}}

	missing := managed.Policy().Clone()
	missing.Rules = missing.Rules[:1]
	if missing.PreservesManagedRequirements(managed) {
		t.Fatal("snapshot dropping a managed rule preserved requirements")
	}

	hostPaths := managed.Policy().Clone()
	hostPaths.HostExecutables = map[string][]string{"git": {filepath.Join(filepath.Dir(hostPath), "other-git")}}
	if hostPaths.PreservesManagedRequirements(managed) {
		t.Fatal("snapshot with a different host executable mapping preserved requirements")
	}

	networkGrant := managed.Policy().Clone()
	networkGrant.NetworkRules = append(networkGrant.NetworkRules, NetworkRule{Host: "blocked.example.com", Protocol: "https", Decision: DecisionAllow})
	if networkGrant.PreservesManagedRequirements(managed) {
		t.Fatal("snapshot granting a managed network denial preserved requirements")
	}

	if !managed.Policy().Clone().PreservesManagedRequirements(managed) {
		t.Fatal("an identical snapshot did not preserve requirements")
	}
}

// InstallInheritedExecPolicy mirrors ExecPolicyManager::from_inherited: only
// managed-only destinations with matching requirements may install a snapshot,
// and the installed policy is independent of the source.
func TestInstallInheritedExecPolicyLikeRust(t *testing.T) {
	managed := parseManagedPolicy(t, `prefix_rule(pattern = ["git", "push"], decision = "forbidden")`)
	snapshot := &InheritedExecPolicy{Policy: managed.Policy().Clone(), SourceRequirements: managed}
	destination := InheritedExecPolicyDestination{IgnoreUserAndProjectRules: true, Requirements: managed}

	installed, err := InstallInheritedExecPolicy(snapshot, destination)
	if err != nil {
		t.Fatalf("InstallInheritedExecPolicy() error = %v", err)
	}
	installed.Rules[0].Decision = DecisionAllow
	if snapshot.Policy.Rules[0].Decision != DecisionForbidden {
		t.Fatal("installed policy shares state with the transported snapshot")
	}

	_, err = InstallInheritedExecPolicy(snapshot, InheritedExecPolicyDestination{Requirements: managed})
	if err == nil || !strings.Contains(err.Error(), "managed-only destination") {
		t.Fatalf("InstallInheritedExecPolicy(user rules) error = %v", err)
	}
	_, err = InstallInheritedExecPolicy(snapshot, InheritedExecPolicyDestination{
		IgnoreUserAndProjectRules: true,
		PolicyFiles:               []string{filepath.Join("rules", "extra.rules")},
		Requirements:              managed,
	})
	if err == nil || !strings.Contains(err.Error(), "managed-only destination") {
		t.Fatalf("InstallInheritedExecPolicy(policy files) error = %v", err)
	}

	other := parseManagedPolicy(t, `prefix_rule(pattern = ["git", "push"], decision = "prompt")`)
	_, err = InstallInheritedExecPolicy(snapshot, InheritedExecPolicyDestination{
		IgnoreUserAndProjectRules: true,
		Requirements:              other,
	})
	if err == nil || !strings.Contains(err.Error(), "requirements differ") {
		t.Fatalf("InstallInheritedExecPolicy(mismatched requirements) error = %v", err)
	}

	weakened := &InheritedExecPolicy{Policy: &Policy{}, SourceRequirements: managed}
	_, err = InstallInheritedExecPolicy(weakened, destination)
	if err == nil || !strings.Contains(err.Error(), "does not preserve") {
		t.Fatalf("InstallInheritedExecPolicy(weakened snapshot) error = %v", err)
	}

	open := &InheritedExecPolicy{Policy: &Policy{}}
	if _, err := InstallInheritedExecPolicy(open, InheritedExecPolicyDestination{IgnoreUserAndProjectRules: true}); err != nil {
		t.Fatalf("InstallInheritedExecPolicy(no requirements) error = %v", err)
	}
}

// Policy.Clone copies rules, examples, network rules, and host mappings so a
// transported snapshot cannot be mutated through the installed policy.
func TestPolicyCloneIsDeepLikeRust(t *testing.T) {
	source := &Policy{
		Rules: []PrefixRule{
			{
				Pattern:          [][]string{{"git"}, {"push"}},
				Decision:         DecisionForbidden,
				Justification:    "managed",
				MatchExamples:    [][]string{{"git", "push"}},
				NotMatchExamples: [][]string{{"git", "status"}},
			},
		},
		NetworkRules:    []NetworkRule{{Host: "blocked.example.com", Protocol: "https", Decision: DecisionForbidden}},
		HostExecutables: map[string][]string{"git": {inheritedAbsoluteGitPath()}},
	}
	clone := source.Clone()
	clone.Rules[0].Pattern[1][0] = "send-pack"
	clone.Rules[0].MatchExamples[0][0] = "other"
	clone.Rules[0].NotMatchExamples[0][0] = "other"
	clone.NetworkRules[0].Host = "allowed.example.com"
	clone.HostExecutables["git"][0] = "/usr/local/bin/git"
	want := &Policy{
		Rules: []PrefixRule{
			{
				Pattern:          [][]string{{"git"}, {"push"}},
				Decision:         DecisionForbidden,
				Justification:    "managed",
				MatchExamples:    [][]string{{"git", "push"}},
				NotMatchExamples: [][]string{{"git", "status"}},
			},
		},
		NetworkRules:    []NetworkRule{{Host: "blocked.example.com", Protocol: "https", Decision: DecisionForbidden}},
		HostExecutables: map[string][]string{"git": {inheritedAbsoluteGitPath()}},
	}
	if !reflect.DeepEqual(source, want) {
		t.Fatalf("source policy changed through the clone: %#v", source)
	}
}
