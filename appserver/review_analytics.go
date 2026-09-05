package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"codex_go/telemetry"
)

func (r *RuntimeRouter) handleServerRequestResolvedResponse(request *ServerRequest, response *Response) {
	if r == nil || request == nil || response == nil || response.Error != nil {
		return
	}
	switch request.Method {
	case ServerRequestCommandExecutionApproval:
		params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok || params == nil {
			return
		}
		var approvalResponse CommandExecutionRequestApprovalResponse
		if !decodeServerResponseResult(response.Result, &approvalResponse) {
			return
		}
		result := commandExecutionReviewResult(approvalResponse.Decision)
		subjectKind, subjectName := commandExecutionReviewSubject(params)
		if subjectKind != telemetry.ReviewSubjectKindNetworkAccess {
			r.recordCommandExecutionApprovalReview(params, &approvalResponse)
		}
		r.emitReviewAnalyticsEvent(context.Background(), reviewAnalyticsInput{
			ThreadID:      params.ThreadID,
			TurnID:        params.TurnID,
			ItemID:        params.ItemID,
			ReviewID:      "user:" + request.ID.String(),
			SubjectKind:   subjectKind,
			SubjectName:   subjectName,
			Reviewer:      telemetry.ReviewerUser,
			Trigger:       commandExecutionReviewTrigger(params),
			Status:        result.Status,
			Resolution:    result.Resolution,
			StartedAtMS:   params.StartedAtMS,
			CompletedAtMS: uint64(time.Now().UTC().UnixMilli()),
		})
	case ServerRequestFileChangeApproval:
		params, ok := request.Params.(*FileChangeRequestApprovalParams)
		if !ok || params == nil {
			return
		}
		var approvalResponse FileChangeRequestApprovalResponse
		if !decodeServerResponseResult(response.Result, &approvalResponse) {
			return
		}
		r.recordFileChangeApprovalReview(params, &approvalResponse)
		result := fileChangeReviewResult(approvalResponse.Decision)
		r.emitReviewAnalyticsEvent(context.Background(), reviewAnalyticsInput{
			ThreadID:      params.ThreadID,
			TurnID:        params.TurnID,
			ItemID:        params.ItemID,
			ReviewID:      "user:" + request.ID.String(),
			SubjectKind:   telemetry.ReviewSubjectKindFileChange,
			SubjectName:   "apply_patch",
			Reviewer:      telemetry.ReviewerUser,
			Trigger:       fileChangeReviewTrigger(params),
			Status:        result.Status,
			Resolution:    result.Resolution,
			StartedAtMS:   params.StartedAtMS,
			CompletedAtMS: uint64(time.Now().UTC().UnixMilli()),
		})
	case ServerRequestPermissionsApproval:
		params, ok := request.Params.(*PermissionsRequestApprovalParams)
		if !ok || params == nil {
			return
		}
		var approvalResponse PermissionsRequestApprovalResponse
		if !decodeServerResponseResult(response.Result, &approvalResponse) {
			return
		}
		result := permissionsReviewResult(&approvalResponse)
		r.emitReviewAnalyticsEvent(context.Background(), reviewAnalyticsInput{
			ThreadID:      params.ThreadID,
			TurnID:        params.TurnID,
			ItemID:        params.ItemID,
			ReviewID:      "user:" + request.ID.String(),
			SubjectKind:   telemetry.ReviewSubjectKindPermissions,
			SubjectName:   "permissions",
			Reviewer:      telemetry.ReviewerUser,
			Trigger:       permissionsReviewTrigger(params),
			Status:        result.Status,
			Resolution:    result.Resolution,
			StartedAtMS:   params.StartedAtMS,
			CompletedAtMS: uint64(time.Now().UTC().UnixMilli()),
		})
	}
}

type reviewAnalyticsInput struct {
	ThreadID      string
	TurnID        string
	ItemID        string
	ReviewID      string
	SubjectKind   string
	SubjectName   string
	Reviewer      string
	Trigger       string
	Status        string
	Resolution    string
	StartedAtMS   uint64
	CompletedAtMS uint64
}

