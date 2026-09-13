package telemetry

import (
	"context"
	"strings"

	"codex_go/protocol"
)

// Rust parity: codex-otel's SessionTelemetry::tool_decision, which core's
// approvals module reports for every resolved approval (approvals.rs
// `record_resolution`) and for the tools whose approval requirement is skipped.

// Tool decision strings mirror codex_protocol's
// `ReviewDecision::to_opaque_string`.
const (
	ToolDecisionApproved                       = "approved"
	ToolDecisionApprovedWithAmendment          = "approved_with_amendment"
	ToolDecisionApprovedForSession             = "approved_for_session"
	ToolDecisionApprovedMCPPolicyAmendment     = "approved_mcp_policy_amendment"
	ToolDecisionApprovedWithNetworkPolicyAllow = "approved_with_network_policy_allow"
	ToolDecisionDeniedWithNetworkPolicyDeny    = "denied_with_network_policy_deny"
	ToolDecisionDenied                         = "denied"
	ToolDecisionTimedOut                       = "timed_out"
	ToolDecisionAbort                          = "abort"
)

// Tool decision sources mirror codex-otel's ToolDecisionSource.
const (
	ToolDecisionSourceAutomatedReviewer = "automated_reviewer"
	ToolDecisionSourceConfig            = "config"
	ToolDecisionSourceUser              = "user"
)

// ToolDecisionEvent mirrors SessionTelemetry::tool_decision: who decided what
// for one tool call.
type ToolDecisionEvent struct {
	ToolName      string
	ToolNamespace string
	CallID        string
	// Decision is one of the decision strings above.
	Decision string
	// Source is absent when the decision came from no tracked reviewer.
	Source string
}

// EmitToolDecision mirrors SessionTelemetry::tool_decision: a log-only record
// naming the tool, its namespace, the call, the opaque decision, and the source
// that made it.
func EmitToolDecision(ctx context.Context, telemetry *SessionTelemetry, event ToolDecisionEvent) {
	if telemetry == nil {
		return
	}
	namespace := strings.TrimSpace(event.ToolNamespace)
	if namespace == "" {
		namespace = protocol.DefaultFunctionNamespace
	}
	fields := map[string]string{
		"tool_name":      event.ToolName,
		"tool_namespace": namespace,
		"call_id":        event.CallID,
		"decision":       event.Decision,
	}
	if event.Source != "" {
		fields["source"] = event.Source
	}
	telemetry.LogEvent(ctx, "codex.tool_decision", fields, nil)
}
