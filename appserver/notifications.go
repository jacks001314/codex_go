package appserver

import "encoding/json"

type ThreadItemPayload map[string]any

const (
	NotificationItemStarted                         NotificationMethod = "item/started"
	NotificationItemCompleted                       NotificationMethod = "item/completed"
	NotificationAgentMessageDelta                   NotificationMethod = "item/agentMessage/delta"
	NotificationReasoningTextDelta                  NotificationMethod = "item/reasoning/textDelta"
	NotificationReasoningSummaryPartAdded           NotificationMethod = "item/reasoning/summaryPartAdded"
	NotificationReasoningSummaryTextDelta           NotificationMethod = "item/reasoning/summaryTextDelta"
	NotificationPlanDelta                           NotificationMethod = "item/plan/delta"
	NotificationFileChangeOutputDelta               NotificationMethod = "item/fileChange/outputDelta"
	NotificationFileChangePatchUpdated              NotificationMethod = "item/fileChange/patchUpdated"
	NotificationItemGuardianApprovalReviewStarted   NotificationMethod = "item/autoApprovalReview/started"
	NotificationItemGuardianApprovalReviewCompleted NotificationMethod = "item/autoApprovalReview/completed"
	NotificationRawResponseItemCompleted            NotificationMethod = "rawResponseItem/completed"
	NotificationRawResponseCompleted                NotificationMethod = "rawResponse/completed"
	NotificationHookStarted                         NotificationMethod = "hook/started"
	NotificationHookCompleted                       NotificationMethod = "hook/completed"
	NotificationExternalAgentConfigImportProgress   NotificationMethod = "externalAgentConfig/import/progress"
	NotificationExternalAgentConfigImportCompleted  NotificationMethod = "externalAgentConfig/import/completed"
	NotificationTurnDiffUpdated                     NotificationMethod = "turn/diff/updated"
	NotificationTurnPlanUpdated                     NotificationMethod = "turn/plan/updated"
	NotificationTurnModerationMetadata              NotificationMethod = "turn/moderationMetadata"
	NotificationContextCompacted                    NotificationMethod = "thread/compacted"
	NotificationMCPServerStatusUpdated              NotificationMethod = "mcpServer/startupStatus/updated"
	NotificationMCPToolCallProgress                 NotificationMethod = "item/mcpToolCall/progress"
	NotificationMCPServerOauthLoginCompleted        NotificationMethod = "mcpServer/oauthLogin/completed"
	NotificationWindowsSandboxSetupCompleted        NotificationMethod = "windowsSandbox/setupCompleted"
	NotificationWindowsWorldWritableWarning         NotificationMethod = "windows/worldWritableWarning"
	NotificationFSChanged                           NotificationMethod = "fs/changed"
	NotificationCommandExecOutputDelta              NotificationMethod = "command/exec/outputDelta"
	NotificationCommandExecutionOutputDelta         NotificationMethod = "item/commandExecution/outputDelta"
	NotificationTerminalInteraction                 NotificationMethod = "item/commandExecution/terminalInteraction"
	NotificationServerRequestResolved               NotificationMethod = "serverRequest/resolved"
	NotificationProcessOutputDelta                  NotificationMethod = "process/outputDelta"
	NotificationProcessExited                       NotificationMethod = "process/exited"
	NotificationGuardianWarning                     NotificationMethod = "guardianWarning"
	NotificationModelSafetyBufferingUpdated         NotificationMethod = "model/safetyBuffering/updated"
	NotificationMcpServerStatusUpdated              NotificationMethod = NotificationMCPServerStatusUpdated
	NotificationMcpToolCallProgress                 NotificationMethod = NotificationMCPToolCallProgress
	NotificationMcpServerOauthLoginCompleted        NotificationMethod = NotificationMCPServerOauthLoginCompleted
	NotificationFsChanged                           NotificationMethod = NotificationFSChanged
	NotificationThreadRealtimeSdp                   NotificationMethod = NotificationThreadRealtimeSDP
	NotificationThreadQueueChanged                  NotificationMethod = "thread/queue/changed"
	NotificationThreadReverted                      NotificationMethod = "thread/reverted"
)

