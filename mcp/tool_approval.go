package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"codex_go/apps"
	"codex_go/sandbox"
	"codex_go/tool"
)

// This file ports Rust's custom-MCP-server tool approval policy
// (core/src/mcp_tool_call.rs):
//
//   - custom_mcp_tool_approval_mode: the effective approval mode for a tool is
//     `mcp_servers.<server>.tools.<tool>.approval_mode`, else
//     `mcp_servers.<server>.default_tools_approval_mode`, else Auto;
//   - requires_mcp_tool_approval / requires_mcp_tool_approval_for_mode: whether
//     the mode and the tool's annotations require the user to approve a call;
//   - mcp_permission_prompt_is_auto_approved: Approve always auto-approves, and
//     the `never` policy auto-approves when the permission profile grants full
//     disk write access;
//   - the request-user-input question (and its option labels) that surfaces the
//     approval, plus the answer -> decision mapping.

// Rust mcp_tool_call.rs approval question constants.
const (
	MCPToolApprovalQuestionIDPrefix  = "mcp_tool_call_approval"
	MCPToolApprovalAccept            = "Allow"
	MCPToolApprovalAcceptForSession  = "Allow for this session"
	MCPToolApprovalAcceptAndRemember = "Allow and don't ask me again"
	MCPToolApprovalCancel            = "Cancel"

	// MCPToolApprovalHeader is Rust's fixed question header.
	MCPToolApprovalHeader = "Approve app tool call?"
)

// MCPToolApprovalDecision is Go's port of the ReviewDecision values an MCP tool
// approval can resolve to.
type MCPToolApprovalDecision int

const (
	// MCPToolApprovalDeny aborts the tool call (Rust ReviewDecision::Abort).
	MCPToolApprovalDeny MCPToolApprovalDecision = iota
	// MCPToolApprovalApprove runs the call once (Rust ReviewDecision::Approved).
	MCPToolApprovalApprove
	// MCPToolApprovalApproveForSession remembers the choice for this session
	// (Rust ReviewDecision::ApprovedForSession).
	MCPToolApprovalApproveForSession
	// MCPToolApprovalApproveAndRemember persists the choice as a config
	// amendment (Rust ReviewDecision::ApprovedMcpPolicyAmendment).
	MCPToolApprovalApproveAndRemember
	// MCPToolApprovalReject is a user *rejection* (Rust
	// ReviewDecision::Denied), which reports a different message than an
	// aborted prompt.
	MCPToolApprovalReject
)

// MCPToolApprovalDeniedMessage is the model-visible text for an aborted call
// (Rust notify_mcp_tool_call_skip with "user cancelled MCP tool call").
const MCPToolApprovalDeniedMessage = "user cancelled MCP tool call"

// MCPToolApprovalRejectedMessage is the model-visible text when the user
// explicitly rejects an MCP tool call (Rust
// parse_mcp_tool_approval_elicitation_response's decline path).
const MCPToolApprovalRejectedMessage = "user rejected MCP tool call"

// MCPToolCallBlockedByAppConfigurationMessage is Rust's model-visible text for
// a Codex Apps tool the app configuration disables
// (notify_mcp_tool_call_skip "MCP tool call blocked by app configuration").
const MCPToolCallBlockedByAppConfigurationMessage = "MCP tool call blocked by app configuration"

// MCPToolApprovalKey identifies a remembered approval. Rust keys on the server,
// the tool, and the connector/plugin identity; Go's MCP service configuration is
// already resolved per server, so the server/tool pair is exact within a thread.
type MCPToolApprovalKey struct {
	Server string
	Tool   string
}

// MCPToolApprovalPromptOptions mirrors Rust's McpToolApprovalPromptOptions after
// the elicitation gate has been folded in.
type MCPToolApprovalPromptOptions struct {
	AllowSessionRemember    bool
	AllowPersistentApproval bool
}

