package appserver

import (
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/execpolicy"
)

// Mirrors Rust #58406 PreparedChildInheritance.exec_policy: a host-transported
// snapshot installs as the reserved child’s own managed policy, and the
// destination keeps its own requirement for threads that did not inherit one.
func TestHostChildInstallsInheritedExecPolicyLikeRust(t *testing.T) {
	managed := mustRequirementsPolicy(t, `prefix_rule(pattern = ["git", "push"], decision = "forbidden")`)
	service := config.NewConfigService(t.TempDir())
	service.SetRequirements(&config.ConfigRequirements{Rules: managed})
	router := NewRuntimeRouter(RuntimeServices{Config: service})

	parent := &execpolicy.Policy{Rules: []execpolicy.PrefixRule{
		{Pattern: [][]string{{"git"}, {"push"}}, Decision: execpolicy.DecisionForbidden},
		{Pattern: [][]string{{"git"}, {"status"}}, Decision: execpolicy.DecisionAllow},
	}}
	created := &agent.PreparedChildContext{
		ExecPolicy:                   execpolicy.NewRequirementsPolicy(parent),
		ExecPolicySourceRequirements: managed,
	}
	if err := router.installInheritedChildExecPolicy("child-thread", created); err != nil {
		t.Fatalf("installInheritedChildExecPolicy() error = %v", err)
	}
	inherited := router.managedCommandPolicyForThread("child-thread")
	if inherited == nil {
		t.Fatal("child thread has no managed command policy")
	}
	if evaluation := inherited.Policy().EvaluateCommand([]string{"git", "status"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionAllow {
		t.Fatalf("inherited allow rule did not reach the child: %#v", evaluation)
	}
	if evaluation := inherited.Policy().EvaluateCommand([]string{"git", "push"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionForbidden {
		t.Fatalf("managed denial was not preserved: %#v", evaluation)
	}

	// The destination keeps its own managed requirement for other threads.
	plain := router.managedCommandPolicyForThread("other-thread")
	if plain == nil {
		t.Fatal("destination requirement is missing for a non-inheriting thread")
	}
	if evaluation := plain.Policy().EvaluateCommand([]string{"git", "status"}); evaluation.Decision != nil {
		t.Fatalf("destination policy gained the inherited allow rule: %#v", evaluation)
	}

	// The installed policy is independent of the prepared snapshot.
	parent.Rules[1].Decision = execpolicy.DecisionForbidden
	if evaluation := inherited.Policy().EvaluateCommand([]string{"git", "status"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionAllow {
		t.Fatalf("installed policy shares state with the transported snapshot: %#v", evaluation)
	}

	// A grandchild prepared from the child transports the child’s installed
	// policy onward (Rust #58406 inheritance through children and
	// grandchildren).
	grandchild := &agent.PreparedChildContext{
		ExecPolicy:                   router.managedCommandPolicyForThread("child-thread"),
		ExecPolicySourceRequirements: managed,
	}
	if err := router.installInheritedChildExecPolicy("grandchild-thread", grandchild); err != nil {
		t.Fatalf("installInheritedChildExecPolicy(grandchild) error = %v", err)
	}
	if evaluation := router.managedCommandPolicyForThread("grandchild-thread").Policy().EvaluateCommand([]string{"git", "status"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionAllow {
		t.Fatalf("grandchild lost the inherited allow rule: %#v", evaluation)
	}

	router.discardInheritedExecPolicy("child-thread")
	if router.inheritedExecPolicy("child-thread") != nil {
		t.Fatal("discardInheritedExecPolicy left the installed policy behind")
	}
}

// A snapshot whose source requirements differ from the destination, or that
// weakens them, is rejected before the child starts.
func TestHostChildRejectsIncompatibleInheritedExecPolicyLikeRust(t *testing.T) {
	managed := mustRequirementsPolicy(t, `prefix_rule(pattern = ["git", "push"], decision = "forbidden")`)
	service := config.NewConfigService(t.TempDir())
	service.SetRequirements(&config.ConfigRequirements{Rules: managed})
	router := NewRuntimeRouter(RuntimeServices{Config: service})

	other := mustRequirementsPolicy(t, `prefix_rule(pattern = ["git", "push"], decision = "prompt")`)
	created := &agent.PreparedChildContext{
		ExecPolicy:                   other.Clone(),
		ExecPolicySourceRequirements: other,
	}
	err := router.installInheritedChildExecPolicy("child-thread", created)
	if err == nil || !strings.Contains(err.Error(), "requirements differ") {
		t.Fatalf("installInheritedChildExecPolicy(mismatched requirements) error = %v", err)
	}

	weakened := &agent.PreparedChildContext{
		ExecPolicy:                   execpolicy.NewRequirementsPolicy(&execpolicy.Policy{}),
		ExecPolicySourceRequirements: managed,
	}
	err = router.installInheritedChildExecPolicy("child-thread", weakened)
	if err == nil || !strings.Contains(err.Error(), "does not preserve") {
		t.Fatalf("installInheritedChildExecPolicy(weakened snapshot) error = %v", err)
	}
	if router.inheritedExecPolicy("child-thread") != nil {
		t.Fatal("a rejected snapshot was installed")
	}
}