func (r *RuntimeRouter) emitReviewAnalyticsEvent(ctx context.Context, input reviewAnalyticsInput) {
	if r == nil || r.services.Analytics == nil {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.ReviewEventSink)
	if !ok {
		return
	}
	active := r.activeRuntimeTurnStateSnapshot(input.ThreadID, input.TurnID)
	if active == nil || strings.TrimSpace(active.ConnectionID) == "" {
		return
	}
	client, ok := r.analyticsAppServerClient(active.ConnectionID)
	if !ok {
		return
	}
	if record := r.threadRecordForAnalytics(input.ThreadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lineage := r.responsesMetadataLineage(input.ThreadID)
	event := telemetry.NewCodexReviewEvent(telemetry.CodexReviewEventParams{
		ThreadID:        strings.TrimSpace(input.ThreadID),
		TurnID:          strings.TrimSpace(input.TurnID),
		ItemID:          stringPtrIfNotEmpty(input.ItemID),
		ReviewID:        strings.TrimSpace(input.ReviewID),
		AppServerClient: client,
		Runtime:         telemetry.CurrentRuntimeMetadata(),
		ThreadSource:    stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:  stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:  stringPtrIfNotEmpty(lineage.ParentThreadID),
		SubjectKind:     input.SubjectKind,
		SubjectName:     strings.TrimSpace(input.SubjectName),
		Reviewer:        input.Reviewer,
		Trigger:         input.Trigger,
		Status:          input.Status,
		Resolution:      input.Resolution,
		StartedAtMS:     input.StartedAtMS,
		CompletedAtMS:   input.CompletedAtMS,
		DurationMS:      uint64PtrFromNonNegativeInt64(int64(input.CompletedAtMS) - int64(input.StartedAtMS)),
	})
	sink.TrackCodexReviewEvent(ctx, event)
}

func decodeServerResponseResult(result any, target any) bool {
	if target == nil {
		return false
	}
	data, err := json.Marshal(result)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false
	}
	return true
}

func (r *RuntimeRouter) handleNotificationAnalytics(method NotificationMethod, params any) {
	if r == nil {
		return
	}
	switch method {
	case NotificationItemGuardianApprovalReviewCompleted:
		notification, ok := params.(*ItemGuardianApprovalReviewCompletedNotification)
		if !ok || notification == nil {
			return
		}
		r.emitGuardianReviewCompletedAnalytics(context.Background(), notification)
	case NotificationHookCompleted:
		notification, ok := params.(*HookRunCompletedNotification)
		if !ok || notification == nil {
			return
		}
		r.emitHookRunAnalyticsEvent(context.Background(), notification)
	}
}

func (r *RuntimeRouter) emitGuardianReviewCompletedAnalytics(ctx context.Context, notification *ItemGuardianApprovalReviewCompletedNotification) {
	if r == nil || notification == nil {
		return
	}
	result, ok := guardianReviewResult(notification.Review.Status)
	if !ok {
		return
	}
	subjectKind, subjectName, trigger := guardianReviewSubjectMetadata(&notification.Action)
	itemID := ""
	if notification.TargetItemID != nil {
		itemID = strings.TrimSpace(*notification.TargetItemID)
	}
	if guardianReviewDenormalizesToToolItem(subjectKind) && itemID != "" {
		r.recordToolItemReviewSummary(notification.ThreadID, notification.TurnID, itemID, toolItemReviewSummary{
			ReviewCount:                    1,
			GuardianReviewCount:            1,
			FinalApprovalOutcome:           guardianFinalApprovalOutcome(result.Status),
			RequestedAdditionalPermissions: guardianReviewRequestedAdditionalPermissions(&notification.Action),
			RequestedNetworkAccess:         guardianReviewRequestedNetworkAccess(&notification.Action),
		})
	}
	r.emitReviewAnalyticsEvent(ctx, reviewAnalyticsInput{
		ThreadID:      notification.ThreadID,
		TurnID:        notification.TurnID,
		ItemID:        itemID,
		ReviewID:      notification.ReviewID,
		SubjectKind:   subjectKind,
		SubjectName:   subjectName,
		Reviewer:      telemetry.ReviewerGuardian,
		Trigger:       trigger,
		Status:        result.Status,
		Resolution:    result.Resolution,
		StartedAtMS:   notification.StartedAtMS,
		CompletedAtMS: notification.CompletedAtMS,
	})
	r.emitGuardianV2ClassificationAnalytics(ctx, notification, result)
}