// MCPToolApprovalRequest is the turn-scoped context an approval handler needs to
// surface and record a decision.
type MCPToolApprovalRequest struct {
	Server          string
	Tool            string
	Arguments       any
	Annotations     *RuntimeToolAnnotations
	ConnectorID     string
	ConnectorName   string
	ToolTitle       string
	ToolDescription string
	ApprovalMode    apps.AppToolApproval
	ThreadID        string
	TurnID          string
	CallID          string
	// SessionKey is Rust's session_mcp_tool_approval_key: present only in Auto
	// mode, and the key both for session-remembered approvals and (when the
	// elicitation feature is on) for the persistent policy amendment.
	SessionKey *MCPToolApprovalKey
	// AllowSessionRemember / AllowPersistentApproval mirror Rust's
	// session_mcp_tool_approval_key / persistent_mcp_tool_approval_key (nil for
	// non-Auto modes).
	AllowSessionRemember    bool
	AllowPersistentApproval bool
}

// MCPToolApprovalHandler surfaces an MCP tool approval to the user. The app
// server implements it; the executor only decides whether one is needed.
type MCPToolApprovalHandler interface {
	ApproveMCPToolCall(ctx context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalDecision, error)
}

// MCPToolApprovalHandlerFunc adapts a function to MCPToolApprovalHandler.
type MCPToolApprovalHandlerFunc func(ctx context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalDecision, error)

// ApproveMCPToolCall implements MCPToolApprovalHandler.
func (f MCPToolApprovalHandlerFunc) ApproveMCPToolCall(ctx context.Context, request *MCPToolApprovalRequest) (MCPToolApprovalDecision, error) {
	if f == nil {
		return MCPToolApprovalDeny, nil
	}
	return f(ctx, request)
}

// ToolApprovalOptions carries the turn-scoped inputs the executor needs to gate
// a custom MCP tool call. Nil disables the gate (embedded/legacy paths).
type ToolApprovalOptions struct {
	Handler        MCPToolApprovalHandler
	ApprovalPolicy sandbox.AskForApproval
	// PermissionProfileForServer resolves the authority published for a server
	// (Rust PreparedMcpCall::permission_profile); nil leaves the profile unknown.
	PermissionProfileForServer func(server string) *sandbox.PermissionProfile
	// AppPolicy resolves the Codex Apps tool policy (Rust
	// connectors::AppToolPolicyEvaluator). Nil leaves codex_apps approvals
	// server-driven, which is how Go behaved before this port.
	AppPolicy *apps.AppToolPolicyEvaluator
}

// NormalizeAppToolApprovalMode maps the unset value to Rust's Default
// (AppToolApproval::Auto).
func NormalizeAppToolApprovalMode(mode apps.AppToolApproval) apps.AppToolApproval {
	switch strings.TrimSpace(string(mode)) {
	case "":
		return apps.AppToolApprovalAuto
	default:
		return mode
	}
}

// ToolApprovalMode ports Rust custom_mcp_tool_approval_mode's user-configured
// half: the per-tool override wins over the server default, and an unset mode is
// Auto. The MCP service already carries the effective (plugin-filtered) config,
// so plugin-contributed servers resolve through the same lookup.
func (s *MCPService) ToolApprovalMode(serverName string, toolName string) apps.AppToolApproval {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if s == nil || serverName == "" {
		return apps.AppToolApprovalAuto
	}
	config, ok := s.ServerConfigForServer(serverName)
	if !ok {
		return apps.AppToolApprovalAuto
	}
	if toolName != "" {
		if toolConfig, ok := config.Tools[toolName]; ok && toolConfig.ApprovalMode != nil {
			return NormalizeAppToolApprovalMode(*toolConfig.ApprovalMode)
		}
	}
	if config.DefaultToolsApprovalMode != nil {
		return NormalizeAppToolApprovalMode(*config.DefaultToolsApprovalMode)
	}
	return apps.AppToolApprovalAuto
}

// RequiresMCPToolApproval ports Rust requires_mcp_tool_approval: a destructive
// hint requires approval, a read-only hint does not, and otherwise the call
// requires approval unless both hints explicitly say it is safe.
func RequiresMCPToolApproval(annotations *RuntimeToolAnnotations) bool {
	if annotations == nil {
		return true
	}
	if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
		return true
	}
	if annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint {
		return false
	}
	destructive := annotations.DestructiveHint == nil || *annotations.DestructiveHint
	openWorld := annotations.OpenWorldHint == nil || *annotations.OpenWorldHint
	return destructive || openWorld
}