type ErrorNotification struct {
	Error     TurnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
}

type ItemStartedNotification struct {
	Item        ThreadItemPayload `json:"item"`
	ThreadID    string            `json:"threadId"`
	TurnID      string            `json:"turnId"`
	StartedAtMS int64             `json:"startedAtMs"`
}

type ItemCompletedNotification struct {
	Item          ThreadItemPayload `json:"item"`
	ThreadID      string            `json:"threadId"`
	TurnID        string            `json:"turnId"`
	CompletedAtMS int64             `json:"completedAtMs"`
}

type TurnStartedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type TurnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type ThreadTokenUsageUpdatedNotification struct {
	ThreadID   string     `json:"threadId"`
	TurnID     string     `json:"turnId"`
	TokenUsage TokenUsage `json:"tokenUsage"`
}

type TokenUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens,omitempty"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens,omitempty"`
	TotalTokens           int64 `json:"totalTokens"`
	Total                 *TokenUsageBreakdown
	Last                  *TokenUsageBreakdown
	ModelContextWindow    *int64
}

type TokenUsageBreakdown struct {
	TotalTokens           int64 `json:"totalTokens"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

func (u *TokenUsage) MarshalJSON() ([]byte, error) {
	last := tokenUsageBreakdownFromUsage(u)
	if u != nil && u.Last != nil {
		last = cloneTokenUsageBreakdownPtr(u.Last)
	}
	total := cloneTokenUsageBreakdownPtr(last)
	if u != nil && u.Total != nil {
		total = cloneTokenUsageBreakdownPtr(u.Total)
	}
	var modelContextWindow *int64
	if u != nil {
		modelContextWindow = cloneInt64PtrAppserver(u.ModelContextWindow)
	}
	return json.Marshal(struct {
		Total              *TokenUsageBreakdown `json:"total"`
		Last               *TokenUsageBreakdown `json:"last"`
		ModelContextWindow *int64               `json:"modelContextWindow"`
	}{
		Total:              total,
		Last:               last,
		ModelContextWindow: modelContextWindow,
	})
}

func tokenUsageBreakdownFromUsage(u *TokenUsage) *TokenUsageBreakdown {
	if u == nil {
		return &TokenUsageBreakdown{}
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return &TokenUsageBreakdown{
		TotalTokens:           total,
		InputTokens:           u.InputTokens,
		CachedInputTokens:     u.CachedInputTokens,
		CacheWriteInputTokens: u.CacheWriteInputTokens,
		OutputTokens:          u.OutputTokens,
		ReasoningOutputTokens: u.ReasoningOutputTokens,
	}
}

func cloneTokenUsageBreakdownPtr(value *TokenUsageBreakdown) *TokenUsageBreakdown {
	if value == nil {
		return &TokenUsageBreakdown{}
	}
	clone := *value
	if clone.TotalTokens == 0 {
		clone.TotalTokens = clone.InputTokens + clone.OutputTokens
	}
	return &clone
}

type SkillsChangedNotification struct{}

type WarningNotification struct {
	Message  string  `json:"message"`
	ThreadID *string `json:"threadId,omitempty"`
}

func (n *WarningNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID *string `json:"threadId"`
		Message  string  `json:"message"`
	}{
		ThreadID: cloneStringPtrAppserver(n.ThreadID),
		Message:  n.Message,
	})
}

type GuardianWarningNotification struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type DeprecationNoticeNotification struct {
	Summary string  `json:"summary"`
	Details *string `json:"details"`
	Message string  `json:"message,omitempty"`
}

