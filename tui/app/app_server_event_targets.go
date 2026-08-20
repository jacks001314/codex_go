package app

import (
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// Rust parity subset: codex-rs/tui/src/app/app_server_event_targets.rs.

const ServerNotificationMcpServerStatusUpdated = "mcp_server_status_updated"

const (
	ServerNotificationError                               = "error"
	ServerNotificationThreadStarted                       = "thread_started"
	ServerNotificationThreadStatusChanged                 = "thread_status_changed"
	ServerNotificationThreadArchived                      = "thread_archived"
	ServerNotificationThreadDeleted                       = "thread_deleted"
	ServerNotificationThreadUnarchived                    = "thread_unarchived"
	ServerNotificationThreadNameUpdated                   = "thread_name_updated"
	ServerNotificationThreadTokenUsageUpdated             = "thread_token_usage_updated"
	ServerNotificationThreadGoalUpdated                   = "thread_goal_updated"
	ServerNotificationThreadGoalCleared                   = "thread_goal_cleared"
	ServerNotificationThreadSettingsUpdated               = "thread_settings_updated"
	ServerNotificationHookStarted                         = "hook_started"
	ServerNotificationHookCompleted                       = "hook_completed"
	ServerNotificationTurnDiffUpdated                     = "turn_diff_updated"
	ServerNotificationTurnPlanUpdated                     = "turn_plan_updated"
	ServerNotificationItemGuardianApprovalReviewStarted   = "item_guardian_approval_review_started"
	ServerNotificationItemGuardianApprovalReviewCompleted = "item_guardian_approval_review_completed"
	ServerNotificationRawResponseItemCompleted            = "raw_response_item_completed"
	ServerNotificationAgentMessageDelta                   = "agent_message_delta"
	ServerNotificationPlanDelta                           = "plan_delta"
	ServerNotificationCommandExecutionOutputDelta         = "command_execution_output_delta"
	ServerNotificationTerminalInteraction                 = "terminal_interaction"
	ServerNotificationFileChangeOutputDelta               = "file_change_output_delta"
	ServerNotificationFileChangePatchUpdated              = "file_change_patch_updated"
	ServerNotificationMcpToolCallProgress                 = "mcp_tool_call_progress"
	ServerNotificationReasoningSummaryTextDelta           = "reasoning_summary_text_delta"
	ServerNotificationReasoningSummaryPartAdded           = "reasoning_summary_part_added"
	ServerNotificationReasoningTextDelta                  = "reasoning_text_delta"
	ServerNotificationContextCompacted                    = "context_compacted"
	ServerNotificationModelRerouted                       = "model_rerouted"
	ServerNotificationModelVerification                   = "model_verification"
	ServerNotificationModelSafetyBufferingUpdated         = "model_safety_buffering_updated"
	ServerNotificationTurnModerationMetadata              = "turn_moderation_metadata"
	ServerNotificationThreadRealtimeStarted               = "thread_realtime_started"
	ServerNotificationThreadRealtimeItemAdded             = "thread_realtime_item_added"
	ServerNotificationThreadRealtimeTranscriptDelta       = "thread_realtime_transcript_delta"
	ServerNotificationThreadRealtimeTranscriptDone        = "thread_realtime_transcript_done"
	ServerNotificationThreadRealtimeOutputAudioDelta      = "thread_realtime_output_audio_delta"
	ServerNotificationThreadRealtimeSdp                   = "thread_realtime_sdp"
	ServerNotificationThreadRealtimeError                 = "thread_realtime_error"
	ServerNotificationThreadRealtimeClosed                = "thread_realtime_closed"
	ServerNotificationWarning                             = "warning"
	ServerNotificationGuardianWarning                     = "guardian_warning"
	ServerNotificationStrictReviewRequired                = "autoApprovalReview/strictReviewRequired"
	ServerNotificationSkillsChanged                       = "skills_changed"
	ServerNotificationMcpServerOauthLoginCompleted        = "mcp_server_oauth_login_completed"
	ServerNotificationAccountUpdated                      = "account_updated"
	ServerNotificationAccountRateLimitsUpdated            = "account_rate_limits_updated"
	ServerNotificationAppListUpdated                      = "app_list_updated"
	ServerNotificationRemoteControlStatusChanged          = "remote_control_status_changed"
	ServerNotificationExternalAgentConfigImportProgress   = "external_agent_config_import_progress"
	ServerNotificationExternalAgentConfigImportCompleted  = "external_agent_config_import_completed"
	ServerNotificationDeprecationNotice                   = "deprecation_notice"
	ServerNotificationConfigWarning                       = "config_warning"
	ServerNotificationFuzzyFileSearchSessionUpdated       = "fuzzy_file_search_session_updated"
	ServerNotificationFuzzyFileSearchSessionCompleted     = "fuzzy_file_search_session_completed"
	ServerNotificationCommandExecOutputDelta              = "command_exec_output_delta"
	ServerNotificationProcessOutputDelta                  = "process_output_delta"
	ServerNotificationProcessExited                       = "process_exited"
	ServerNotificationFsChanged                           = "fs_changed"
	ServerNotificationWindowsWorldWritableWarning         = "windows_world_writable_warning"
	ServerNotificationWindowsSandboxSetupCompleted        = "windows_sandbox_setup_completed"
	ServerNotificationAccountLoginCompleted               = "account_login_completed"
)

type EventTarget struct {
	ThreadID string
	TurnID   string
}

type ServerNotificationThreadTargetKind string

const (
	ServerNotificationThreadTargetThread    ServerNotificationThreadTargetKind = "thread"
	ServerNotificationThreadTargetInvalid   ServerNotificationThreadTargetKind = "invalid_thread_id"
	ServerNotificationThreadTargetAppScoped ServerNotificationThreadTargetKind = "app_scoped"
	ServerNotificationThreadTargetGlobal    ServerNotificationThreadTargetKind = "global"
)

type ServerNotificationThreadTarget struct {
	Kind     ServerNotificationThreadTargetKind
	ThreadID string
}

func ServerRequestThreadID(request *ServerRequest) (string, bool) {
	if request == nil {
		return "", false
	}
	threadID, valid := ParseAppServerThreadID(request.ThreadID)
	if !valid {
		return "", false
	}
	switch request.Kind {
	case ServerRequestCommandExecutionApproval,
		ServerRequestFileChangeApproval,
		ServerRequestUserInput,
		ServerRequestMcpElicitation,
		ServerRequestPermissionsApproval,
		"dynamic_tool_call",
		"current_time_read":
		return threadID, true
	default:
		return "", false
	}
}

func ServerNotificationThreadTargetForEvent(notification *ServerEvent) ServerNotificationThreadTarget {
	if notification == nil {
		return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetGlobal}
	}
	if serverNotificationKindIsAlwaysGlobal(notification.Name) {
		return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetGlobal}
	}
	threadID := notification.ThreadID
	if threadID == "" {
		threadID = notification.Target.ThreadID
	}
	if notification.Name == ServerNotificationMcpServerStatusUpdated && threadID == "" {
		return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetAppScoped}
	}
	if threadID == "" {
		return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetGlobal}
	}
	parsedThreadID, valid := ParseAppServerThreadID(threadID)
	if !valid {
		return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetInvalid, ThreadID: threadID}
	}
	return ServerNotificationThreadTarget{Kind: ServerNotificationThreadTargetThread, ThreadID: parsedThreadID}
}

