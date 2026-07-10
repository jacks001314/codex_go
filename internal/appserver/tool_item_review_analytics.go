package appserver

import (
	"strings"

	"codex_go/internal/sandbox"
	"codex_go/internal/telemetry"
)

type toolItemReviewSummary struct {
	ReviewCount                    uint64
	GuardianReviewCount            uint64
	UserReviewCount                uint64
	FinalApprovalOutcome           string
	RequestedAdditionalPermissions bool
	RequestedNetworkAccess         bool
}

func defaultToolItemReviewSummary() toolItemReviewSummary {
	return toolItemReviewSummary{FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUnknown}
}

func (r *RuntimeRouter) toolItemReviewSummary(threadID string, turnID string, itemID string) toolItemReviewSummary {
	summary := defaultToolItemReviewSummary()
	if r == nil {
		return summary
	}
	key := toolItemReviewSummaryKey(threadID, turnID, itemID)
	if key == "" {
		return summary
	}
	r.approvalSessionsMu.RLock()
	defer r.approvalSessionsMu.RUnlock()
	if r.toolItemReviews == nil {
		return summary
	}
	if stored, ok := r.toolItemReviews[key]; ok {
		return stored
	}
	return summary
}

func (r *RuntimeRouter) recordCommandExecutionApprovalReview(params *CommandExecutionRequestApprovalParams, response *CommandExecutionRequestApprovalResponse) {
	if r == nil || params == nil || response == nil {
		return
	}
	result := commandExecutionReviewResult(response.Decision)
	r.recordToolItemReviewSummary(params.ThreadID, params.TurnID, params.ItemID, toolItemReviewSummary{
		ReviewCount:                    1,
		UserReviewCount:                1,
		FinalApprovalOutcome:           result.FinalApprovalOutcome,
		RequestedAdditionalPermissions: commandExecutionRequestedAdditionalPermissions(params),
		RequestedNetworkAccess:         commandExecutionRequestedNetworkAccess(params),
	})
}

func (r *RuntimeRouter) recordFileChangeApprovalReview(params *FileChangeRequestApprovalParams, response *FileChangeRequestApprovalResponse) {
	if r == nil || params == nil || response == nil {
		return
	}
	result := fileChangeReviewResult(response.Decision)
	r.recordToolItemReviewSummary(params.ThreadID, params.TurnID, params.ItemID, toolItemReviewSummary{
		ReviewCount:                    1,
		UserReviewCount:                1,
		FinalApprovalOutcome:           result.FinalApprovalOutcome,
		RequestedAdditionalPermissions: params.GrantRoot != nil && strings.TrimSpace(*params.GrantRoot) != "",
		RequestedNetworkAccess:         false,
	})
}

func (r *RuntimeRouter) recordToolItemReviewSummary(threadID string, turnID string, itemID string, summary toolItemReviewSummary) {
	if r == nil {
		return
	}
	key := toolItemReviewSummaryKey(threadID, turnID, itemID)
	if key == "" {
		return
	}
	if summary.FinalApprovalOutcome == "" {
		summary.FinalApprovalOutcome = telemetry.FinalApprovalOutcomeUnknown
	}
	r.approvalSessionsMu.Lock()
	defer r.approvalSessionsMu.Unlock()
	if r.toolItemReviews == nil {
		r.toolItemReviews = map[string]toolItemReviewSummary{}
	}
	r.toolItemReviews[key] = summary
}

func (r *RuntimeRouter) clearToolItemReviewSummaries(threadID string, turnID string) {
	if r == nil {
		return
	}
	prefix := toolItemReviewSummaryKeyPrefix(threadID, turnID)
	if prefix == "" {
		return
	}
	r.approvalSessionsMu.Lock()
	defer r.approvalSessionsMu.Unlock()
	for key := range r.toolItemReviews {
		if strings.HasPrefix(key, prefix) {
			delete(r.toolItemReviews, key)
		}
	}
}

func toolItemReviewSummaryKey(threadID string, turnID string, itemID string) string {
	prefix := toolItemReviewSummaryKeyPrefix(threadID, turnID)
	itemID = strings.TrimSpace(itemID)
	if prefix == "" || itemID == "" {
		return ""
	}
	return prefix + itemID
}

func toolItemReviewSummaryKeyPrefix(threadID string, turnID string) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return ""
	}
	return threadID + "\x00" + turnID + "\x00"
}

type userReviewResult struct {
	Status               string
	Resolution           string
	FinalApprovalOutcome string
}

