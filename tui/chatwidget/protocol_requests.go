package chatwidget

import "strings"

type ServerRequestKind string

const (
	RequestCommandExecutionApproval ServerRequestKind = "command_execution_request_approval"
	RequestFileChangeApproval       ServerRequestKind = "file_change_request_approval"
	RequestMcpServerElicitation     ServerRequestKind = "mcp_server_elicitation_request"
	RequestPermissionsApproval      ServerRequestKind = "permissions_request_approval"
	RequestToolUserInput            ServerRequestKind = "tool_request_user_input"
	RequestDynamicToolCall          ServerRequestKind = "dynamic_tool_call"
	RequestAttestationGenerate      ServerRequestKind = "attestation_generate"
	RequestCurrentTimeRead          ServerRequestKind = "current_time_read"
	RequestChatGPTAuthRefresh       ServerRequestKind = "chatgpt_auth_tokens_refresh"
	RequestApplyPatchApprovalLegacy ServerRequestKind = "apply_patch_approval"
	RequestExecApprovalLegacy       ServerRequestKind = "exec_command_approval"
)

type RequestSurface string

const (
	RequestSurfaceExecApproval       RequestSurface = "exec_approval"
	RequestSurfaceFileChangeApproval RequestSurface = "file_change_approval"
	RequestSurfaceElicitation        RequestSurface = "elicitation"
	RequestSurfacePermissions        RequestSurface = "permissions"
	RequestSurfaceUserInput          RequestSurface = "user_input"
	RequestSurfaceUnsupported        RequestSurface = "unsupported"
)

type ProtocolRequest struct {
	ID         string
	Kind       ServerRequestKind
	ThreadID   string
	TurnID     string
	CallID     string
	ServerName string
	RequestID  string
}

type ProtocolRequestDecision struct {
	Surface RequestSurface
	Queue   *QueuedInterrupt
	Error   string
}

type ProtocolNotificationRoute string

const (
	ProtocolNotificationRouteDefault           ProtocolNotificationRoute = "default"
	ProtocolNotificationRouteGuardianReview    ProtocolNotificationRoute = "guardian_review"
	ProtocolNotificationRouteShutdownComplete  ProtocolNotificationRoute = "shutdown_complete"
	ProtocolNotificationRouteTurnDiff          ProtocolNotificationRoute = "turn_diff"
	ProtocolNotificationRouteDeprecationNotice ProtocolNotificationRoute = "deprecation_notice"
	ProtocolNotificationRouteIgnored           ProtocolNotificationRoute = "ignored"
)

type ProtocolNotificationRouteDecision struct {
	Route                ProtocolNotificationRoute
	GuardianAssessment   *GuardianAssessmentEvent
	RequestImmediateExit bool
	RefreshStatusLine    bool
	HistorySummary       string
	HistoryDetails       string
	SuppressedByReplay   bool
}

func ClassifyProtocolRequest(request ProtocolRequest, replay ReplayKind) ProtocolRequestDecision {
	switch request.Kind {
	case RequestCommandExecutionApproval:
		return ProtocolRequestDecision{Surface: RequestSurfaceExecApproval, Queue: &QueuedInterrupt{Kind: QueuedInterruptExecApproval, ID: firstNonEmptyRequestID(request.ID, request.CallID)}}
	case RequestFileChangeApproval:
		return ProtocolRequestDecision{Surface: RequestSurfaceFileChangeApproval, Queue: &QueuedInterrupt{Kind: QueuedInterruptApplyPatchApproval, ID: firstNonEmptyRequestID(request.ID, request.CallID)}}
	case RequestMcpServerElicitation:
		return ProtocolRequestDecision{Surface: RequestSurfaceElicitation, Queue: &QueuedInterrupt{Kind: QueuedInterruptElicitation, ID: request.ID, ServerName: request.ServerName, RequestID: request.RequestID}}
	case RequestPermissionsApproval:
		return ProtocolRequestDecision{Surface: RequestSurfacePermissions, Queue: &QueuedInterrupt{Kind: QueuedInterruptRequestPermissions, ID: firstNonEmptyRequestID(request.ID, request.CallID)}}
	case RequestToolUserInput:
		return ProtocolRequestDecision{Surface: RequestSurfaceUserInput, Queue: &QueuedInterrupt{Kind: QueuedInterruptRequestUserInput, ID: firstNonEmptyRequestID(request.ID, request.CallID)}}
	default:
		if replay == ReplayNone {
			return ProtocolRequestDecision{Surface: RequestSurfaceUnsupported, Error: "This request type is not supported by the TUI yet."}
		}
		return ProtocolRequestDecision{Surface: RequestSurfaceUnsupported}
	}
}

func ClassifyProtocolNotification(notification ProtocolNotification, replay ReplayKind) ProtocolNotificationRouteDecision {
	if ShouldSuppressDuringReplay(replay, notification.Kind) {
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteIgnored, SuppressedByReplay: true}
	}
	switch notification.Kind {
	case NotificationGuardianReviewStarted:
		status := notification.GuardianStatus
		if status == "" {
			status = GuardianAssessmentInProgress
		}
		event := GuardianAssessmentEvent{
			ID:     firstNonEmptyRequestID(notification.GuardianID, notification.TurnID),
			Status: status,
			Action: notification.GuardianAction,
		}
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteGuardianReview, GuardianAssessment: &event}
	case NotificationGuardianReviewCompleted:
		status := notification.GuardianStatus
		if status == "" {
			status = GuardianAssessmentDenied
		}
		event := GuardianAssessmentEvent{
			ID:     firstNonEmptyRequestID(notification.GuardianID, notification.TurnID),
			Status: status,
			Action: notification.GuardianAction,
		}
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteGuardianReview, GuardianAssessment: &event}
	case NotificationShutdownComplete:
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteShutdownComplete, RequestImmediateExit: true}
	case NotificationTurnDiffUpdated:
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteTurnDiff, RefreshStatusLine: true}
	case NotificationDeprecationNotice:
		return ProtocolNotificationRouteDecision{
			Route:          ProtocolNotificationRouteDeprecationNotice,
			HistorySummary: notification.Summary,
			HistoryDetails: notification.Details,
		}
	default:
		return ProtocolNotificationRouteDecision{Route: ProtocolNotificationRouteDefault}
	}
}

func firstNonEmptyRequestID(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