// RequiresMCPToolApprovalForMode ports Rust
// requires_mcp_tool_approval_for_mode.
func RequiresMCPToolApprovalForMode(annotations *RuntimeToolAnnotations, mode apps.AppToolApproval) bool {
	switch NormalizeAppToolApprovalMode(mode) {
	case apps.AppToolApprovalAuto:
		return RequiresMCPToolApproval(annotations)
	case apps.AppToolApprovalPrompt:
		return true
	case apps.AppToolApprovalWrites:
		return annotations == nil || annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint
	case apps.AppToolApprovalApprove:
		return false
	default:
		return RequiresMCPToolApproval(annotations)
	}
}

// MCPPermissionPromptAutoApproved ports Rust
// mcp_permission_prompt_is_auto_approved: Approve never prompts, and the `never`
// policy skips the prompt when the profile has full disk write access (or the
// sandbox is disabled).
func MCPPermissionPromptAutoApproved(
	policy sandbox.AskForApproval,
	profile *sandbox.PermissionProfile,
	mode apps.AppToolApproval,
) bool {
	if NormalizeAppToolApprovalMode(mode) == apps.AppToolApprovalApprove {
		return true
	}
	if policy != sandbox.ApprovalNever {
		return false
	}
	return MCPPermissionProfileGrantsFullDiskWrite(profile)
}

// MCPPermissionProfileGrantsFullDiskWrite mirrors the permission-profile half of
// Rust mcp_permission_prompt_is_auto_approved (Disabled/External/full-disk
// managed profiles).
func MCPPermissionProfileGrantsFullDiskWrite(profile *sandbox.PermissionProfile) bool {
	if profile == nil {
		return false
	}
	return profile.Disabled || profile.LegacySandboxPolicy().HasFullDiskWriteAccess()
}

// MCPSessionToolApprovalKey ports Rust session_mcp_tool_approval_key: only Auto
// mode offers a session-remembered approval.
func MCPSessionToolApprovalKey(mode apps.AppToolApproval, serverName string, toolName string) (MCPToolApprovalKey, bool) {
	if NormalizeAppToolApprovalMode(mode) != apps.AppToolApprovalAuto {
		return MCPToolApprovalKey{}, false
	}
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if serverName == "" || toolName == "" {
		return MCPToolApprovalKey{}, false
	}
	return MCPToolApprovalKey{Server: serverName, Tool: toolName}, true
}

// MCPToolApprovalPromptOptionsFor ports Rust mcp_tool_approval_prompt_options:
// the persistent option additionally requires the tool-call MCP elicitation
// feature.
func MCPToolApprovalPromptOptionsFor(
	allowSessionRemember bool,
	allowPersistentApproval bool,
	toolCallMCPElicitationEnabled bool,
) MCPToolApprovalPromptOptions {
	return MCPToolApprovalPromptOptions{
		AllowSessionRemember:    allowSessionRemember,
		AllowPersistentApproval: toolCallMCPElicitationEnabled && allowPersistentApproval,
	}
}

// MCPToolApprovalFallbackMessage ports Rust
// build_mcp_tool_approval_fallback_message.
func MCPToolApprovalFallbackMessage(server string, toolName string, connectorName string) string {
	server = strings.TrimSpace(server)
	actor := strings.TrimSpace(connectorName)
	if actor == "" {
		if IsCodexAppsMCPServerName(server) {
			actor = "this app"
		} else {
			actor = "the " + server + " MCP server"
		}
	}
	return `Allow ` + actor + ` to run tool "` + strings.TrimSpace(toolName) + `"?`
}

// BuildMCPToolApprovalQuestion ports Rust build_mcp_tool_approval_question.
func BuildMCPToolApprovalQuestion(
	questionID string,
	server string,
	toolName string,
	connectorName string,
	options MCPToolApprovalPromptOptions,
	questionOverride string,
) tool.UserInputQuestion {
	question := strings.TrimSpace(questionOverride)
	if question == "" {
		question = MCPToolApprovalFallbackMessage(server, toolName, connectorName)
	}
	question = strings.TrimRight(question, "?") + "?"

	choices := []tool.UserInputChoice{{
		Label:       MCPToolApprovalAccept,
		Description: "Run the tool and continue.",
	}}
	if options.AllowSessionRemember {
		choices = append(choices, tool.UserInputChoice{
			Label:       MCPToolApprovalAcceptForSession,
			Description: "Run the tool and remember this choice for this session.",
		})
	}
	if options.AllowPersistentApproval {
		choices = append(choices, tool.UserInputChoice{
			Label:       MCPToolApprovalAcceptAndRemember,
			Description: "Run the tool and remember this choice for future tool calls.",
		})
	}
	choices = append(choices, tool.UserInputChoice{
		Label:       MCPToolApprovalCancel,
		Description: "Cancel this tool call.",
	})

	return tool.UserInputQuestion{
		ID:       strings.TrimSpace(questionID),
		Header:   MCPToolApprovalHeader,
		Question: question,
		IsOther:  false,
		IsSecret: false,
		Options:  choices,
	}
}

