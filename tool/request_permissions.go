package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const RequestPermissionsToolName = "request_permissions"

// RequestPermissionsDecision is the Guardian-reviewed outcome for a
// request_permissions call (Rust #38701).
type RequestPermissionsDecision struct {
	Approved bool
	Reason   string
}

// RequestPermissionsReviewer reviews a request_permissions call through the
// shared Guardian approval path.
type RequestPermissionsReviewer func(ctx context.Context, threadID, turnID, callID string, reason string, permissions map[string]any) (RequestPermissionsDecision, error)

// RequestPermissionsExecutor implements the request_permissions tool: the
// requested permission profile is routed through the shared Guardian approval
// path and the resulting decision drives the tool output.
type RequestPermissionsExecutor struct {
	Reviewer RequestPermissionsReviewer
}

func (e *RequestPermissionsExecutor) Spec() Spec {
	return Spec{
		Name:        PlainName(RequestPermissionsToolName),
		Description: "Requests additional permissions for the current turn. The requested permission profile is reviewed automatically; the call only succeeds when the review approves it.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{"type": "string", "description": "Why the additional permissions are needed."},
				"permissions": map[string]any{
					"type":        "object",
					"description": "Requested permission profile (fileSystem/network).",
				},
			},
			"required": []string{"permissions"},
		},
	}
}

func (e *RequestPermissionsExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if invocation == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, RespondToModel("request_permissions received an unsupported payload")
	}
	if e == nil || e.Reviewer == nil {
		return nil, RespondToModel("request_permissions review is not configured")
	}
	var args struct {
		Reason      string         `json:"reason"`
		Permissions map[string]any `json:"permissions"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, RespondToModel("request_permissions arguments are invalid: " + err.Error())
	}
	if len(args.Permissions) == 0 {
		return nil, RespondToModel("request_permissions requires a permissions object")
	}
	threadID := strings.TrimSpace(invocationContextString(invocation, "thread_id"))
	turnID := strings.TrimSpace(invocationContextString(invocation, "turn_id"))
	decision, err := e.Reviewer(ctx, threadID, turnID, invocation.CallID, strings.TrimSpace(args.Reason), cloneRequestPermissions(args.Permissions))
	if err != nil {
		return nil, err
	}
	if !decision.Approved {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "The requested permissions were denied by review."
		}
		return nil, RespondToModel(reason)
	}
	body := "The requested permissions were approved."
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		body = reason
	}
	data := map[string]any{"approved": true}
	encoded, err := json.Marshal(args.Permissions)
	if err == nil {
		data["permissions"] = json.RawMessage(encoded)
	}
	return &Output{Success: true, Body: body, Data: data}, nil
}

func invocationContextString(invocation *Invocation, key string) string {
	if invocation == nil || invocation.Context == nil {
		return ""
	}
	value, _ := invocation.Context[key].(string)
	return value
}

func cloneRequestPermissions(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

var _ Executor = (*RequestPermissionsExecutor)(nil)

// RegisterRequestPermissionsTool registers the request_permissions executor
// with the given reviewer, replacing any previous registration.
func RegisterRequestPermissionsTool(registry *Registry, reviewer RequestPermissionsReviewer) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}
	return registry.Register(&RequestPermissionsExecutor{Reviewer: reviewer})
}
