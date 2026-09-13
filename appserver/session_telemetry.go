package appserver

import (
	"context"
	"sort"
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/protocol"
	"codex_go/sandbox"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust parity: the app-server's SessionTelemetry wiring - the per-session
// metadata (codex-otel's SessionTelemetryMetadata) and the record emitters the
// session telemetry feeds (tool_result_with_tags and the events ported beside
// their metric halves).

// sessionTelemetryForThread builds the session telemetry for one thread, with
// the log records bound to the installed OTEL logs client (Rust's log export
// layer).
func (r *RuntimeRouter) sessionTelemetryForThread(threadID string) *telemetry.SessionTelemetry {
	if r == nil {
		return nil
	}
	session := telemetry.NewSessionTelemetry(r.sessionTelemetryMetadataForThread(threadID))
	if provider := r.currentOtelProvider(); provider != nil {
		if client := provider.Logs(); client != nil {
			session.Logs = client
		}
		session.Tracer = provider.Tracer()
	}
	return session
}

// sessionTelemetryMetadataForThread mirrors the metadata Rust's SessionTelemetry
// carries for a session: the conversation identity, the transport details of
// the connection driving it, the resolved model, and the account identity. Rows
// the session does not know (for example the account identity of an API-key
// session) stay empty, the way Rust's Option fields record nothing.
func (r *RuntimeRouter) sessionTelemetryMetadataForThread(threadID string) telemetry.SessionTelemetryMetadata {
	metadata := telemetry.SessionTelemetryMetadata{
		ConversationID: strings.TrimSpace(threadID),
		AppVersion:     appServerVersion(),
	}
	if r == nil {
		return metadata
	}
	if r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{}); err == nil && read != nil {
			cfg := &config.Config{Values: read.Config}
			metadata.LogUserPrompts = cfg.Otel().LogUserPrompt
			metadata.AuthEnv = telemetry.CollectAuthEnvTelemetry(
				providerEnvKeyForSession(cfg),
				// The app-server does not enable the CODEX_API_KEY environment
				// variable (Rust's app-server passes false to shared_from_config).
				false,
			)
		}
	}
	if active := r.activeTurnForNetworkApprovalThread(threadID, ""); active != nil {
		if active.runConfig != nil {
			metadata.Model = strings.TrimSpace(active.runConfig.Model)
			metadata.Originator = strings.TrimSpace(active.runConfig.Originator)
		}
		if info := r.clientInfoForConnection(active.connectionID); info.Name != "" {
			metadata.TerminalType = info.Name
		}
	}
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		if metadata.Model == "" {
			metadata.Model = strings.TrimSpace(record.Metadata.Model)
		}
		if metadata.Originator == "" {
			metadata.Originator = strings.TrimSpace(record.Metadata.Originator)
		}
	}
	metadata.Slug = metadata.Model
	metadata.AgentName = r.agentNameForThread(threadID)
	if r.services.Account != nil {
		if snapshot := r.services.Account.AuthSnapshot(); snapshot != nil {
			metadata.AuthMode = snapshot.Mode()
			metadata.AccountID = auth.AccountIDFromAuthForRestrictions(snapshot)
			if account := auth.AccountFromAuth(snapshot); account != nil && account.Email != nil {
				metadata.AccountEmail = *account.Email
			}
		}
	}
	return metadata
}

// providerEnvKeyForSession resolves the env key of the provider a session uses
// (Rust's session provider `env_key`), or "" when the provider has none.
func providerEnvKeyForSession(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	provider, err := model.ProviderForConfigID(configValues(cfg), stringConfigValue(cfg, "model_provider"),
		stringConfigValue(cfg, "openai_base_url"))
	if err != nil || provider == nil {
		return ""
	}
	return strings.TrimSpace(provider.EnvKey)
}

// emitConversationStartsRecords mirrors SessionTelemetry::conversation_starts:
// the record a session reports when its thread starts, carrying the model and
// permission settings, the auth environment, and the enabled MCP servers.
func (r *RuntimeRouter) emitConversationStartsRecords(ctx context.Context, threadID string, cfg *config.Config, params *ThreadStartParams) {
	session := r.sessionTelemetryForThread(threadID)
	if session == nil {
		return
	}
	telemetry.EmitConversationStarts(ctx, session, r.conversationStartsEvent(cfg, params))
}