func EventTargetFromServerEvent(event ServerEvent) EventTarget {
	target := event.Target
	if target.ThreadID == "" {
		target.ThreadID = event.ThreadID
	}
	if target.TurnID == "" {
		target.TurnID = event.TurnID
	}
	return target
}

func ValidAppServerThreadID(threadID string) bool {
	_, ok := ParseAppServerThreadID(threadID)
	return ok
}

func ParseAppServerThreadID(threadID string) (string, bool) {
	if threadID == "" || strings.ContainsFunc(threadID, unicode.IsSpace) {
		return "", false
	}
	parsed, err := uuid.Parse(threadID)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func serverNotificationKindIsAlwaysGlobal(kind string) bool {
	switch kind {
	case ServerNotificationSkillsChanged,
		ServerNotificationMcpServerOauthLoginCompleted,
		ServerNotificationAccountUpdated,
		ServerNotificationAccountRateLimitsUpdated,
		ServerNotificationAppListUpdated,
		ServerNotificationRemoteControlStatusChanged,
		ServerNotificationExternalAgentConfigImportProgress,
		ServerNotificationExternalAgentConfigImportCompleted,
		ServerNotificationDeprecationNotice,
		ServerNotificationConfigWarning,
		ServerNotificationFuzzyFileSearchSessionUpdated,
		ServerNotificationFuzzyFileSearchSessionCompleted,
		ServerNotificationCommandExecOutputDelta,
		ServerNotificationProcessOutputDelta,
		ServerNotificationProcessExited,
		ServerNotificationFsChanged,
		ServerNotificationWindowsWorldWritableWarning,
		ServerNotificationWindowsSandboxSetupCompleted,
		ServerNotificationAccountLoginCompleted:
		return true
	default:
		return false
	}
}
