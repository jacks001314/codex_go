package appserver

import (
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
)

const bundledAppHookTestConnector = "connector_openai_codex_dream_memory"

func bundledAppHookTestTool(annotations *mcp.RuntimeToolAnnotations) *mcp.RuntimeToolInfo {
	return &mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: bundledAppHookTestConnector,
		Tool: mcp.RuntimeTool{
			Name:        "codex_dream_memory.get_mec",
			Title:       "Get memory",
			Annotations: annotations,
		},
	}
}

func bundledAppBool(value bool) *bool { return &value }

func bundledAppHookTestEvaluator(values map[string]any) *apps.AppToolPolicyEvaluator {
	return appToolPolicyEvaluatorFor(&config.Config{Values: values})
}

// appsDefaultPolicy scopes one approval mode to every App tool, as a user does
// with `[apps._default]`.
func appsDefaultPolicy(mode string) map[string]any {
	return map[string]any{"apps": map[string]any{
		"_default": map[string]any{"default_tools_approval_mode": mode},
	}}
}

// appsToolPolicy overrides the policy of the bundled hook's own tool.
func appsToolPolicy(policy map[string]any) map[string]any {
	return map[string]any{"apps": map[string]any{
		bundledAppHookTestConnector: map[string]any{
			"tools": map[string]any{"codex_dream_memory.get_mec": policy},
		},
	}}
}

// TestBundledAppHookRequiresAutomaticExecutionPolicyLikeRust mirrors Rust
// bundled_app_hook_connector (#62080): a built-in App hook may dispatch without
// asking only while the policy enables the tool and needs no user approval for
// the call. Hooks have no approval prompt, so anything else must be refused.
func TestBundledAppHookRequiresAutomaticExecutionPolicyLikeRust(t *testing.T) {
	readOnly := &mcp.RuntimeToolAnnotations{ReadOnlyHint: bundledAppBool(true)}
	writeTool := &mcp.RuntimeToolAnnotations{DestructiveHint: bundledAppBool(true)}
	cases := []struct {
		name        string
		values      map[string]any
		annotations *mcp.RuntimeToolAnnotations
		wantAllowed bool
	}{
		{
			name:        "auto mode with a safe tool runs",
			values:      appsDefaultPolicy("auto"),
			annotations: readOnly,
			wantAllowed: true,
		},
		{
			name:        "auto mode still needs approval for a destructive tool",
			values:      appsDefaultPolicy("auto"),
			annotations: writeTool,
			wantAllowed: false,
		},
		{
			name:        "prompt mode never dispatches silently",
			values:      appsDefaultPolicy("prompt"),
			annotations: readOnly,
			wantAllowed: false,
		},
		{
			name:        "writes mode allows a read-only tool",
			values:      appsDefaultPolicy("writes"),
			annotations: readOnly,
			wantAllowed: true,
		},
		{
			name:        "writes mode refuses a writing tool",
			values:      appsDefaultPolicy("writes"),
			annotations: writeTool,
			wantAllowed: false,
		},
		{
			name:        "approve mode dispatches automatically",
			values:      appsToolPolicy(map[string]any{"enabled": true, "approval_mode": "approve"}),
			annotations: writeTool,
			wantAllowed: true,
		},
		{
			name:        "a disabled tool never dispatches",
			values:      appsToolPolicy(map[string]any{"enabled": false}),
			annotations: readOnly,
			wantAllowed: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := bundledAppHookTestTool(testCase.annotations)
			got := bundledAppHookRunsWithoutApproval(tool, bundledAppHookTestEvaluator(testCase.values))
			if got != testCase.wantAllowed {
				t.Fatalf("bundledAppHookRunsWithoutApproval() = %v, want %v", got, testCase.wantAllowed)
			}
			call := HookMcpCall{Server: mcp.RuntimeCodexAppsMCPServerName, Tool: tool.Tool.Name, ExpectedAppConnectorID: bundledAppHookTestConnector}
			lookup := func(string, string) *mcp.RuntimeToolInfo { return tool }
			err := validateBundledAppHookTargetWith(lookup, bundledAppHookTestEvaluator(testCase.values), call.Server, call.Tool, call)
			if testCase.wantAllowed && err != nil {
				t.Fatalf("validateBundledAppHookTargetWith() error = %v", err)
			}
			if !testCase.wantAllowed && err == nil {
				t.Fatal("validateBundledAppHookTargetWith() allowed a call that needs approval")
			}
		})
	}
}

// TestBundledAppHookTargetChecksIdentityLikeRust keeps the dispatch identity
// checks that #62080 builds on: the host Apps server, no execution environment,
// and the connector the bundled declaration names.
func TestBundledAppHookTargetChecksIdentityLikeRust(t *testing.T) {
	tool := bundledAppHookTestTool(&mcp.RuntimeToolAnnotations{ReadOnlyHint: bundledAppBool(true)})
	lookup := func(string, string) *mcp.RuntimeToolInfo { return tool }
	evaluator := bundledAppHookTestEvaluator(nil)

	// A hook that declares no bundled App identity is not constrained here.
	if err := validateBundledAppHookTargetWith(lookup, evaluator, mcp.RuntimeCodexAppsMCPServerName, tool.Tool.Name, HookMcpCall{Server: mcp.RuntimeCodexAppsMCPServerName, Tool: tool.Tool.Name}); err != nil {
		t.Fatalf("validateBundledAppHookTargetWith() error = %v", err)
	}

	call := HookMcpCall{
		Server:                 mcp.RuntimeCodexAppsMCPServerName,
		Tool:                   tool.Tool.Name,
		ExpectedAppConnectorID: bundledAppHookTestConnector,
	}
	if err := validateBundledAppHookTargetWith(lookup, evaluator, call.Server, call.Tool, call); err != nil {
		t.Fatalf("validateBundledAppHookTargetWith() error = %v", err)
	}
	withEnvironment := call
	withEnvironment.EnvironmentID = "executor-1"
	if err := validateBundledAppHookTargetWith(lookup, evaluator, call.Server, call.Tool, withEnvironment); err == nil ||
		!strings.Contains(err.Error(), "host Apps server") {
		t.Fatalf("environment-scoped call error = %v, want the host Apps server error", err)
	}
	if err := validateBundledAppHookTargetWith(lookup, evaluator, "other_server", call.Tool, call); err == nil ||
		!strings.Contains(err.Error(), "host Apps server") {
		t.Fatalf("foreign server error = %v, want the host Apps server error", err)
	}
	for _, tool := range []*mcp.RuntimeToolInfo{
		bundledAppHookTestTool(&mcp.RuntimeToolAnnotations{ReadOnlyHint: bundledAppBool(true)}),
		nil,
	} {
		mismatched := tool
		if mismatched != nil {
			mismatched.ConnectorID = "connector_other"
		}
		lookup := func(string, string) *mcp.RuntimeToolInfo { return mismatched }
		if err := validateBundledAppHookTargetWith(lookup, evaluator, call.Server, call.Tool, call); err == nil ||
			!strings.Contains(err.Error(), "approval policy") {
			t.Fatalf("connector mismatch error = %v, want the approval policy error", err)
		}
	}
}