// conversationStartsEvent resolves the values the session-start record reports
// from the effective config and the thread-start request.
func (r *RuntimeRouter) conversationStartsEvent(cfg *config.Config, params *ThreadStartParams) telemetry.ConversationStartsEvent {
	event := telemetry.ConversationStartsEvent{
		ReasoningEffort:  stringConfigValue(cfg, "model_reasoning_effort"),
		ReasoningSummary: firstNonEmpty(stringConfigValue(cfg, "model_reasoning_summary"), "auto"),
	}
	providerID := stringConfigValue(cfg, "model_provider")
	cwd := ""
	if params != nil {
		providerID = firstNonEmpty(strings.TrimSpace(params.ModelProvider), providerID)
		cwd = strings.TrimSpace(params.CWD)
	}
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err == nil && provider != nil {
		event.ProviderName = firstNonEmpty(strings.TrimSpace(provider.Name), providerID)
	} else {
		event.ProviderName = providerID
	}
	event.ContextWindow = intConfigValue(cfg, "model_context_window")
	event.AutoCompactTokenLimit = intConfigValue(cfg, "model_auto_compact_token_limit")
	event.ApprovalPolicy = conversationStartsApprovalPolicy(cfg, params)
	event.SandboxPolicy = r.conversationStartsSandboxPolicy(cfg, params, cwd)
	event.MCPServers = enabledMCPServerNames(cfg)
	return event
}

// conversationStartsApprovalPolicy mirrors the resolved approval policy Rust
// reports: the request's policy when it sets one, otherwise the config value,
// otherwise the runtime's default.
func conversationStartsApprovalPolicy(cfg *config.Config, params *ThreadStartParams) string {
	var turnParams *turn.TurnStartParams
	if params != nil {
		turnParams = &turn.TurnStartParams{ApprovalPolicy: params.ApprovalPolicy}
	}
	return string(turnApprovalPolicyForTurn(cfg, turnParams))
}

// conversationStartsSandboxPolicy mirrors the legacy sandbox policy Rust's
// Display reports for the session's effective permission profile.
func (r *RuntimeRouter) conversationStartsSandboxPolicy(cfg *config.Config, params *ThreadStartParams, cwd string) string {
	var turnParams *turn.TurnStartParams
	if params != nil {
		if strings.TrimSpace(params.CWD) != "" {
			cwd = strings.TrimSpace(params.CWD)
		}
		turnParams = &turn.TurnStartParams{SandboxPolicy: params.Sandbox, Permissions: params.Permissions}
	}
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, turnParams)
	if err == nil {
		if policy := telemetrySandboxPolicy(resolution, cwd); policy != "" {
			return policy
		}
	}
	// Rust reports the SandboxMode enum default when the config selects no
	// sandbox (config_types.rs: `#[default] ReadOnly`).
	return firstNonEmpty(stringConfigValue(cfg, "sandbox_mode"), string(sandbox.SandboxReadOnly))
}

// telemetrySandboxPolicy mirrors Rust's SandboxPolicy Display names
// (kebab-case) for the session's effective permission profile; the analytics
// projection of the same profile uses snake_case names.
func telemetrySandboxPolicy(resolution *config.SandboxPermissionProfileResolution, cwd string) string {
	if resolution == nil || resolution.Profile == nil {
		return ""
	}
	profile := resolution.Profile
	if profile.Disabled {
		return "danger-full-access"
	}
	policy := profile.SandboxPolicy
	if policy == nil {
		if profile.AllowsNetwork() {
			return "danger-full-access"
		}
		return "read-only"
	}
	if policy.HasFullDiskWriteAccess() {
		if profile.AllowsNetwork() {
			return "danger-full-access"
		}
		return "external-sandbox"
	}
	if len(policy.GetWritableRootsWithCWD(cwd)) == 0 {
		return "read-only"
	}
	return "workspace-write"
}