func (n *DeprecationNoticeNotification) MarshalJSON() ([]byte, error) {
	summary := n.Summary
	if summary == "" {
		summary = n.Message
	}
	return json.Marshal(struct {
		Summary string  `json:"summary"`
		Details *string `json:"details"`
	}{
		Summary: summary,
		Details: cloneStringPtrAppserver(n.Details),
	})
}

type ModelReroutedNotification struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
	Reason    string `json:"reason"`
}

type ModelRerouteReason string

const ModelRerouteReasonHighRiskCyberActivity ModelRerouteReason = "highRiskCyberActivity"

type ModelVerification string

const ModelVerificationTrustedAccessForCyber ModelVerification = "trustedAccessForCyber"

type ModelVerificationNotification struct {
	ThreadID      string              `json:"threadId"`
	TurnID        string              `json:"turnId"`
	Verifications []ModelVerification `json:"verifications"`
}

func (n *ModelVerificationNotification) MarshalJSON() ([]byte, error) {
	verifications := append([]ModelVerification(nil), n.Verifications...)
	if verifications == nil {
		verifications = []ModelVerification{}
	}
	return json.Marshal(struct {
		ThreadID      string              `json:"threadId"`
		TurnID        string              `json:"turnId"`
		Verifications []ModelVerification `json:"verifications"`
	}{
		ThreadID:      n.ThreadID,
		TurnID:        n.TurnID,
		Verifications: verifications,
	})
}

type ModelSafetyBufferingUpdatedNotification struct {
	ThreadID        string   `json:"threadId"`
	TurnID          string   `json:"turnId"`
	Model           string   `json:"model"`
	UseCases        []string `json:"useCases"`
	Reasons         []string `json:"reasons"`
	ShowBufferingUI bool     `json:"showBufferingUi"`
	FasterModel     *string  `json:"fasterModel"`
}

func (n *ModelSafetyBufferingUpdatedNotification) MarshalJSON() ([]byte, error) {
	useCases := append([]string(nil), n.UseCases...)
	if useCases == nil {
		useCases = []string{}
	}
	reasons := append([]string(nil), n.Reasons...)
	if reasons == nil {
		reasons = []string{}
	}
	return json.Marshal(struct {
		ThreadID        string   `json:"threadId"`
		TurnID          string   `json:"turnId"`
		Model           string   `json:"model"`
		UseCases        []string `json:"useCases"`
		Reasons         []string `json:"reasons"`
		ShowBufferingUI bool     `json:"showBufferingUi"`
		FasterModel     *string  `json:"fasterModel"`
	}{
		ThreadID:        n.ThreadID,
		TurnID:          n.TurnID,
		Model:           n.Model,
		UseCases:        useCases,
		Reasons:         reasons,
		ShowBufferingUI: n.ShowBufferingUI,
		FasterModel:     cloneStringPtrAppserver(n.FasterModel),
	})
}

type FuzzyFileSearchSessionUpdatedNotification struct {
	SessionID string `json:"sessionId"`
	Query     string `json:"query"`
	Files     []any  `json:"files"`
}

func (n *FuzzyFileSearchSessionUpdatedNotification) MarshalJSON() ([]byte, error) {
	files := append([]any(nil), n.Files...)
	if files == nil {
		files = []any{}
	}
	return json.Marshal(struct {
		SessionID string `json:"sessionId"`
		Query     string `json:"query"`
		Files     []any  `json:"files"`
	}{
		SessionID: n.SessionID,
		Query:     n.Query,
		Files:     files,
	})
}

type FuzzyFileSearchSessionCompletedNotification struct {
	SessionID string `json:"sessionId"`
}

type ThreadRealtimeStartedNotification struct {
	ThreadID          string  `json:"threadId"`
	Version           string  `json:"version"`
	RealtimeSessionID *string `json:"realtimeSessionId"`
}

type ThreadRealtimeItemAddedNotification struct {
	ThreadID string `json:"threadId"`
	Item     any    `json:"item"`
}

