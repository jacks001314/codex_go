package tool

import (
	"context"
	"strings"
	"testing"
)

// TestRequestPermissionsTargetsSelectedEnvironmentLikeRust covers the
// per-environment resolution of request_permissions (Rust #20647
// `78421face0`, "Route process tools to selected environments"; Rust #25858
// `e29071e4c9`, "Add environmentId to request_permissions").
//
// Rust resolves the call's environment through `resolve_tool_environment`
// (an explicit id must be a *ready* `turn_environments()` entry) and hands the
// selected `turn_environment.selection()` to
// `session.request_permissions_for_environment`
// (codex-rs/core/src/tools/handlers/request_permissions.rs:71-105). The Go
// handler must resolve against the turn's real executors, so an explicit or
// implicit primary that is a remote environment targets that environment
// instead of reporting the shared waiting message.
//
// Compared with the Rust tests `guardian_reviews_target_environment_and_reuses_prefix`
// (codex-rs/core/src/tools/handlers/guardian_environments_tests.rs) and
// `remote_request_permissions_grant_unblocks_later_remote_exec`
// (codex-rs/core/tests/suite/remote_env.rs:3981).
func TestRequestPermissionsTargetsSelectedEnvironmentLikeRust(t *testing.T) {
	provider := NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           "/remote/workspace",
		ExecServerURL: "ws://127.0.0.1:1/exec",
	}}, "/local/workspace")
	executor := &RequestPermissionsExecutor{
		EnvironmentCheck: &UnifiedExecEnvironmentCheck{
			SelectedEnvironmentIDs: []string{"remote-x"},
			ReadyEnvironmentCount:  1,
			StableEnvironmentTools: true,
		},
		EnvironmentFileSystems: provider,
	}

	executeReviewPermissionsLikeRust(t, executor, `{"environment_id":"remote-x","permissions":{"network":{"enabled":true}}}`, "remote-x")
	// No environment_id: Rust's primary() is the first ready environment, so the
	// implicit call targets the remote executor rather than the local id.
	executeReviewPermissionsLikeRust(t, executor, `{"permissions":{"network":{"enabled":true}}}`, "remote-x")
	executeReviewPermissionsLikeRust(t, executor, `{"environment_id":"local","permissions":{"network":{"enabled":true}}}`, "local")

	_, err := executor.Execute(context.Background(), &Invocation{
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"environment_id":"never-selected","permissions":{"network":{"enabled":true}}}`},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown turn environment id `never-selected`") {
		t.Fatalf("unselected environment error = %v", err)
	}
}

func executeReviewPermissionsLikeRust(t *testing.T, executor *RequestPermissionsExecutor, arguments string, wantEnvironment string) {
	t.Helper()
	reviewed := ""
	executor.Reviewer = func(_ context.Context, _, _, _, environmentID, _ string, _ map[string]any) (RequestPermissionsDecision, error) {
		reviewed = environmentID
		return RequestPermissionsDecision{Approved: true}, nil
	}
	if _, err := executor.Execute(context.Background(), &Invocation{
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: arguments},
	}); err != nil {
		t.Fatalf("Execute(%s) error = %v", arguments, err)
	}
	if reviewed != wantEnvironment {
		t.Fatalf("Execute(%s) reviewed environment = %q, want %q", arguments, reviewed, wantEnvironment)
	}
}