// enabledMCPServerNames lists the configured MCP servers that are not disabled
// (Rust's effective_mcp_servers filtered by `enabled`).
func enabledMCPServerNames(cfg *config.Config) []string {
	if cfg == nil || cfg.Values == nil {
		return nil
	}
	servers, _ := cfg.Values["mcp_servers"].(map[string]any)
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name, entry := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if table, ok := entry.(map[string]any); ok {
			if enabled, ok := table["enabled"].(bool); ok && !enabled {
				continue
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// intConfigValue reads an integer config value, or nil when it is absent.
func intConfigValue(cfg *config.Config, key string) *int64 {
	if cfg == nil || cfg.Values == nil {
		return nil
	}
	switch value := cfg.Values[key].(type) {
	case int:
		converted := int64(value)
		return &converted
	case int64:
		converted := value
		return &converted
	case float64:
		converted := int64(value)
		return &converted
	default:
		return nil
	}
}

// emitUserPromptRecords mirrors SessionTelemetry::user_prompt at the point a
// turn (or a steer) accepts user input: the session metadata decides whether the
// prompt text reaches the record.
func (r *RuntimeRouter) emitUserPromptRecords(ctx context.Context, threadID string, prompt string, inputs []turn.TurnUserInput) {
	session := r.sessionTelemetryForThread(threadID)
	if session == nil {
		return
	}
	telemetry.EmitUserPrompt(ctx, session, userPromptInputs(prompt, inputs))
}

// userPromptInputs maps the request's prompt and structured inputs onto the
// input kinds the prompt record counts. Rust concatenates the text items into
// the logged prompt and counts the images by variant.
func userPromptInputs(prompt string, inputs []turn.TurnUserInput) []telemetry.UserPromptInput {
	items := make([]telemetry.UserPromptInput, 0, len(inputs)+1)
	if prompt != "" {
		items = append(items, telemetry.UserPromptInput{Kind: telemetry.UserPromptText, Text: prompt})
	}
	for _, input := range inputs {
		switch {
		case strings.TrimSpace(input.Text) != "":
			items = append(items, telemetry.UserPromptInput{Kind: telemetry.UserPromptText, Text: input.Text})
		case strings.TrimSpace(input.URL) != "":
			items = append(items, telemetry.UserPromptInput{Kind: telemetry.UserPromptImage})
		case strings.TrimSpace(input.Path) != "":
			items = append(items, telemetry.UserPromptInput{Kind: telemetry.UserPromptLocalImage})
		}
	}
	return items
}

// toolResultLogLimits resolves the `otel.tool_result` byte budget the log
// record uses (Rust's ToolResultLogConfig on the session telemetry).
func (r *RuntimeRouter) toolResultLogLimits() protocol.ToolResultLogConfig {
	if r == nil || r.services.Config == nil {
		return protocol.DefaultToolResultLogConfig()
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return protocol.DefaultToolResultLogConfig()
	}
	return (&config.Config{Values: read.Config}).Otel().ToolResult
}

// emitToolResultRecords mirrors the record half of Rust's
// SessionTelemetry::tool_result_with_tags: the diagnostic log record and the
// trace-safe span event for one completed call, beside the metrics
// emitToolCallMetrics records.
func (r *RuntimeRouter) emitToolResultRecords(ctx context.Context, threadID string, execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil || execution.Invocation == nil {
		return
	}
	// Nothing consumes the records when no OTLP logs client is installed and the
	// call is not running inside a span, so skip resolving the session identity.
	logsAvailable := false
	if provider := r.currentOtelProvider(); provider != nil && provider.Logs() != nil {
		logsAvailable = true
	}
	if !logsAvailable && telemetry.SpanFromContext(ctx) == nil {
		return
	}
	session := r.sessionTelemetryForThread(threadID)
	if session == nil {
		return
	}
	telemetry.EmitToolResult(ctx, session, r.toolResultLogLimits(), toolResultEventForExecution(execution))
}

// toolResultEventForExecution maps one completed call onto the tool-result
// event, the way Rust's registry builds ToolResultEvent from the invocation and
// the executor's result.
func toolResultEventForExecution(execution *turn.ToolExecutionResult) telemetry.ToolResultEvent {
	invocation := execution.Invocation
	event := telemetry.ToolResultEvent{
		ToolName:      strings.TrimSpace(invocation.ToolName.Name),
		ToolNamespace: strings.TrimSpace(invocation.ToolName.Namespace),
		CallID:        invocation.CallID,
		Arguments:     toolLogPayload(invocation),
		Duration:      execution.FinishedAt.Sub(execution.StartedAt),
	}
	if event.Duration < 0 {
		event.Duration = 0
	}
	if execution.Output != nil {
		event.Success = execution.Output.Success
		// Rust logs the tool's log_output: the output body, or the failure
		// message when the call reported an error.
		event.Output = firstNonEmpty(execution.Output.Body, execution.Output.Error)
	}
	event.MCPServer = strings.TrimSpace(execution.TelemetryTags["mcp_server"])
	event.MCPServerOrigin = strings.TrimSpace(execution.TelemetryTags["mcp_server_origin"])
	return event
}

// toolLogPayload mirrors codex-tools' ToolPayload::log_payload plus core's
// tool_log_payload: a direct plaintext collaboration call hides its arguments.
func toolLogPayload(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	if invocation.Source == "direct_plaintext_message" {
		return "[plaintext arguments]"
	}
	switch invocation.Payload.Kind {
	case tool.PayloadToolSearch:
		query, _ := invocation.Payload.Search["query"].(string)
		return query
	case tool.PayloadCustom:
		return invocation.Payload.Input
	default:
		return invocation.Payload.Arguments
	}
}
