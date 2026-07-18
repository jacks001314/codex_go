package chatwidget

import "strings"

type ReplayKind string

const (
	ReplayNone                  ReplayKind = ""
	ReplayResumeInitialMessages ReplayKind = "resume_initial_messages"
	ReplayHistory               ReplayKind = "history"
	ReplayThreadSnapshot        ReplayKind = "thread_snapshot"
)

type ServerNotificationKind string

const (
	NotificationThreadTokenUsageUpdated     ServerNotificationKind = "thread_token_usage_updated"
	NotificationThreadNameUpdated           ServerNotificationKind = "thread_name_updated"
	NotificationThreadGoalUpdated           ServerNotificationKind = "thread_goal_updated"
	NotificationThreadGoalCleared           ServerNotificationKind = "thread_goal_cleared"
	NotificationThreadSettingsUpdated       ServerNotificationKind = "thread_settings_updated"
	NotificationTurnStarted                 ServerNotificationKind = "turn_started"
	NotificationTurnCompleted               ServerNotificationKind = "turn_completed"
	NotificationItemStarted                 ServerNotificationKind = "item_started"
	NotificationItemCompleted               ServerNotificationKind = "item_completed"
	NotificationAgentMessageDelta           ServerNotificationKind = "agent_message_delta"
	NotificationPlanDelta                   ServerNotificationKind = "plan_delta"
	NotificationReasoningSummaryTextDelta   ServerNotificationKind = "reasoning_summary_text_delta"
	NotificationReasoningTextDelta          ServerNotificationKind = "reasoning_text_delta"
	NotificationTerminalInteraction         ServerNotificationKind = "terminal_interaction"
	NotificationCommandExecutionOutputDelta ServerNotificationKind = "command_execution_output_delta"
	NotificationFileChangeOutputDelta       ServerNotificationKind = "file_change_output_delta"
	NotificationTurnDiffUpdated             ServerNotificationKind = "turn_diff_updated"
	NotificationTurnPlanUpdated             ServerNotificationKind = "turn_plan_updated"
	NotificationHookStarted                 ServerNotificationKind = "hook_started"
	NotificationHookCompleted               ServerNotificationKind = "hook_completed"
	NotificationError                       ServerNotificationKind = "error"
	NotificationSkillsChanged               ServerNotificationKind = "skills_changed"
	NotificationModelSafetyBufferingUpdated ServerNotificationKind = "model_safety_buffering_updated"
	NotificationWarning                     ServerNotificationKind = "warning"
	NotificationGuardianWarning             ServerNotificationKind = "guardian_warning"
	NotificationDeprecationNotice           ServerNotificationKind = "deprecation_notice"
	NotificationConfigWarning               ServerNotificationKind = "config_warning"
	NotificationMcpServerStatusUpdated      ServerNotificationKind = "mcp_server_status_updated"
	NotificationGuardianReviewStarted       ServerNotificationKind = "item_guardian_approval_review_started"
	NotificationGuardianReviewCompleted     ServerNotificationKind = "item_guardian_approval_review_completed"
	NotificationThreadClosed                ServerNotificationKind = "thread_closed"
	NotificationShutdownComplete            ServerNotificationKind = "shutdown_complete"
)

type ProtocolNotification struct {
	Kind           ServerNotificationKind
	ThreadID       string
	TurnID         string
	WillRetry      bool
	GuardianID     string
	GuardianStatus GuardianAssessmentStatus
	GuardianAction GuardianAssessmentAction
	UnifiedDiff    string
	Summary        string
	Details        string
}

type ProtocolNotificationDecision struct {
	Handle                   bool
	FromReplay               bool
	ResumeInitialReplay      bool
	RestoreRetryStatusHeader bool
	Reason                   string
}

func DecideProtocolNotification(currentThreadID string, notification ProtocolNotification, replay ReplayKind) ProtocolNotificationDecision {
	currentThreadID = strings.TrimSpace(currentThreadID)
	notificationThreadID := strings.TrimSpace(notification.ThreadID)
	if notification.Kind == NotificationMcpServerStatusUpdated && currentThreadID != "" && notificationThreadID != "" && currentThreadID != notificationThreadID {
		return ProtocolNotificationDecision{Reason: "misrouted_mcp_status"}
	}
	fromReplay := replay != ReplayNone
	resumeInitial := replay == ReplayResumeInitialMessages
	isRetryError := notification.Kind == NotificationError && notification.WillRetry
	return ProtocolNotificationDecision{
		Handle:                   true,
		FromReplay:               fromReplay,
		ResumeInitialReplay:      resumeInitial,
		RestoreRetryStatusHeader: !resumeInitial && !isRetryError,
	}
}

func NotificationStartsTurn(kind ServerNotificationKind) bool {
	return kind == NotificationTurnStarted
}

func NotificationCompletesTurn(kind ServerNotificationKind) bool {
	return kind == NotificationTurnCompleted || kind == NotificationThreadClosed
}