// ParseMCPToolApprovalElicitationResponse ports Rust
// parse_mcp_tool_approval_elicitation_response: an accepted elicitation uses the
// requested persist mode when present, otherwise its content is read as a
// user-input answer and an unanswered accept counts as approved; a declined
// elicitation is a rejection and anything else aborts.
func ParseMCPToolApprovalElicitationResponse(
	action string,
	meta any,
	content any,
	questionID string,
) MCPToolApprovalDecision {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "accept":
		if values, ok := meta.(map[string]any); ok {
			if persist, ok := values["persist"].(string); ok {
				switch strings.ToLower(strings.TrimSpace(persist)) {
				case "session":
					return MCPToolApprovalApproveForSession
				case "always":
					return MCPToolApprovalApproveAndRemember
				}
			}
		}
		if decision := ParseMCPToolApprovalResponse(mcpToolApprovalElicitationContent(content), questionID); decision != MCPToolApprovalDeny {
			return decision
		}
		return MCPToolApprovalApprove
	case "decline":
		return MCPToolApprovalReject
	default:
		return MCPToolApprovalDeny
	}
}

// mcpToolApprovalElicitationContent ports Rust
// request_user_input_response_from_elicitation_content: an elicitation content
// object maps question ids to string or string-list answers.
func mcpToolApprovalElicitationContent(content any) *tool.UserInputResponse {
	values, ok := content.(map[string]any)
	if !ok {
		return &tool.UserInputResponse{Answers: map[string]string{}}
	}
	response := &tool.UserInputResponse{Answers: map[string]string{}}
	for key, raw := range values {
		switch typed := raw.(type) {
		case string:
			response.Answers[key] = typed
		case []string:
			if response.StructuredAnswers == nil {
				response.StructuredAnswers = map[string][]string{}
			}
			response.StructuredAnswers[key] = append([]string(nil), typed...)
		case []any:
			for _, item := range typed {
				text, ok := item.(string)
				if !ok {
					continue
				}
				if response.StructuredAnswers == nil {
					response.StructuredAnswers = map[string][]string{}
				}
				response.StructuredAnswers[key] = append(response.StructuredAnswers[key], text)
			}
		}
	}
	return response
}

// ParseMCPToolApprovalResponse ports Rust parse_mcp_tool_approval_response: an
// unanswered question aborts the call, session/persistent choices win over the
// plain accept, and any other answer cancels.
func ParseMCPToolApprovalResponse(response *tool.UserInputResponse, questionID string) MCPToolApprovalDecision {
	if response == nil {
		return MCPToolApprovalDeny
	}
	answers := []string{}
	if answer := strings.TrimSpace(response.Answers[strings.TrimSpace(questionID)]); answer != "" {
		answers = append(answers, answer)
	}
	answers = append(answers, response.StructuredAnswers[strings.TrimSpace(questionID)]...)
	if len(answers) == 0 {
		return MCPToolApprovalDeny
	}
	for _, answer := range answers {
		if strings.TrimSpace(answer) == MCPToolApprovalAcceptForSession {
			return MCPToolApprovalApproveForSession
		}
	}
	for _, answer := range answers {
		if strings.TrimSpace(answer) == MCPToolApprovalAcceptAndRemember {
			return MCPToolApprovalApproveAndRemember
		}
	}
	for _, answer := range answers {
		if strings.TrimSpace(answer) == MCPToolApprovalAccept {
			return MCPToolApprovalApprove
		}
	}
	return MCPToolApprovalDeny
}

// NormalizeMCPToolApprovalDecision ports Rust
// normalize_approval_decision_for_mode: Prompt and Writes modes never remember a
// choice.
func NormalizeMCPToolApprovalDecision(decision MCPToolApprovalDecision, mode apps.AppToolApproval) MCPToolApprovalDecision {
	switch NormalizeAppToolApprovalMode(mode) {
	case apps.AppToolApprovalPrompt, apps.AppToolApprovalWrites:
		if decision == MCPToolApprovalApproveForSession || decision == MCPToolApprovalApproveAndRemember {
			return MCPToolApprovalApprove
		}
	}
	return decision
}