type ThreadRealtimeTranscriptDeltaNotification struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role"`
	Delta    string `json:"delta"`
}

type ThreadRealtimeTranscriptDoneNotification struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role"`
	Text     string `json:"text"`
}

type ThreadRealtimeAudioChunk struct {
	Data              string  `json:"data"`
	ItemID            *string `json:"itemId"`
	NumChannels       uint16  `json:"numChannels"`
	SampleRate        uint32  `json:"sampleRate"`
	SamplesPerChannel *uint32 `json:"samplesPerChannel"`
}

type ThreadRealtimeOutputAudioDeltaNotification struct {
	ThreadID string                   `json:"threadId"`
	Audio    ThreadRealtimeAudioChunk `json:"audio"`
}

type ThreadRealtimeSDPNotification struct {
	ThreadID string `json:"threadId"`
	SDP      string `json:"sdp"`
}

type ThreadRealtimeErrorNotification struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ThreadRealtimeClosedNotification struct {
	ThreadID string  `json:"threadId"`
	Reason   *string `json:"reason"`
}

type DeltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type AgentMessageDeltaNotification = DeltaNotification
type PlanDeltaNotification = DeltaNotification
type FileChangeOutputDeltaNotification = DeltaNotification
type CommandExecutionOutputDeltaNotification = DeltaNotification

type ReasoningTextDeltaNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	ContentIndex int    `json:"contentIndex"`
}

type ReasoningSummaryPartAddedNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	SummaryIndex int    `json:"summaryIndex"`
}

type ReasoningSummaryTextDeltaNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	SummaryIndex int    `json:"summaryIndex"`
}

type ContextCompactedNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	Summary      string `json:"summary,omitempty"`
	ItemCount    int    `json:"itemCount,omitempty"`
	WindowNumber uint64 `json:"windowNumber,omitempty"`
	Trigger      string `json:"trigger,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Phase        string `json:"phase,omitempty"`
	Status       string `json:"status,omitempty"`
	Source       string `json:"source,omitempty"`
	ResponseID   string `json:"responseId,omitempty"`
	Model        string `json:"model,omitempty"`
	ProviderID   string `json:"providerId,omitempty"`
	CompletedAt  string `json:"completedAt,omitempty"`
	TokenUsage   any    `json:"tokenUsage,omitempty"`
}

func (n *ContextCompactedNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}{
		ThreadID: n.ThreadID,
		TurnID:   n.TurnID,
	})
}

type TurnDiffUpdatedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Diff     string `json:"diff"`
}

type TurnPlanUpdatedNotification struct {
	ThreadID    string         `json:"threadId"`
	TurnID      string         `json:"turnId"`
	Explanation *string        `json:"explanation"`
	Plan        []TurnPlanStep `json:"plan"`
}

func (n *TurnPlanUpdatedNotification) MarshalJSON() ([]byte, error) {
	plan := append([]TurnPlanStep(nil), n.Plan...)
	if plan == nil {
		plan = []TurnPlanStep{}
	}
	return json.Marshal(struct {
		ThreadID    string         `json:"threadId"`
		TurnID      string         `json:"turnId"`
		Explanation *string        `json:"explanation"`
		Plan        []TurnPlanStep `json:"plan"`
	}{
		ThreadID:    n.ThreadID,
		TurnID:      n.TurnID,
		Explanation: n.Explanation,
		Plan:        plan,
	})
}

type TurnPlanStep struct {
	Step   string             `json:"step"`
	Status TurnPlanStepStatus `json:"status"`
}

type TurnPlanStepStatus string

const (
	TurnPlanStepPending    TurnPlanStepStatus = "pending"
	TurnPlanStepInProgress TurnPlanStepStatus = "inProgress"
	TurnPlanStepCompleted  TurnPlanStepStatus = "completed"
)

type TurnModerationMetadataNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Metadata any    `json:"metadata"`
}

type FileChangePatchUpdatedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Changes  []any  `json:"changes"`
}

func (n *FileChangePatchUpdatedNotification) MarshalJSON() ([]byte, error) {
	changes := threadItemFileChangesFromAny(n.Changes)
	return json.Marshal(struct {
		ThreadID string             `json:"threadId"`
		TurnID   string             `json:"turnId"`
		ItemID   string             `json:"itemId"`
		Changes  []fileChangeUpdate `json:"changes"`
	}{
		ThreadID: n.ThreadID,
		TurnID:   n.TurnID,
		ItemID:   n.ItemID,
		Changes:  changes,
	})
}

type RawResponseItemCompletedNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     any    `json:"item"`
}

type RawResponseCompletedNotification struct {
	ThreadID   string               `json:"threadId"`
	TurnID     string               `json:"turnId"`
	ResponseID string               `json:"responseId"`
	Usage      *TokenUsageBreakdown `json:"usage"`
}

type HookStartedNotification struct {
	ThreadID string  `json:"threadId"`
	TurnID   *string `json:"turnId"`
	Run      any     `json:"run"`
}

type HookCompletedNotification struct {
	ThreadID string  `json:"threadId"`
	TurnID   *string `json:"turnId"`
	Run      any     `json:"run"`
}

type GuardianApprovalReviewStatus string

const (
	GuardianApprovalReviewInProgress GuardianApprovalReviewStatus = "inProgress"
	GuardianApprovalReviewApproved   GuardianApprovalReviewStatus = "approved"
	GuardianApprovalReviewDenied     GuardianApprovalReviewStatus = "denied"
	GuardianApprovalReviewTimedOut   GuardianApprovalReviewStatus = "timedOut"
	GuardianApprovalReviewAborted    GuardianApprovalReviewStatus = "aborted"
)

type GuardianRiskLevel string

const (
	GuardianRiskLow      GuardianRiskLevel = "low"
	GuardianRiskMedium   GuardianRiskLevel = "medium"
	GuardianRiskHigh     GuardianRiskLevel = "high"
	GuardianRiskCritical GuardianRiskLevel = "critical"
)

type GuardianUserAuthorization string

const (
	GuardianAuthorizationUnknown GuardianUserAuthorization = "unknown"
	GuardianAuthorizationLow     GuardianUserAuthorization = "low"
	GuardianAuthorizationMedium  GuardianUserAuthorization = "medium"
	GuardianAuthorizationHigh    GuardianUserAuthorization = "high"
)

type GuardianApprovalReview struct {
	Status            GuardianApprovalReviewStatus `json:"status"`
	RiskLevel         *GuardianRiskLevel           `json:"riskLevel"`
	UserAuthorization *GuardianUserAuthorization   `json:"userAuthorization"`
	Rationale         *string                      `json:"rationale"`
}

type GuardianCommandSource string

const (
	GuardianCommandSourceShell       GuardianCommandSource = "shell"
	GuardianCommandSourceUnifiedExec GuardianCommandSource = "unifiedExec"
)

type GuardianApprovalReviewAction struct {
	Type          string                    `json:"type"`
	Source        GuardianCommandSource     `json:"source,omitempty"`
	Command       string                    `json:"command,omitempty"`
	Program       string                    `json:"program,omitempty"`
	Argv          []string                  `json:"argv,omitempty"`
	CWD           string                    `json:"cwd,omitempty"`
	Files         []string                  `json:"files,omitempty"`
	Target        string                    `json:"target,omitempty"`
	Host          string                    `json:"host,omitempty"`
	Protocol      NetworkApprovalProtocol   `json:"protocol,omitempty"`
	Port          uint16                    `json:"port,omitempty"`
	Server        string                    `json:"server,omitempty"`
	ToolName      string                    `json:"toolName,omitempty"`
	ConnectorID   *string                   `json:"connectorId,omitempty"`
	ConnectorName *string                   `json:"connectorName,omitempty"`
	ToolTitle     *string                   `json:"toolTitle,omitempty"`
	Reason        *string                   `json:"reason,omitempty"`
	Permissions   *RequestPermissionProfile `json:"permissions,omitempty"`
}

func (a *GuardianApprovalReviewAction) MarshalJSON() ([]byte, error) {
	actionType := a.Type
	switch actionType {
	case "apply_patch":
		actionType = "applyPatch"
	case "network_access":
		actionType = "networkAccess"
	case "mcp_tool_call":
		actionType = "mcpToolCall"
	case "request_permissions":
		actionType = "requestPermissions"
	}
	switch actionType {
	case "command":
		return json.Marshal(struct {
			Type    string                `json:"type"`
			Source  GuardianCommandSource `json:"source"`
			Command string                `json:"command"`
			CWD     string                `json:"cwd"`
		}{
			Type:    actionType,
			Source:  a.Source,
			Command: a.Command,
			CWD:     a.CWD,
		})
	case "execve":
		argv := append([]string(nil), a.Argv...)
		if argv == nil {
			argv = []string{}
		}
		return json.Marshal(struct {
			Type    string                `json:"type"`
			Source  GuardianCommandSource `json:"source"`
			Program string                `json:"program"`
			Argv    []string              `json:"argv"`
			CWD     string                `json:"cwd"`
		}{
			Type:    actionType,
			Source:  a.Source,
			Program: a.Program,
			Argv:    argv,
			CWD:     a.CWD,
		})
	case "applyPatch":
		files := append([]string(nil), a.Files...)
		if files == nil {
			files = []string{}
		}
		return json.Marshal(struct {
			Type  string   `json:"type"`
			CWD   string   `json:"cwd"`
			Files []string `json:"files"`
		}{
			Type:  actionType,
			CWD:   a.CWD,
			Files: files,
		})
	case "networkAccess":
		return json.Marshal(struct {
			Type     string                  `json:"type"`
			Target   string                  `json:"target"`
			Host     string                  `json:"host"`
			Protocol NetworkApprovalProtocol `json:"protocol"`
			Port     uint16                  `json:"port"`
		}{
			Type:     actionType,
			Target:   a.Target,
			Host:     a.Host,
			Protocol: a.Protocol,
			Port:     a.Port,
		})
	case "mcpToolCall":
		return json.Marshal(struct {
			Type          string  `json:"type"`
			Server        string  `json:"server"`
			ToolName      string  `json:"toolName"`
			ConnectorID   *string `json:"connectorId"`
			ConnectorName *string `json:"connectorName"`
			ToolTitle     *string `json:"toolTitle"`
		}{
			Type:          actionType,
			Server:        a.Server,
			ToolName:      a.ToolName,
			ConnectorID:   cloneString(a.ConnectorID),
			ConnectorName: cloneString(a.ConnectorName),
			ToolTitle:     cloneString(a.ToolTitle),
		})
	case "requestPermissions":
		permissions := a.Permissions
		if permissions == nil {
			permissions = &RequestPermissionProfile{}
		}
		return json.Marshal(struct {
			Type        string                    `json:"type"`
			Reason      *string                   `json:"reason"`
			Permissions *RequestPermissionProfile `json:"permissions"`
		}{
			Type:        actionType,
			Reason:      cloneString(a.Reason),
			Permissions: permissions,
		})
	default:
		type actionAlias GuardianApprovalReviewAction
		return json.Marshal(actionAlias(*a))
	}
}

type ItemGuardianApprovalReviewStartedNotification struct {
	ThreadID     string                       `json:"threadId"`
	TurnID       string                       `json:"turnId"`
	StartedAtMS  uint64                       `json:"startedAtMs"`
	ReviewID     string                       `json:"reviewId"`
	TargetItemID *string                      `json:"targetItemId"`
	Review       GuardianApprovalReview       `json:"review"`
	Action       GuardianApprovalReviewAction `json:"action"`
}

type ItemGuardianApprovalReviewCompletedNotification struct {
	ThreadID       string                       `json:"threadId"`
	TurnID         string                       `json:"turnId"`
	StartedAtMS    uint64                       `json:"startedAtMs"`
	CompletedAtMS  uint64                       `json:"completedAtMs"`
	ReviewID       string                       `json:"reviewId"`
	TargetItemID   *string                      `json:"targetItemId"`
	DecisionSource AutoReviewDecisionSource     `json:"decisionSource"`
	Review         GuardianApprovalReview       `json:"review"`
	Action         GuardianApprovalReviewAction `json:"action"`
}

type TerminalInteractionNotification struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	ProcessID string `json:"processId"`
	Stdin     string `json:"stdin"`
	Data      any    `json:"data,omitempty"`
}

func (n *TerminalInteractionNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
		ItemID    string `json:"itemId"`
		ProcessID string `json:"processId"`
		Stdin     string `json:"stdin"`
	}{
		ThreadID:  n.ThreadID,
		TurnID:    n.TurnID,
		ItemID:    n.ItemID,
		ProcessID: n.ProcessID,
		Stdin:     n.Stdin,
	})
}

type ServerRequestResolvedNotification struct {
	ThreadID  string    `json:"threadId"`
	RequestID RequestID `json:"requestId"`
	Outcome   any       `json:"outcome,omitempty"`
}

func (n *ServerRequestResolvedNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID  string    `json:"threadId"`
		RequestID RequestID `json:"requestId"`
	}{
		ThreadID:  n.ThreadID,
		RequestID: n.RequestID,
	})
}

type MCPServerStartupFailureReason string

const MCPServerStartupFailureReauthenticationRequired MCPServerStartupFailureReason = "reauthenticationRequired"

type MCPServerStatusUpdatedNotification struct {
	ThreadID      *string                        `json:"threadId"`
	Name          string                         `json:"name"`
	Status        string                         `json:"status"`
	Error         *string                        `json:"error"`
	FailureReason *MCPServerStartupFailureReason `json:"failureReason"`
}

func (n *MCPServerStatusUpdatedNotification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ThreadID      *string                        `json:"threadId"`
		Name          string                         `json:"name"`
		Status        string                         `json:"status"`
		Error         *string                        `json:"error"`
		FailureReason *MCPServerStartupFailureReason `json:"failureReason"`
	}{
		ThreadID:      cloneStringPtrAppserver(n.ThreadID),
		Name:          n.Name,
		Status:        normalizeMCPServerStartupStatus(n.Status),
		Error:         cloneStringPtrAppserver(n.Error),
		FailureReason: cloneMCPServerStartupFailureReasonPtr(n.FailureReason),
	})
}

func normalizeMCPServerStartupStatus(status string) string {
	switch status {
	case "starting", "ready", "failed", "cancelled":
		return status
	case "stopped":
		return "cancelled"
	default:
		return "failed"
	}
}

func cloneMCPServerStartupFailureReasonPtr(value *MCPServerStartupFailureReason) *MCPServerStartupFailureReason {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type MCPToolCallProgressNotification struct {
	ThreadID      string         `json:"threadId"`
	TurnID        string         `json:"turnId"`
	ItemID        string         `json:"itemId"`
	ServerName    string         `json:"serverName,omitempty"`
	ProgressToken any            `json:"progressToken,omitempty"`
	Progress      *float64       `json:"progress,omitempty"`
	Total         *float64       `json:"total,omitempty"`
	Message       string         `json:"message"`
	Params        map[string]any `json:"params,omitempty"`
}

type MCPServerOauthLoginCompletedNotification struct {
	Name     string  `json:"name"`
	ThreadID *string `json:"threadId"`
	Success  bool    `json:"success"`
	Error    *string `json:"error,omitempty"`
}
