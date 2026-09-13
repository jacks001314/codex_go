package appserver

import (
	"context"
	"strings"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/protocol"
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
			metadata.LogUserPrompts = (&config.Config{Values: read.Config}).Otel().LogUserPrompt
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
