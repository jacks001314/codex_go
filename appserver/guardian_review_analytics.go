package appserver

import (
	"context"
	"encoding/json"
	"strings"

	"codex_go/telemetry"
)

// emitGuardianReviewAnalytics mirrors Rust's codex_guardian_review event. The
// generic codex_review_event describes the approval outcome; Guardian's causal
// root and session attribution belong to this separate event.
func (r *RuntimeRouter) emitGuardianReviewAnalytics(ctx context.Context, notification *ItemGuardianApprovalReviewCompletedNotification) {
	if r == nil || notification == nil || r.services.Analytics == nil || r.threadAnalyticsDisabled(notification.ThreadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.GuardianReviewEventSink)
	if !ok {
		return
	}
	active := r.activeRuntimeTurnStateSnapshot(notification.ThreadID, notification.TurnID)
	if active == nil || strings.TrimSpace(active.ConnectionID) == "" {
		return
	}
	client, ok := r.analyticsAppServerClient(active.ConnectionID)
	if !ok {
		return
	}
	if record := r.threadRecordForAnalytics(notification.ThreadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	lineage := r.responsesMetadataLineage(notification.ThreadID)
	if ctx == nil {
		ctx = context.Background()
	}

	decision, terminalStatus, failureReason, outcome := guardianReviewAnalyticsDecision(notification.Review.Status)
	approvalSource := "main_turn"
	if strings.TrimSpace(lineage.SubagentKind) != "" || strings.TrimSpace(lineage.ParentThreadID) != "" {
		approvalSource = "delegated_subagent"
	}
	completedAtMS := notification.CompletedAtMS
	completedAt := completedAtMS / 1_000
	latency := guardianV2DurationMS(notification.StartedAtMS, completedAtMS)
	event := telemetry.NewGuardianReviewEvent(telemetry.GuardianReviewEventParams{
		SessionID:               firstNonEmpty(lineage.SessionID, notification.ThreadID),
		AppServerClient:         client,
		Runtime:                 telemetry.CurrentRuntimeMetadata(),
		GuardianContextMode:     stringPtrIfNotEmpty(notification.guardianContextMode),
		ThreadID:                strings.TrimSpace(notification.ThreadID),
		TurnID:                  strings.TrimSpace(notification.TurnID),
		RootTurnID:              notification.guardianReviewedActionRootTurnID,
		ReviewID:                strings.TrimSpace(notification.ReviewID),
		TargetItemID:            notification.TargetItemID,
		ApprovalRequestSource:   approvalSource,
		ReviewedAction:          guardianReviewedActionAnalytics(&notification.Action),
		ReviewedActionTruncated: false,
		Decision:                decision,
		TerminalStatus:          terminalStatus,
		FailureReason:           failureReason,
		AttemptCount:            1,
		RiskLevel:               guardianAnalyticsRiskLevel(notification.Review.RiskLevel),
		UserAuthorization:       guardianAnalyticsUserAuthorization(notification.Review.UserAuthorization),
		Outcome:                 outcome,
		GuardianModelProviderID: r.guardianModelProviderIDPtr(),
		ReviewTimeoutMS:         0,
		CompletionLatencyMS:     &latency,
		StartedAt:               notification.StartedAtMS / 1_000,
		CompletedAt:             &completedAt,
	})
	sink.TrackCodexGuardianReviewEvent(ctx, event)
}

func guardianReviewAnalyticsDecision(status GuardianApprovalReviewStatus) (decision, terminalStatus string, failureReason, outcome *string) {
	switch status {
	case GuardianApprovalReviewApproved:
		return "approved", "approved", nil, stringPtrIfNotEmpty("allow")
	case GuardianApprovalReviewDenied:
		return "denied", "denied", nil, stringPtrIfNotEmpty("deny")
	case GuardianApprovalReviewTimedOut:
		return "denied", "timed_out", stringPtrIfNotEmpty("timeout"), stringPtrIfNotEmpty("deny")
	case GuardianApprovalReviewAborted:
		return "aborted", "aborted", stringPtrIfNotEmpty("cancelled"), nil
	default:
		return "denied", "failed_closed", nil, nil
	}
}

func guardianAnalyticsRiskLevel(value *GuardianRiskLevel) *string {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return nil
	}
	return stringPtrIfNotEmpty(string(*value))
}

func guardianAnalyticsUserAuthorization(value *GuardianUserAuthorization) *string {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return nil
	}
	return stringPtrIfNotEmpty(string(*value))
}

func guardianReviewedActionAnalytics(action *GuardianApprovalReviewAction) json.RawMessage {
	if action == nil {
		return json.RawMessage(`{"type":"native_tool_call"}`)
	}
	actionType := guardianReviewActionType(action)
	var value any
	switch actionType {
	case "command":
		source := "shell"
		if action.Source == GuardianCommandSourceUnifiedExec {
			source = "unified_exec"
		}
		value = struct {
			Type                  string `json:"type"`
			SandboxPermissions    string `json:"sandbox_permissions"`
			AdditionalPermissions any    `json:"additional_permissions"`
		}{Type: source, SandboxPermissions: "use_default", AdditionalPermissions: guardianAnalyticsAdditionalPermissions(action.Permissions)}
	case "writeStdin":
		value = struct {
			Type string `json:"type"`
			TTY  bool   `json:"tty"`
		}{Type: "write_stdin"}
	case "execve":
		source := "shell"
		if action.Source == GuardianCommandSourceUnifiedExec {
			source = "unified_exec"
		}
		value = struct {
			Type                  string `json:"type"`
			Source                string `json:"source"`
			AdditionalPermissions any    `json:"additional_permissions"`
		}{Type: "execve", Source: source, AdditionalPermissions: guardianAnalyticsAdditionalPermissions(action.Permissions)}
	case "applyPatch":
		value = struct {
			Type string `json:"type"`
		}{Type: "apply_patch"}
	case "networkAccess":
		value = struct {
			Type     string `json:"type"`
			Protocol string `json:"protocol"`
			Port     uint16 `json:"port"`
		}{Type: "network_access", Protocol: strings.ToLower(strings.TrimSpace(string(action.Protocol))), Port: action.Port}
	case "mcpToolCall":
		value = struct {
			Type          string  `json:"type"`
			Server        string  `json:"server"`
			ToolName      string  `json:"tool_name"`
			ConnectorID   *string `json:"connector_id"`
			ConnectorName *string `json:"connector_name"`
			ToolTitle     *string `json:"tool_title"`
		}{
			Type: "mcp_tool_call", Server: action.Server, ToolName: action.ToolName,
			ConnectorID: action.ConnectorID, ConnectorName: action.ConnectorName, ToolTitle: action.ToolTitle,
		}
	case "requestPermissions":
		value = struct {
			Type string `json:"type"`
		}{Type: "request_permissions"}
	default:
		value = struct {
			Type string `json:"type"`
		}{Type: "native_tool_call"}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"type":"native_tool_call"}`)
	}
	return json.RawMessage(encoded)
}

func guardianAnalyticsAdditionalPermissions(permissions *RequestPermissionProfile) any {
	if permissions == nil || permissions.Network == nil {
		return nil
	}
	return struct {
		Network struct {
			Enabled *bool `json:"enabled"`
		} `json:"network"`
	}{Network: struct {
		Enabled *bool `json:"enabled"`
	}{Enabled: permissions.Network.Enabled}}
}
