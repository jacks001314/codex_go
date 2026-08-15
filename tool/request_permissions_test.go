package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRequestPermissionsExecutorApprovesAndDeniesLikeRust(t *testing.T) {
	var reviewedThread, reviewedTurn, reviewedCall, reviewedReason string
	var reviewedPermissions map[string]any
	executor := &RequestPermissionsExecutor{
		Reviewer: func(ctx context.Context, threadID, turnID, callID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
			reviewedThread = threadID
			reviewedTurn = turnID
			reviewedCall = callID
			reviewedReason = reason
			reviewedPermissions = permissions
			return RequestPermissionsDecision{Approved: true, Reason: "approved by review"}, nil
		},
	}
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName(RequestPermissionsToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"reason":"need network","permissions":{"network":{"enabled":true}}}`},
		Context:  map[string]any{"thread_id": "thread-1", "turn_id": "turn-1"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || output.Body != "approved by review" {
		t.Fatalf("output = %#v", output)
	}
	if reviewedThread != "thread-1" || reviewedTurn != "turn-1" || reviewedCall != "call-1" || reviewedReason != "need network" {
		t.Fatalf("review context = %q/%q/%q/%q", reviewedThread, reviewedTurn, reviewedCall, reviewedReason)
	}
	if network, ok := reviewedPermissions["network"].(map[string]any); !ok || network["enabled"] != true {
		t.Fatalf("permissions = %#v", reviewedPermissions)
	}

	denied := &RequestPermissionsExecutor{
		Reviewer: func(ctx context.Context, threadID, turnID, callID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
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
		Reviewer: func(ctx context.Context, threadID, turnID, callID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
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
	if err := RegisterRequestPermissionsTool(registry, func(ctx context.Context, threadID, turnID, callID, reason string, permissions map[string]any) (RequestPermissionsDecision, error) {
		return RequestPermissionsDecision{Approved: true}, nil
	}); err != nil {
		t.Fatalf("RegisterRequestPermissionsTool() error = %v", err)
	}
	if _, ok := registry.Lookup(PlainName(RequestPermissionsToolName)); !ok {
		t.Fatal("request_permissions tool missing")
	}
}