// runtimeToolAnnotations decodes the raw MCP tool annotations Go keeps on the
// runtime tool info into Rust's ToolAnnotations shape.
func runtimeToolAnnotations(annotations any) *RuntimeToolAnnotations {
	if annotations == nil {
		return nil
	}
	data, err := json.Marshal(annotations)
	if err != nil {
		return nil
	}
	var out RuntimeToolAnnotations
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return &out
}

// approveToolCallIfNeeded ports the decision half of Rust
// maybe_request_mcp_tool_approval: it returns a denied tool output when the call
// must not run, and nil when the call may proceed.
func (e *ToolExecutor) approveToolCallIfNeeded(ctx context.Context, callID string, arguments any) (*tool.Output, error) {
	options := e.toolApproval
	if options == nil || options.Handler == nil {
		return nil, nil
	}
	server := e.resolvedServerName()
	toolName := e.resolvedRemoteToolName()
	annotations := runtimeToolAnnotations(e.toolInfo.Annotations)
	mode := NormalizeAppToolApprovalMode(e.mcpService().ToolApprovalMode(server, toolName))
	if IsCodexAppsMCPServerName(server) {
		if options.AppPolicy == nil {
			// The hosted server raises its own approval elicitations.
			return nil, nil
		}
		// Rust mcp_tool_call.rs: Codex Apps tools resolve their enablement and
		// approval mode from the app configuration, and a disabled tool never
		// runs.
		policy := options.AppPolicy.Policy(apps.AppToolPolicyInput{
			ConnectorID:     e.connectorID,
			ToolName:        toolName,
			ToolTitle:       e.toolInfo.Title,
			DestructiveHint: annotations.DestructiveHint,
			OpenWorldHint:   annotations.OpenWorldHint,
		})
		if !policy.Enabled {
			body := MCPToolCallBlockedByAppConfigurationMessage
			return &tool.Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       map[string]any{"server": server, "tool": toolName, "blocked": true},
				LogPreview: body,
			}, nil
		}
		mode = NormalizeAppToolApprovalMode(policy.Approval)
	}
	var profile *sandbox.PermissionProfile
	if options.PermissionProfileForServer != nil {
		profile = options.PermissionProfileForServer(server)
	}
	if MCPPermissionPromptAutoApproved(options.ApprovalPolicy, profile, mode) {
		return nil, nil
	}
	if !RequiresMCPToolApprovalForMode(annotations, mode) {
		return nil, nil
	}
	sessionKey, allowSessionRemember := MCPSessionToolApprovalKey(mode, server, toolName)
	var sessionKeyPtr *MCPToolApprovalKey
	if allowSessionRemember {
		key := sessionKey
		sessionKeyPtr = &key
	}
	decision, err := options.Handler.ApproveMCPToolCall(ctx, &MCPToolApprovalRequest{
		Server:                  server,
		Tool:                    toolName,
		Arguments:               arguments,
		Annotations:             annotations,
		ConnectorID:             e.connectorID,
		ConnectorName:           e.connectorName,
		ToolTitle:               e.toolInfo.Title,
		ToolDescription:         e.toolInfo.Description,
		ApprovalMode:            mode,
		ThreadID:                e.threadID,
		TurnID:                  e.turnID,
		CallID:                  callID,
		SessionKey:              sessionKeyPtr,
		AllowSessionRemember:    allowSessionRemember,
		AllowPersistentApproval: allowSessionRemember,
	})
	if err != nil {
		return nil, err
	}
	switch decision {
	case MCPToolApprovalApprove, MCPToolApprovalApproveForSession, MCPToolApprovalApproveAndRemember:
		// The handler records the session/persistent choice; the call proceeds.
		return nil, nil
	default:
		body := MCPToolApprovalDeniedMessage
		if decision == MCPToolApprovalReject {
			body = MCPToolApprovalRejectedMessage
		}
		return &tool.Output{
			Success:    false,
			Body:       body,
			Error:      body,
			Data:       map[string]any{"server": server, "tool": toolName, "rejected": true},
			LogPreview: body,
		}, nil
	}
}
