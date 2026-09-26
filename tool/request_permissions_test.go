package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRequestPermissionsExecutorApprovesAndDeniesLikeRust(t *testing.T) {
	var reviewedThread, reviewedTurn, reviewedCall, reviewedEnvironment, reviewedReason string
	var reviewedPermissions map[string]any
	executor := &RequestPermissionsExecutor{
		Reviewer: func(ctx context.Context, threadID, turnID, callID, environmentID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
			reviewedThread = threadID
			reviewedTurn = turnID
			reviewedCall = callID
			reviewedEnvironment = environmentID
			reviewedReason = reason
			reviewedPermissions = permissions
			return RequestPermissionsDecision{Approved: true, Reason: "approved by review"}, nil
		},
	}
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"reason":"need network","environment_id":"remote","permissions":{"network":{"enabled":true}}}`},
		Context:  map[string]any{"thread_id": "thread-1", "turn_id": "turn-1"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || output.Body != "approved by review" {
		t.Fatalf("output = %#v", output)
	}
	if reviewedThread != "thread-1" || reviewedTurn != "turn-1" || reviewedCall != "call-1" || reviewedEnvironment != "remote" || reviewedReason != "need network" {
		t.Fatalf("review context = %q/%q/%q/%q/%q", reviewedThread, reviewedTurn, reviewedCall, reviewedEnvironment, reviewedReason)
	}
	if network, ok := reviewedPermissions["network"].(map[string]any); !ok || network["enabled"] != true {
		t.Fatalf("permissions = %#v", reviewedPermissions)
	}

	denied := &RequestPermissionsExecutor{
		Reviewer: func(ctx context.Context, threadID, turnID, callID, environmentID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
			return RequestPermissionsDecision{Approved: false, Reason: "blocked by policy"}, nil
		},
	}
	_, err = denied.Execute(context.Background(), &Invocation{
		CallID:   "call-2",
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"permissions":{"fileSystem":{"read":["/etc"]}}}`},
	})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("deny error = %v", err)
	}

	missing := &RequestPermissionsExecutor{
		Reviewer: func(ctx context.Context, threadID, turnID, callID, environmentID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
			t.Fatal("reviewer should not run without permissions")
			return RequestPermissionsDecision{}, nil
		},
	}
	if _, err := missing.Execute(context.Background(), &Invocation{
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{}`},
	}); err == nil {
		t.Fatal("missing permissions should fail")
	}
}

func TestRegisterRequestPermissionsTool(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterRequestPermissionsTool(registry, func(ctx context.Context, threadID, turnID, callID, environmentID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
		return RequestPermissionsDecision{Approved: true}, nil
	}); err != nil {
		t.Fatalf("RegisterRequestPermissionsTool() error = %v", err)
	}
	if _, ok := registry.Lookup(PlainName(RequestPermissionsToolName)); !ok {
		t.Fatal("request_permissions tool missing")
	}
}

// Mirrors Rust's `create_request_permissions_tool` / `
// request_permissions_tool_description` (shell_spec.rs): the schema declares
// `environment_id` with its environment-context guidance and only `permissions`
// is required.
func TestRequestPermissionsToolSpecDeclaresEnvironmentLikeRust(t *testing.T) {
	spec := (&RequestPermissionsExecutor{}).Spec()
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", spec.InputSchema["properties"])
	}
	environment, ok := properties["environment_id"].(map[string]any)
	if !ok || environment["type"] != "string" ||
		environment["description"] != "Environment id from <environment_context>. Omit to use the primary environment." {
		t.Fatalf("environment_id schema = %#v", properties["environment_id"])
	}
	required, ok := spec.InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "permissions" {
		t.Fatalf("required = %#v", spec.InputSchema["required"])
	}
	if !strings.Contains(spec.Description, "Use environment_id to target a specific attached environment; omit it to use the primary environment.") {
		t.Fatalf("description = %q", spec.Description)
	}
}