func commandExecutionReviewResult(decision any) userReviewResult {
	switch approvalDecisionString(decision) {
	case string(CommandExecutionApprovalAcceptForSession):
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionSessionApproval,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApprovedForSession,
		}
	case string(CommandExecutionApprovalAcceptWithExecpolicyAmendment):
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionExecPolicyAmendment,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApproved,
		}
	case string(CommandExecutionApprovalApplyNetworkPolicyAmendment):
		switch commandExecutionApprovalDecisionNetworkAction(decision) {
		case string(NetworkPolicyRuleAllow):
			return userReviewResult{
				Status:               telemetry.ReviewStatusApproved,
				Resolution:           telemetry.ReviewResolutionNetworkPolicyAmendment,
				FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApproved,
			}
		case string(NetworkPolicyRuleDeny):
			return userReviewResult{
				Status:               telemetry.ReviewStatusDenied,
				Resolution:           telemetry.ReviewResolutionNetworkPolicyAmendment,
				FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserDenied,
			}
		}
		return userReviewResult{
			Status:               telemetry.ReviewStatusAborted,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserAborted,
		}
	case string(CommandExecutionApprovalAccept):
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApproved,
		}
	case string(CommandExecutionApprovalDecline):
		return userReviewResult{
			Status:               telemetry.ReviewStatusDenied,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserDenied,
		}
	default:
		return userReviewResult{
			Status:               telemetry.ReviewStatusAborted,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserAborted,
		}
	}
}

func fileChangeReviewResult(decision any) userReviewResult {
	switch approvalDecisionString(decision) {
	case string(FileChangeApprovalAcceptForSession):
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionSessionApproval,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApprovedForSession,
		}
	case string(FileChangeApprovalAccept):
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApproved,
		}
	case string(FileChangeApprovalDecline):
		return userReviewResult{
			Status:               telemetry.ReviewStatusDenied,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserDenied,
		}
	default:
		return userReviewResult{
			Status:               telemetry.ReviewStatusAborted,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserAborted,
		}
	}
}

func permissionsReviewResult(response *PermissionsRequestApprovalResponse) userReviewResult {
	if response == nil || grantedPermissionProfileIsEmpty(response.Permissions) {
		return userReviewResult{
			Status:               telemetry.ReviewStatusDenied,
			Resolution:           telemetry.ReviewResolutionNone,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserDenied,
		}
	}
	if response.Scope == PermissionGrantScopeSession {
		return userReviewResult{
			Status:               telemetry.ReviewStatusApproved,
			Resolution:           telemetry.ReviewResolutionSessionApproval,
			FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApprovedForSession,
		}
	}
	return userReviewResult{
		Status:               telemetry.ReviewStatusApproved,
		Resolution:           telemetry.ReviewResolutionNone,
		FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUserApproved,
	}
}

func commandExecutionRequestedAdditionalPermissions(params *CommandExecutionRequestApprovalParams) bool {
	if params == nil {
		return false
	}
	return params.SandboxDenied ||
		params.SuggestedProfile != nil ||
		(params.AdditionalPermissions != nil && !params.AdditionalPermissions.IsEmpty())
}

func commandExecutionRequestedNetworkAccess(params *CommandExecutionRequestApprovalParams) bool {
	if params == nil {
		return false
	}
	if params.NetworkApprovalContext != nil || len(params.ProposedNetworkPolicyAmendments) > 0 {
		return true
	}
	if params.AdditionalPermissions != nil && params.AdditionalPermissions.Network != nil {
		return *params.AdditionalPermissions.Network
	}
	return false
}

func permissionsRequestRequestedAdditionalPermissions(params *PermissionsRequestApprovalParams) bool {
	if permissionsRequestRequestedNetworkAccess(params) {
		return true
	}
	if params == nil || params.Permissions == nil {
		return false
	}
	return permissionsMapValuePresent(params.Permissions, "fileSystem") ||
		permissionsMapValuePresent(params.Permissions, "file_system")
}

func permissionsRequestRequestedNetworkAccess(params *PermissionsRequestApprovalParams) bool {
	if params == nil || params.Permissions == nil {
		return false
	}
	network, ok := permissionsMapValue(params.Permissions, "network")
	if !ok {
		return false
	}
	networkMap, ok := permissionsMapFromAny(network)
	if !ok {
		return false
	}
	enabled, ok := permissionsBoolFromAny(networkMap["enabled"])
	return ok && enabled
}

func grantedPermissionProfileIsEmpty(profile *GrantedPermissionProfile) bool {
	if profile == nil {
		return true
	}
	if profile.Network != nil && profile.Network.Enabled != nil && *profile.Network.Enabled {
		return false
	}
	if profile.FileSystem == nil {
		return true
	}
	return len(profile.FileSystem.Read) == 0 &&
		len(profile.FileSystem.Write) == 0 &&
		len(profile.FileSystem.Entries) == 0
}

func permissionsMapValue(values map[string]any, key string) (any, bool) {
	if values == nil {
		return nil, false
	}
	value, ok := values[key]
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

func permissionsMapValuePresent(values map[string]any, key string) bool {
	_, ok := permissionsMapValue(values, key)
	return ok
}

func permissionsMapFromAny(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

func permissionsBoolFromAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	default:
		return false, false
	}
}

func cloneAdditionalPermissionProfile(profile *sandbox.AdditionalPermissionProfile) *sandbox.AdditionalPermissionProfile {
	if profile == nil {
		return nil
	}
	out := &sandbox.AdditionalPermissionProfile{FileSystem: append([]string(nil), profile.FileSystem...)}
	if profile.Network != nil {
		value := *profile.Network
		out.Network = &value
	}
	return out
}
