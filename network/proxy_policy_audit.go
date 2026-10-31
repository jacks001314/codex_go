package network

import "log/slog"

// logProxyPolicyAudit emits the executor-local policy decision event with
// launch-local process attribution (Rust #42373). The controller's forwarded
// audit notification still uses its own trusted session metadata.
func logProxyPolicyAudit(event ProxyPolicyAuditEvent) {
	metadata := event.Metadata
	processLog := metadata.ProcessLog
	conversationID := processLog.ThreadID
	if conversationID == "" {
		conversationID = metadata.ConversationID
	}
	method := event.Method
	if method == "" {
		method = "none"
	}
	client := event.Client
	if client == "" {
		client = "unknown"
	}
	slog.Info("codex.network_proxy.policy_decision",
		"target", "codex_otel.network_proxy",
		"event.name", "codex.network_proxy.policy_decision",
		"event.timestamp", event.Timestamp,
		"launch.trace_id", processLog.LaunchTraceID,
		"launch.span_id", processLog.LaunchSpanID,
		"conversation.id", conversationID,
		"tool.call_id", processLog.ToolCallID,
		"executor.environment_id", processLog.ExecutorEnvironmentID,
		"executor.registration_id", processLog.ExecutorRegistrationID,
		"app.version", metadata.AppVersion,
		"auth_mode", metadata.AuthMode,
		"originator", metadata.Originator,
		"user.account_id", metadata.UserAccountID,
		"user.email", metadata.UserEmail,
		"terminal.type", metadata.TerminalType,
		"model", metadata.Model,
		"slug", metadata.Slug,
		"network.policy.scope", event.Scope,
		"network.policy.decision", event.Decision,
		"network.policy.source", string(event.Source),
		"network.policy.reason", event.Reason,
		"network.transport.protocol", string(event.Request.Protocol),
		"server.address", event.Request.Host,
		"server.port", event.Request.Port,
		"http.request.method", method,
		"client.address", client,
		"execution.id", event.Request.ExecutionID,
		"network.policy.override", event.PolicyOverride,
	)
}