func (r *RuntimeRouter) emitGuardianV2ClassificationAnalytics(ctx context.Context, notification *ItemGuardianApprovalReviewCompletedNotification, result userReviewResult) {
	if r == nil || r.services.Analytics == nil || notification == nil {
		return
	}
	active := r.activeRuntimeTurnStateSnapshot(notification.ThreadID, notification.TurnID)
	if active == nil || active.RunConfig == nil || !active.RunConfig.GuardianV2Enabled {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.GuardianV2EventSink)
	if !ok {
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
	var risk *string
	if notification.Review.RiskLevel != nil {
		value := string(*notification.Review.RiskLevel)
		risk = &value
	}
	event := telemetry.NewGuardianV2ClassificationEvent(telemetry.GuardianV2EventParams{
		SessionID:       firstNonEmpty(lineage.SessionID, notification.ThreadID),
		AppServerClient: client,
		Runtime:         telemetry.CurrentRuntimeMetadata(),
		ThreadSource:    stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:  stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:  stringPtrIfNotEmpty(lineage.ParentThreadID),
		GuardianV2Event: telemetry.GuardianV2Event{
			ThreadID:     strings.TrimSpace(notification.ThreadID),
			TurnID:       strings.TrimSpace(notification.TurnID),
			ItemID:       notification.TargetItemID,
			Model:        stringPtrIfNotEmpty(active.RunConfig.Model),
			OccurredAtMS: notification.CompletedAtMS,
			Outcome:      guardianV2AnalyticsOutcome(result.Status),
			RiskLevel:    risk,
			DurationMS:   guardianV2DurationMS(notification.StartedAtMS, notification.CompletedAtMS),
		},
	})
	sink.TrackCodexGuardianV2Event(ctx, event)
}

func (r *RuntimeRouter) emitGuardianV2FastDecision(ctx context.Context, threadID, turnID, itemID string) {
	if r == nil || r.services.Analytics == nil {
		return
	}
	active := r.activeRuntimeTurnStateSnapshot(threadID, turnID)
	if active == nil || active.RunConfig == nil || !active.RunConfig.GuardianV2Enabled {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.GuardianV2EventSink)
	if !ok {
		return
	}
	client, ok := r.analyticsAppServerClient(active.ConnectionID)
	if !ok {
		return
	}
	if record := r.threadRecordForAnalytics(threadID); record != nil {
		if originator := strings.TrimSpace(record.Metadata.Originator); originator != "" {
			client.ProductClientID = originator
		}
	}
	lineage := r.responsesMetadataLineage(threadID)
	if ctx == nil {
		ctx = context.Background()
	}
	event := telemetry.NewGuardianV2FastDecisionEvent(telemetry.GuardianV2EventParams{
		SessionID:       firstNonEmpty(lineage.SessionID, threadID),
		AppServerClient: client,
		Runtime:         telemetry.CurrentRuntimeMetadata(),
		ThreadSource:    stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:  stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:  stringPtrIfNotEmpty(lineage.ParentThreadID),
		GuardianV2Event: telemetry.GuardianV2Event{
			ThreadID:     strings.TrimSpace(threadID),
			TurnID:       strings.TrimSpace(turnID),
			ItemID:       stringPtrIfNotEmpty(itemID),
			Model:        stringPtrIfNotEmpty(active.RunConfig.Model),
			OccurredAtMS: uint64(time.Now().UTC().UnixMilli()),
			Decision:     "approved",
		},
	})
	sink.TrackCodexGuardianV2Event(ctx, event)
}

func guardianV2AnalyticsOutcome(status string) string {
	switch status {
	case telemetry.ReviewStatusApproved:
		return "allow"
	case telemetry.ReviewStatusDenied:
		return "deny"
	default:
		return "aborted"
	}
}

func guardianV2DurationMS(startedAtMS, completedAtMS uint64) uint64 {
	if completedAtMS < startedAtMS {
		return 0
	}
	return completedAtMS - startedAtMS
}

func commandExecutionReviewSubject(params *CommandExecutionRequestApprovalParams) (string, string) {
	if params != nil && params.NetworkApprovalContext != nil {
		return telemetry.ReviewSubjectKindNetworkAccess, "network_access"
	}
	return telemetry.ReviewSubjectKindCommandExecution, "command_execution"
}

func commandExecutionReviewTrigger(params *CommandExecutionRequestApprovalParams) string {
	if params == nil {
		return telemetry.ReviewTriggerInitial
	}
	if params.ApprovalID != nil && strings.TrimSpace(*params.ApprovalID) != "" {
		return telemetry.ReviewTriggerExecveIntercept
	}
	if commandExecutionRequestedNetworkAccess(params) {
		return telemetry.ReviewTriggerNetworkPolicyDenial
	}
	if commandExecutionRequestedAdditionalPermissions(params) {
		return telemetry.ReviewTriggerSandboxDenial
	}
	return telemetry.ReviewTriggerInitial
}

func guardianReviewResult(status GuardianApprovalReviewStatus) (userReviewResult, bool) {
	switch status {
	case GuardianApprovalReviewApproved:
		return userReviewResult{Status: telemetry.ReviewStatusApproved, Resolution: telemetry.ReviewResolutionNone, FinalApprovalOutcome: telemetry.FinalApprovalOutcomeGuardianApproved}, true
	case GuardianApprovalReviewDenied:
		return userReviewResult{Status: telemetry.ReviewStatusDenied, Resolution: telemetry.ReviewResolutionNone, FinalApprovalOutcome: telemetry.FinalApprovalOutcomeGuardianDenied}, true
	case GuardianApprovalReviewTimedOut:
		return userReviewResult{Status: telemetry.ReviewStatusTimedOut, Resolution: telemetry.ReviewResolutionNone, FinalApprovalOutcome: telemetry.FinalApprovalOutcomeGuardianAborted}, true
	case GuardianApprovalReviewAborted:
		return userReviewResult{Status: telemetry.ReviewStatusAborted, Resolution: telemetry.ReviewResolutionNone, FinalApprovalOutcome: telemetry.FinalApprovalOutcomeGuardianAborted}, true
	default:
		return userReviewResult{}, false
	}
}

func guardianReviewSubjectMetadata(action *GuardianApprovalReviewAction) (string, string, string) {
	switch guardianReviewActionType(action) {
	case "execve":
		return telemetry.ReviewSubjectKindCommandExecution, "command_execution", telemetry.ReviewTriggerExecveIntercept
	case "applyPatch":
		return telemetry.ReviewSubjectKindFileChange, "apply_patch", telemetry.ReviewTriggerSandboxDenial
	case "networkAccess":
		return telemetry.ReviewSubjectKindNetworkAccess, "network_access", telemetry.ReviewTriggerNetworkPolicyDenial
	case "requestPermissions":
		return telemetry.ReviewSubjectKindPermissions, "permissions", guardianRequestPermissionsReviewTrigger(action)
	case "mcpToolCall":
		toolName := ""
		if action != nil {
			toolName = strings.TrimSpace(action.ToolName)
		}
		return telemetry.ReviewSubjectKindMCPToolCall, toolName, telemetry.ReviewTriggerInitial
	default:
		return telemetry.ReviewSubjectKindCommandExecution, "command_execution", telemetry.ReviewTriggerInitial
	}
}

func guardianReviewActionType(action *GuardianApprovalReviewAction) string {
	if action == nil {
		return ""
	}
	switch strings.TrimSpace(action.Type) {
	case "apply_patch", "applyPatch":
		return "applyPatch"
	case "network_access", "networkAccess":
		return "networkAccess"
	case "mcp_tool_call", "mcpToolCall":
		return "mcpToolCall"
	case "request_permissions", "requestPermissions":
		return "requestPermissions"
	default:
		return strings.TrimSpace(action.Type)
	}
}

func guardianRequestPermissionsReviewTrigger(action *GuardianApprovalReviewAction) string {
	if guardianReviewRequestedNetworkAccess(action) {
		return telemetry.ReviewTriggerNetworkPolicyDenial
	}
	if action != nil && action.Permissions != nil && action.Permissions.FileSystem != nil {
		return telemetry.ReviewTriggerSandboxDenial
	}
	return telemetry.ReviewTriggerInitial
}

func guardianReviewDenormalizesToToolItem(subjectKind string) bool {
	switch subjectKind {
	case telemetry.ReviewSubjectKindCommandExecution, telemetry.ReviewSubjectKindFileChange, telemetry.ReviewSubjectKindMCPToolCall:
		return true
	default:
		return false
	}
}

func guardianFinalApprovalOutcome(status string) string {
	switch status {
	case telemetry.ReviewStatusApproved:
		return telemetry.FinalApprovalOutcomeGuardianApproved
	case telemetry.ReviewStatusDenied:
		return telemetry.FinalApprovalOutcomeGuardianDenied
	default:
		return telemetry.FinalApprovalOutcomeGuardianAborted
	}
}

func guardianReviewRequestedAdditionalPermissions(action *GuardianApprovalReviewAction) bool {
	switch guardianReviewActionType(action) {
	case "applyPatch", "networkAccess":
		return true
	case "requestPermissions":
		return guardianReviewRequestedNetworkAccess(action) ||
			(action != nil && action.Permissions != nil && action.Permissions.FileSystem != nil)
	default:
		return false
	}
}

func guardianReviewRequestedNetworkAccess(action *GuardianApprovalReviewAction) bool {
	switch guardianReviewActionType(action) {
	case "networkAccess":
		return true
	case "requestPermissions":
		return action != nil &&
			action.Permissions != nil &&
			action.Permissions.Network != nil &&
			action.Permissions.Network.Enabled != nil &&
			*action.Permissions.Network.Enabled
	default:
		return false
	}
}

func fileChangeReviewTrigger(params *FileChangeRequestApprovalParams) string {
	if params != nil && params.GrantRoot != nil && strings.TrimSpace(*params.GrantRoot) != "" {
		return telemetry.ReviewTriggerSandboxDenial
	}
	return telemetry.ReviewTriggerInitial
}

func permissionsReviewTrigger(params *PermissionsRequestApprovalParams) string {
	if permissionsRequestRequestedNetworkAccess(params) {
		return telemetry.ReviewTriggerNetworkPolicyDenial
	}
	if permissionsRequestRequestedAdditionalPermissions(params) {
		return telemetry.ReviewTriggerSandboxDenial
	}
	return telemetry.ReviewTriggerInitial
}
