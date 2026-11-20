package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/apps"
	"codex_go/mcp"
)

// Execute runs an mcp_tool hook through the session's shared MCP runtime
// (Rust #39296/#39331/#55263). Dormant cached servers start on demand; the
// hook timeout covers startup and the call, and no model-tool approval or
// recursive hook dispatch is involved.
func (r *RuntimeRouter) Execute(ctx context.Context, call HookMcpCall) (HookMcpOutput, error) {
	if r == nil {
		return HookMcpOutput{}, fmt.Errorf("MCP hook executor is unavailable")
	}
	server := strings.TrimSpace(call.Server)
	toolName := strings.TrimSpace(call.Tool)
	if server == "" || toolName == "" {
		return HookMcpOutput{}, fmt.Errorf("MCP hook requires server and tool")
	}
	if err := r.validateBundledAppHookTarget(server, toolName, call); err != nil {
		return HookMcpOutput{}, err
	}
	if call.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, call.Timeout)
		defer cancel()
	}
	threadID := ""
	service := r.mcpServiceForThread(threadID, nil)
	if service == nil {
		return HookMcpOutput{}, fmt.Errorf("MCP runtime is unavailable for hook call %s.%s", server, toolName)
	}
	// Rust #39296/#39331: hook calls are restricted to already-connected,
	// cataloged, policy-allowed tools. Unavailable servers fail immediately
	// without starting or reconnecting them.
	config, ok := service.ServerConfigForServer(server)
	if !ok {
		return HookMcpOutput{}, fmt.Errorf("MCP hook server %q is not cataloged or connected", server)
	}
	if strings.TrimSpace(config.URL) == "" && strings.TrimSpace(config.Command) == "" {
		return HookMcpOutput{}, fmt.Errorf("MCP hook server %q has no configured endpoint", server)
	}
	if err := validateHookMcpEnvironment(server, config, call.EnvironmentID); err != nil {
		return HookMcpOutput{}, err
	}
	if filter := mcp.ToolFilterFromServerConfig(&config); filter != nil && !filter.Allows(toolName) {
		return HookMcpOutput{}, fmt.Errorf("MCP hook tool %q is disabled for server %q", toolName, server)
	}
	// Rust #61437: a bundled REPL cleanup has no process to clean up when the
	// configured local stdio server's startup was never requested.
	if call.SkipDormantReplCleanup && service.IsDormantPrivateStdio(server, call.EnvironmentID) {
		return HookMcpOutput{}, nil
	}
	// Rust #55263: a dormant cached server starts on demand for the hook, and
	// the hook timeout covers startup plus the call.
	if err := service.WaitForServerStartup(ctx, server, ""); err != nil {
		return HookMcpOutput{}, err
	}
	// Rust #62080: recheck admission immediately before dispatch, because a
	// policy, catalog or credential change during startup must stop the call. A
	// call that was dispatched is never retried, so this is the last gate.
	if err := r.validateBundledAppHookTarget(server, toolName, call); err != nil {
		return HookMcpOutput{}, err
	}
	var arguments map[string]any
	if len(call.Input) > 0 {
		arguments = call.Input
	}
	response, err := service.CallTool(&mcp.MCPToolCallParams{
		Server:    server,
		Tool:      toolName,
		Arguments: arguments,
		Meta:      cloneAnyMap(call.Metadata),
		Context:   ctx,
	})
	if err != nil {
		return HookMcpOutput{}, err
	}
	text := mcpHookResponseText(response)
	if response != nil && response.IsError != nil && *response.IsError {
		// Rust CoreHookMcpExecutor: an MCP error result fails the handler, so
		// its structured content is never billed.
		return HookMcpOutput{}, fmt.Errorf("MCP tool returned an error: %s", text)
	}
	return HookMcpOutput{Text: text, StructuredContent: response.StructuredContent}, nil
}

// validateBundledAppHookTarget enforces the identity a built-in bundled App
// hook was declared with (Rust #62112/#62080): the call must use the host Apps
// server without an execution environment, the registered tool catalog must
// still expose the expected connector, and the current policy must let that
// tool run without asking the user.
func (r *RuntimeRouter) validateBundledAppHookTarget(server string, toolName string, call HookMcpCall) error {
	// Ordinary hooks never reach the Apps catalog or config, so keep the lookup
	// and policy lazy: resolving them here would refresh MCP state for calls
	// that have no bundled App identity to prove (#62080).
	if strings.TrimSpace(call.ExpectedAppConnectorID) == "" {
		return nil
	}
	return validateBundledAppHookTargetWith(
		r.executorHookToolLookup(""),
		appToolPolicyEvaluatorFor(r.effectiveMCPConfigForThread("")),
		server,
		toolName,
		call,
	)
}

// validateBundledAppHookTargetWith is the dependency-injected form so the
// identity and policy rules can be checked without an MCP runtime.
func validateBundledAppHookTargetWith(lookup func(server string, tool string) *mcp.RuntimeToolInfo, evaluator *apps.AppToolPolicyEvaluator, server string, toolName string, call HookMcpCall) error {
	expected := strings.TrimSpace(call.ExpectedAppConnectorID)
	if expected == "" {
		return nil
	}
	if server != mcp.RuntimeCodexAppsMCPServerName || strings.TrimSpace(call.EnvironmentID) != "" {
		return fmt.Errorf("bundled App hook must use the host Apps server")
	}
	var info *mcp.RuntimeToolInfo
	if lookup != nil {
		info = lookup(server, toolName)
	}
	if info == nil || strings.TrimSpace(info.ConnectorID) != expected {
		return fmt.Errorf("App hook connector or approval policy does not permit automatic execution")
	}
	if !bundledAppHookRunsWithoutApproval(info, evaluator) {
		return fmt.Errorf("App hook connector or approval policy does not permit automatic execution")
	}
	return nil
}

// bundledAppHookRunsWithoutApproval mirrors Rust bundled_app_hook_connector: a
// built-in App hook carries the connector identity only while the policy still
// enables the tool and needs no user approval for the call. Hooks have no
// approval prompt, so a call that would need one must not run silently.
func bundledAppHookRunsWithoutApproval(tool *mcp.RuntimeToolInfo, evaluator *apps.AppToolPolicyEvaluator) bool {
	if tool == nil {
		return false
	}
	if evaluator == nil {
		return true
	}
	policy := appToolPolicyFor(tool, evaluator)
	if !policy.Enabled {
		return false
	}
	return !mcp.RequiresMCPToolApprovalForMode(tool.Tool.Annotations, policy.Approval)
}

func validateHookMcpEnvironment(server string, config mcp.ServerConfig, environmentID string) error {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" || config.EffectiveEnvironmentID() == environmentID {
		return nil
	}
	return fmt.Errorf(
		"MCP server `%s` is running in environment `%s`, expected `%s`",
		server,
		config.EffectiveEnvironmentID(),
		environmentID,
	)
}

func mcpHookResponseText(response *mcp.MCPToolCallResponse) string {
	if response == nil {
		return ""
	}
	parts := make([]string, 0, len(response.Content))
	for _, content := range response.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, strings.TrimSpace(content.Text))
			continue
		}
		if len(content.Raw) > 0 {
			if data, err := json.Marshal(content.Raw); err == nil {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n")
}
