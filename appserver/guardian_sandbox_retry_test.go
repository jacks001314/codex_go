package appserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

// Ports Rust #54265 (sha 6fbd7be151), `core/tests/suite/guardian_sandbox_retry.rs`:
// `guardian_rechecks_unsandboxed_retry_after_sandboxed_approval`.
//
// Rust's orchestrator re-attempts a denied command unsandboxed
// (core/src/tools/orchestrator.rs:484-520) and refuses to let the first verdict
// authorize the retry: `retry_preapproved` is true only for
// `ApprovalResolutionSource::User | Hook`, the retry's approval action is
// rewritten to `SandboxPermissions::RequireEscalated`, and
// `bypass_retry_approval` is forced off under strict auto review. The retry is
// therefore reviewed on its own authority and, when denied, never reaches the
// executor (`starts.len() == 1`).
//
// Go has no orchestrator-level retry loop: the prompt tells the model to rerun a
// sandbox-denied command with `sandbox_permissions: "require_escalated"`
// (sandbox/permission_prompt_templates.go), and that rerun is a new tool call
// whose approval goes through the same reviewer. This test pins the property
// both carriers must preserve — a command approved inside the sandbox does not
// authorize the unsandboxed rerun, which needs its own verdict — at the Go
// carrier: `RuntimeRouter.shellApprovalForTurn` plus the Guardian branch
// (appserver/runtime_router.go shellApprovalForTurn).
func TestGuardianSandboxRetryNeedsItsOwnAuthorizationLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.4\"\napprovals_reviewer = \"auto_review\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	reviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:       cwd,
		Config:           config.NewConfigService(home),
		GuardianReviewer: reviewer,
	})
	defer router.Close()
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	approval := router.shellApprovalForTurn("thread-1", "turn-1", false)
	sandboxed := func() *tool.ShellApprovalRequest {
		return &tool.ShellApprovalRequest{
			Request: &tool.ShellRequest{
				HookCommand:        "touch marker",
				CWD:                cwd,
				ApprovalReason:     "requires approval",
				SandboxPermissions: sandbox.SandboxPermissionsUseDefault,
			},
			Invocation: &tool.Invocation{CallID: "sandbox-retry"},
		}
	}

	// The sandboxed attempt is approved.
	decision, err := approval(context.Background(), sandboxed())
	if err != nil {
		t.Fatalf("sandboxed approval error = %v", err)
	}
	if !decision.Approved {
		t.Fatalf("sandboxed approval = %#v, want approved", decision)
	}

	// The unsandboxed rerun is a second, independent review whose action reports
	// the escalated permissions Rust rewrites onto the retry.
	retry := sandboxed()
	retry.Request.SandboxPermissions = sandbox.SandboxPermissionsRequireEscalated
	retry.Request.Justification = "The sandbox denied the write."
	retry.Invocation = &tool.Invocation{CallID: "sandbox-retry-escalated"}
	reviewer.decision = state.DecisionDenied
	reviewer.reason = "The unsandboxed retry is not authorized."
	decision, err = approval(context.Background(), retry)
	if err != nil {
		t.Fatalf("escalated approval error = %v", err)
	}
	if decision.Approved {
		t.Fatalf("escalated retry was authorized by the sandboxed approval: %#v", decision)
	}
	// Rust asserts the tool result carries the denial rationale so the model
	// cannot treat the retry as authorized.
	if decision.DenyReason != "The unsandboxed retry is not authorized." {
		t.Fatalf("deny reason = %q, want the reviewer rationale", decision.DenyReason)
	}

	// Two reviews were requested, each with the permissions of its own attempt.
	if len(reviewer.actions) != 2 {
		t.Fatalf("guardian reviews = %d, want one per attempt", len(reviewer.actions))
	}
	if got := reviewer.actions[0].SandboxPermissions; got != string(sandbox.SandboxPermissionsUseDefault) {
		t.Fatalf("first reviewed permissions = %q, want use_default", got)
	}
	if got := reviewer.actions[1].SandboxPermissions; got != string(sandbox.SandboxPermissionsRequireEscalated) {
		t.Fatalf("retry reviewed permissions = %q, want require_escalated", got)
	}
	if !strings.Contains(reviewer.actions[1].Justification, "sandbox denied") {
		t.Fatalf("retry justification = %q, want the sandbox denial note", reviewer.actions[1].Justification)
	}

	// A Guardian verdict is not a reusable rule: Rust only lets a User/Hook
	// resolution pre-approve the retry, and Go's Guardian branch records no
	// exec-policy amendment, so nothing here can auto-approve a later attempt.
	if prefixes := router.execPolicySaved.approvedPrefixes("thread-1"); len(prefixes) != 0 {
		t.Fatalf("Guardian approval saved reusable prefixes: %#v", prefixes)
	}
}
