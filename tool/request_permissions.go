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
// shared Guardian approval path. environmentID is the call's own environment
// when it named one (Rust `RequestPermissionsEnvironmentArgs::environment_id`).
type RequestPermissionsReviewer func(ctx context.Context, threadID, turnID, callID, environmentID, reason string, permissions map[string]any) (RequestPermissionsDecision, error)

// RequestPermissionsExecutor implements the request_permissions tool: the
// requested permission profile is routed through the shared Guardian approval
// path and the resulting decision drives the tool output.
type RequestPermissionsExecutor struct {
	Reviewer RequestPermissionsReviewer
}

func (e *RequestPermissionsExecutor) Spec() Spec {
	return Spec{
		Name:        PlainName(RequestPermissionsToolName),
		Description: "Request additional filesystem or network permissions from the user and wait for the client to grant a subset of the requested permission profile. Use environment_id to target a specific attached environment; omit it to use the primary environment. Relative filesystem paths resolve against the selected environment cwd. Granted permissions apply automatically to later shell-like commands in the current turn, or for the rest of the session if the client approves them at session scope.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{"type": "string", "description": "Why the additional permissions are needed."},
				"environment_id": map[string]any{
					"type":        "string",
					"description": "Environment id from <environment_context>. Omit to use the primary environment.",
				},
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
		Reason             string         `json:"reason"`
		EnvironmentID      *string        `json:"environment_id"`
		EnvironmentIDCamel *string        `json:"environmentId"`
		Permissions        map[string]any `json:"permissions"`
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, RespondToModel("request_permissions arguments are invalid: " + err.Error())
	}
	if len(args.Permissions) == 0 {
		return nil, RespondToModel("request_permissions requires a permissions object")
	}
	threadID := strings.TrimSpace(invocationContextString(invocation, "thread_id"))
	turnID := strings.TrimSpace(invocationContextString(invocation, "turn_id"))
	environmentID := ""
	if args.EnvironmentID != nil {
		environmentID = strings.TrimSpace(*args.EnvironmentID)
	} else if args.EnvironmentIDCamel != nil {
		environmentID = strings.TrimSpace(*args.EnvironmentIDCamel)
	}
	decision, err := e.Reviewer(ctx, threadID, turnID, invocation.CallID, environmentID, strings.TrimSpace(args.Reason), cloneRequestPermissions(args.Permissions))
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
