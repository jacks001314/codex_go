package chatwidget

import (
	"encoding/json"
	"strings"

	historycell "codex_go/internal/tui/history_cell"
)

const SafetyAccessBlockPrefix = "Invalid prompt: we've limited access to this content for safety reasons."

type TurnRuntimeNotificationKind string

const (
	TurnRuntimeNotificationAgentTurnComplete TurnRuntimeNotificationKind = "agent_turn_complete"
	TurnRuntimeNotificationPlanModePrompt    TurnRuntimeNotificationKind = "plan_mode_prompt"
)

type TurnRuntimeNotification struct {
	Kind     TurnRuntimeNotificationKind
	Title    string
	Response string
}

type TurnRuntimeHistoryKind string

const (
	TurnRuntimeHistoryError                 TurnRuntimeHistoryKind = "error"
	TurnRuntimeHistoryWarning               TurnRuntimeHistoryKind = "warning"
	TurnRuntimeHistorySafetyAccessBlock     TurnRuntimeHistoryKind = "safety_access_block"
	TurnRuntimeHistoryCyberPolicy           TurnRuntimeHistoryKind = "cyber_policy"
	TurnRuntimeHistoryFinalMessageSeparator TurnRuntimeHistoryKind = "final_message_separator"
)

type TurnRuntimeHistoryEvent struct {
	Kind    TurnRuntimeHistoryKind
	Message string
}

type TurnRuntimeStatusKind string

const (
	TurnRuntimeStatusWorking TurnRuntimeStatusKind = "working"
	TurnRuntimeStatusFailed  TurnRuntimeStatusKind = "failed"
)

type TurnRuntimeErrorOutcome string

const (
	TurnRuntimeErrorNone              TurnRuntimeErrorOutcome = ""
	TurnRuntimeErrorRejectedSteer     TurnRuntimeErrorOutcome = "rejected_steer"
	TurnRuntimeErrorCyberPolicy       TurnRuntimeErrorOutcome = "cyber_policy"
	TurnRuntimeErrorSafetyAccessBlock TurnRuntimeErrorOutcome = "safety_access_block"
	TurnRuntimeErrorServerOverloaded  TurnRuntimeErrorOutcome = "server_overloaded"
	TurnRuntimeErrorRateLimit         TurnRuntimeErrorOutcome = "rate_limit"
	TurnRuntimeErrorGeneric           TurnRuntimeErrorOutcome = "generic_error"
)

type TurnRuntimeCodexErrorKind string

const (
	TurnRuntimeCodexErrorActiveTurnNotSteerable     TurnRuntimeCodexErrorKind = "ActiveTurnNotSteerable"
	TurnRuntimeCodexErrorCyberPolicy                TurnRuntimeCodexErrorKind = "CyberPolicy"
	TurnRuntimeCodexErrorServerOverloaded           TurnRuntimeCodexErrorKind = "ServerOverloaded"
	TurnRuntimeCodexErrorUsageLimitExceeded         TurnRuntimeCodexErrorKind = "UsageLimitExceeded"
	TurnRuntimeCodexErrorResponseTooManyFailedTries TurnRuntimeCodexErrorKind = "ResponseTooManyFailedAttempts"
)

type TurnRuntimeCodexErrorInfo struct {
	Kind           TurnRuntimeCodexErrorKind
	HTTPStatusCode int
}

type RateLimitErrorKind string

const (
	RateLimitErrorServerOverloaded RateLimitErrorKind = "server_overloaded"
	RateLimitErrorUsageLimit       RateLimitErrorKind = "usage_limit"
	RateLimitErrorGeneric          RateLimitErrorKind = "generic"
)

type TurnRateLimitReachedType string

const (
	TurnRateLimitReached                 TurnRateLimitReachedType = "rate_limit_reached"
	TurnWorkspaceOwnerCreditsDepleted    TurnRateLimitReachedType = "workspace_owner_credits_depleted"
	TurnWorkspaceMemberCreditsDepleted   TurnRateLimitReachedType = "workspace_member_credits_depleted"
	TurnWorkspaceOwnerUsageLimitReached  TurnRateLimitReachedType = "workspace_owner_usage_limit_reached"
	TurnWorkspaceMemberUsageLimitReached TurnRateLimitReachedType = "workspace_member_usage_limit_reached"
)

type AddCreditsNudgeCreditType string

const (
	AddCreditsNudgeCredits    AddCreditsNudgeCreditType = "credits"
	AddCreditsNudgeUsageLimit AddCreditsNudgeCreditType = "usage_limit"
)

type TurnCompleteRuntimeParams struct {
	TurnID           string
	LastAgentMessage string
	DurationMS       *int64
	FromReplay       bool
}

type TurnCompleteRuntimeResult struct {
	Completed              bool
	FollowUpStarted        bool
	NotificationSent       bool
	PlanPromptOpened       bool
	HadPendingSteers       bool
	FinalSeparatorAdded    bool
	RuntimeMetricsIncluded bool
	NotificationResponse   string
}

type TurnRuntimeState struct {
	Lifecycle       TurnLifecycleState
	SafetyBuffering SafetyBufferingState
	Streaming       ChatStreamingState
	Tools           ToolLifecycleState

	InputQueue InputQueueState

	MCPStartupActive bool
	TaskRunning      bool

	InterruptHintVisible bool
	StatusHeader         string
	StatusKind           TurnRuntimeStatusKind
	PendingStatusRestore bool

	PlanModeActive            bool
	CollaborationModesEnabled bool
	ModalOrPopupActive        bool
	DefaultModeAvailable      bool
	SawPlanItemThisTurn       bool
	LatestProposedPlan        string
	PlanImplementationPrompt  *SelectionView

	RateLimitSwitchPrompt     RateLimitSwitchPromptState
	LowerCostSwitchPreset     *RateLimitSwitchPreset
	RateLimitSwitchPromptView *SelectionView
	RateLimitReachedType      TurnRateLimitReachedType
	WorkspaceOwnerNudge       *AddCreditsNudgeCreditType

	ContextWindowSize *int64
	LastTokenUsage    StatusTokenUsage
	ContextUsedTokens *int64

	RuntimeMetrics             historycell.RuntimeMetricsSummary
	HadWorkActivity            bool
	NeedsFinalMessageSeparator bool
	FinalMessageSeparators     []historycell.FinalMessageSeparator
	UsageInsertionRequests     int

	LastAgentMarkdown     string
	SawCopySourceThisTurn bool

	RunningCommands     []string
	SuppressedExecCalls []string
	History             []historycell.HistoryCell
	HistoryEvents       []TurnRuntimeHistoryEvent
	Notifications       []TurnRuntimeNotification
	PendingInputPreview PendingInputPreview

	ActiveGoalContinuing      bool
	FollowUpStartedCount      int
	RequestRedrawCount        int
	BranchRefreshRequests     int
	GitSummaryRefreshRequests int
	LastErrorOutcome          TurnRuntimeErrorOutcome
	LastErrorMessage          string
	PetNotificationKind       string
}

func (s *TurnRuntimeState) StartTurn(turnID string) {
	if s == nil {
		return
	}
	s.Lifecycle.Start(turnID)
	s.SafetyBuffering.RecordTurn(turnID)
}

func (s *TurnRuntimeState) CompleteTurn(turnID string) bool {
	if s == nil {
		return false
	}
	s.SafetyBuffering.Clear()
	s.Tools.Active = nil
	return s.Lifecycle.Complete(turnID)
}

func (s *TurnRuntimeState) UpdateTaskRunningState() bool {
	if s == nil {
		return false
	}
	next := s.Lifecycle.AgentTurnRunning || s.MCPStartupActive
	changed := s.TaskRunning != next
	s.TaskRunning = next
	s.Streaming.TaskRunning = next
	return changed
}

func (s *TurnRuntimeState) ApplyRuntimeMetricsDelta(delta historycell.RuntimeMetricsSummary) {
	if s == nil {
		return
	}
	mergeRuntimeMetricCountDuration(&s.RuntimeMetrics.ToolCalls, delta.ToolCalls)
	mergeRuntimeMetricCountDuration(&s.RuntimeMetrics.APICalls, delta.APICalls)
	mergeRuntimeMetricCountDuration(&s.RuntimeMetrics.WebSocketCalls, delta.WebSocketCalls)
	mergeRuntimeMetricCountDuration(&s.RuntimeMetrics.StreamingEvents, delta.StreamingEvents)
	mergeRuntimeMetricCountDuration(&s.RuntimeMetrics.WebSocketEvents, delta.WebSocketEvents)
	s.RuntimeMetrics.ResponsesAPIOverheadMS += delta.ResponsesAPIOverheadMS
	s.RuntimeMetrics.ResponsesAPIInferenceTimeMS += delta.ResponsesAPIInferenceTimeMS
	s.RuntimeMetrics.ResponsesAPIEngineIAPITTFTMS += delta.ResponsesAPIEngineIAPITTFTMS
	s.RuntimeMetrics.ResponsesAPIEngineServiceTTFTMS += delta.ResponsesAPIEngineServiceTTFTMS
	s.RuntimeMetrics.ResponsesAPIEngineIAPITBTMS += delta.ResponsesAPIEngineIAPITBTMS
	s.RuntimeMetrics.ResponsesAPIEngineServiceTBTMS += delta.ResponsesAPIEngineServiceTBTMS
}

func (s *TurnRuntimeState) OnTaskStarted(turnID string) {
	if s == nil {
		return
	}
	s.InputQueue.UserTurnPendingStart = false
	s.SafetyBuffering.ActiveTurnID = ""
	s.SafetyBuffering.RetryPromptShown = false
	s.SafetyBuffering.AgentMessageStarted = false
	s.Lifecycle.Start(turnID)
	if turnID != "" {
		s.SafetyBuffering.RecordTurn(turnID)
	}
	s.Streaming.TaskCompletePending = false
	s.Streaming.FinalizedAnswerSource = ""
	s.Streaming.FinalizedPlanSource = ""
	s.Streaming.VisibleTurnActivity = 0
	if s.Streaming.PlanStreamController != nil {
		s.Streaming.PlanStreamController = nil
		s.Streaming.ClearActiveStreamTail()
		s.UsageInsertionRequests++
	}
	if s.Streaming.AdaptiveChunking != nil {
		s.Streaming.AdaptiveChunking.Reset()
	}
	s.RuntimeMetrics = historycell.RuntimeMetricsSummary{}
	s.InterruptHintVisible = true
	s.PendingStatusRestore = false
	s.UpdateTaskRunningState()
	s.StatusKind = TurnRuntimeStatusWorking
	if !s.MCPStartupActive || !(McpStartupRoundState{}).StatusHeaderIsMcpStartupOwned(s.StatusHeader) {
		s.StatusHeader = "Working"
	}
	s.Streaming.ReasoningBuffer = ""
	s.Streaming.FullReasoningBuffer = ""
	s.PetNotificationKind = "running"
	s.RequestRedraw()
}

func (s *TurnRuntimeState) OnTaskComplete(params TurnCompleteRuntimeParams) TurnCompleteRuntimeResult {
	result := TurnCompleteRuntimeResult{}
	if s == nil {
		return result
	}
	s.InputQueue.SubmitPendingSteersAfterInterrupt = false
	sanitizedLastAgentMessage := strings.TrimSpace(params.LastAgentMessage)
	if sanitizedLastAgentMessage != "" && !s.SawCopySourceThisTurn {
		s.LastAgentMarkdown = sanitizedLastAgentMessage
	}
	notificationResponse := sanitizedLastAgentMessage
	if notificationResponse == "" && s.SawCopySourceThisTurn {
		notificationResponse = s.LastAgentMarkdown
	}
	s.SawCopySourceThisTurn = false
	result.NotificationResponse = notificationResponse

	s.Streaming.FlushAnswerStreamWithSeparator()
	if s.Streaming.UsageInsertionRequests > 0 {
		s.UsageInsertionRequests += s.Streaming.UsageInsertionRequests
		s.Streaming.UsageInsertionRequests = 0
	}
	if s.Streaming.PlanStreamController != nil {
		hadLiveTail := s.Streaming.PlanStreamController.HasLiveTail()
		s.Streaming.ClearActiveStreamTail()
		cell, source := s.Streaming.PlanStreamController.Finalize()
		s.Streaming.PlanStreamController = nil
		if !hadLiveTail && cell != nil {
			s.addHistory(cell)
		}
		if source != "" {
			s.Streaming.FinalizedPlanSource = source
			s.LatestProposedPlan = source
		}
		s.UsageInsertionRequests++
	}

	if !params.FromReplay {
		runtimeMetrics := s.RuntimeMetrics
		runtimeMetricsIncluded := !runtimeMetricsEmpty(runtimeMetrics)
		showWorkSeparator := s.HadWorkActivity && (s.NeedsFinalMessageSeparator || runtimeMetricsIncluded)
		if showWorkSeparator || runtimeMetricsIncluded {
			var elapsed *int64
			if showWorkSeparator && params.DurationMS != nil && *params.DurationMS >= 0 {
				value := *params.DurationMS / 1000
				elapsed = &value
			}
			var metrics *historycell.RuntimeMetricsSummary
			if runtimeMetricsIncluded {
				copy := runtimeMetrics
				metrics = &copy
			}
			separator := historycell.NewFinalMessageSeparator(elapsed, metrics)
			s.FinalMessageSeparators = append(s.FinalMessageSeparators, separator)
			s.addHistory(separator)
			s.HistoryEvents = append(s.HistoryEvents, TurnRuntimeHistoryEvent{Kind: TurnRuntimeHistoryFinalMessageSeparator})
			result.FinalSeparatorAdded = true
			result.RuntimeMetricsIncluded = runtimeMetricsIncluded
		}
		s.RuntimeMetrics = historycell.RuntimeMetricsSummary{}
		s.NeedsFinalMessageSeparator = false
		s.HadWorkActivity = false
		s.RequestStatusLineBranchRefresh()
		s.RequestStatusLineGitSummaryRefresh()
	}

	s.PendingStatusRestore = false
	s.InputQueue.UserTurnPendingStart = false
	s.Tools.Active = nil
	result.Completed = s.Lifecycle.Complete(params.TurnID)
	s.SafetyBuffering.Clear()
	s.UpdateTaskRunningState()
	s.RunningCommands = nil
	s.SuppressedExecCalls = nil
	if !params.FromReplay {
		s.PetNotificationKind = "review"
	}
	s.RequestRedraw()

	result.HadPendingSteers = len(s.InputQueue.PendingSteers) > 0
	s.PendingInputPreview = s.InputQueue.Preview()
	if !params.FromReplay && !s.HasQueuedFollowUpMessages() && !result.HadPendingSteers {
		result.PlanPromptOpened = s.MaybePromptPlanImplementation()
	}
	if !params.FromReplay {
		s.SawPlanItemThisTurn = false
		s.Streaming.SawPlanItemThisTurn = false
	}
	result.FollowUpStarted = s.MaybeSendNextQueuedInput()
	if !result.FollowUpStarted && !s.ActiveGoalContinuing {
		s.Notify(TurnRuntimeNotification{
			Kind:     TurnRuntimeNotificationAgentTurnComplete,
			Response: notificationResponse,
		})
		result.NotificationSent = true
	}
	s.MaybeShowPendingRateLimitPrompt()
	return result
}

func (s *TurnRuntimeState) MaybePromptPlanImplementation() bool {
	if s == nil ||
		!s.CollaborationModesEnabled ||
		s.HasQueuedFollowUpMessages() ||
		!s.PlanModeActive ||
		!s.sawPlanItemThisTurn() ||
		s.ModalOrPopupActive ||
		s.RateLimitSwitchPrompt == RateLimitSwitchPromptPending {
		return false
	}
	s.OpenPlanImplementationPrompt()
	return true
}

func (s *TurnRuntimeState) OpenPlanImplementationPrompt() {
	if s == nil {
		return
	}
	label, _ := s.PlanImplementationContextUsageLabel()
	view := NewPlanImplementationView(PlanImplementationConfig{
		DefaultModeAvailable:   s.DefaultModeAvailable,
		PlanMarkdown:           s.latestProposedPlan(),
		ClearContextUsageLabel: label,
	})
	s.PlanImplementationPrompt = &view
	s.Notify(TurnRuntimeNotification{
		Kind:  TurnRuntimeNotificationPlanModePrompt,
		Title: PlanImplementationTitle,
	})
}

func (s *TurnRuntimeState) PlanImplementationContextUsageLabel() (string, bool) {
	if s == nil {
		return "", false
	}
	if s.ContextWindowSize != nil {
		remaining := s.LastTokenUsage.PercentOfContextWindowRemaining(*s.ContextWindowSize)
		usedPercent := 100 - clampInt64(remaining, 0, 100)
		if usedPercent > 0 {
			return formatInt64(usedPercent) + "% used", true
		}
		return "", false
	}
	usedTokens := int64(0)
	if s.ContextUsedTokens != nil {
		usedTokens = *s.ContextUsedTokens
	} else {
		usedTokens = s.LastTokenUsage.TokensInContextWindow()
	}
	if usedTokens > 0 {
		return FormatTokensCompact(usedTokens) + " used", true
	}
	return "", false
}

func (s *TurnRuntimeState) HasQueuedFollowUpMessages() bool {
	return s != nil && s.InputQueue.HasQueuedFollowUpMessages()
}

func (s *TurnRuntimeState) EnqueueRejectedSteer() bool {
	if s == nil || len(s.InputQueue.PendingSteers) == 0 {
		return false
	}
	pending := s.InputQueue.PendingSteers[0]
	s.InputQueue.PendingSteers = s.InputQueue.PendingSteers[1:]
	s.InputQueue.RejectedSteersQueue = append(s.InputQueue.RejectedSteersQueue, pending.UserMessage)
	s.InputQueue.RejectedSteerHistoryRecords = append(s.InputQueue.RejectedSteerHistoryRecords, pending.HistoryRecord)
	s.PendingInputPreview = s.InputQueue.Preview()
	return true
}

func (s *TurnRuntimeState) FinalizeTurn() {
	if s == nil {
		return
	}
	s.SafetyBuffering.Clear()
	s.Streaming.ClearActiveStreamTail()
	s.Tools.Active = nil
	s.InputQueue.UserTurnPendingStart = false
	s.Lifecycle.Complete("")
	s.UpdateTaskRunningState()
	s.RunningCommands = nil
	s.SuppressedExecCalls = nil
	if s.Streaming.AdaptiveChunking != nil {
		s.Streaming.AdaptiveChunking.Reset()
	}
	if s.Streaming.StreamController != nil || s.Streaming.PlanStreamController != nil {
		s.Streaming.StreamController = nil
		s.Streaming.PlanStreamController = nil
		s.UsageInsertionRequests++
	}
	s.PendingStatusRestore = false
	s.RequestStatusLineBranchRefresh()
	s.RequestStatusLineGitSummaryRefresh()
	s.MaybeShowPendingRateLimitPrompt()
}

func (s *TurnRuntimeState) OnServerOverloadedError(message string) {
	if s == nil {
		return
	}
	s.InputQueue.SubmitPendingSteersAfterInterrupt = false
	s.FinalizeTurn()
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Codex is currently experiencing high load."
	}
	s.LastErrorOutcome = TurnRuntimeErrorServerOverloaded
	s.LastErrorMessage = message
	s.addHistoryEvent(TurnRuntimeHistoryWarning, message, historycell.NewWarningEvent(message))
	s.RequestRedraw()
	s.MaybeSendNextQueuedInput()
}

func (s *TurnRuntimeState) OnError(message string) {
	if s == nil {
		return
	}
	s.InputQueue.SubmitPendingSteersAfterInterrupt = false
	s.Streaming.FlushAnswerStreamWithSeparator()
	s.FinalizeTurn()
	message = strings.TrimSpace(message)
	s.LastErrorOutcome = TurnRuntimeErrorGeneric
	s.LastErrorMessage = message
	s.addHistoryEvent(TurnRuntimeHistoryError, message, historycell.NewErrorEvent(message))
	s.PetNotificationKind = "failed"
	s.RequestRedraw()
	s.MaybeSendNextQueuedInput()
}

func (s *TurnRuntimeState) OnCyberPolicyError() {
	if s == nil {
		return
	}
	s.InputQueue.SubmitPendingSteersAfterInterrupt = false
	s.FinalizeTurn()
	s.LastErrorOutcome = TurnRuntimeErrorCyberPolicy
	s.addHistoryEvent(TurnRuntimeHistoryCyberPolicy, "", historycell.NewCyberPolicyErrorEvent())
	s.RequestRedraw()
	s.MaybeSendNextQueuedInput()
}

func (s *TurnRuntimeState) OnRateLimitError(errorKind RateLimitErrorKind, message string) {
	if s == nil {
		return
	}
	usageLimitError := errorKind == RateLimitErrorUsageLimit
	if usageLimitError {
		switch s.RateLimitReachedType {
		case TurnWorkspaceOwnerCreditsDepleted:
			s.RateLimitReachedType = TurnWorkspaceOwnerUsageLimitReached
		case TurnWorkspaceMemberCreditsDepleted:
			s.RateLimitReachedType = TurnWorkspaceMemberUsageLimitReached
		}
	}
	s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	switch s.RateLimitReachedType {
	case TurnWorkspaceOwnerCreditsDepleted:
		s.OnError("You're out of credits. Your workspace is out of credits. Add credits to continue using Codex.")
		s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	case TurnWorkspaceOwnerUsageLimitReached:
		s.OnError("Usage limit reached. You've reached your usage limit. Increase your limits to continue using codex.")
		s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	case TurnWorkspaceMemberCreditsDepleted:
		s.OnError(message)
		s.OpenWorkspaceOwnerNudgePrompt(AddCreditsNudgeCredits)
		s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	case TurnWorkspaceMemberUsageLimitReached:
		s.OnError(message)
		s.OpenWorkspaceOwnerNudgePrompt(AddCreditsNudgeUsageLimit)
		s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	default:
		s.OnError(message)
		s.LastErrorOutcome = TurnRuntimeErrorRateLimit
	}
}

func (s *TurnRuntimeState) HandleNonRetryError(message string, info *TurnRuntimeCodexErrorInfo) TurnRuntimeErrorOutcome {
	if s == nil {
		return TurnRuntimeErrorNone
	}
	if info != nil && info.Kind == TurnRuntimeCodexErrorActiveTurnNotSteerable && s.EnqueueRejectedSteer() {
		s.LastErrorOutcome = TurnRuntimeErrorRejectedSteer
		return TurnRuntimeErrorRejectedSteer
	}
	if info != nil && info.Kind == TurnRuntimeCodexErrorCyberPolicy {
		s.OnCyberPolicyError()
		return TurnRuntimeErrorCyberPolicy
	}
	if IsSafetyAccessBlockMessage(message) {
		s.InputQueue.SubmitPendingSteersAfterInterrupt = false
		s.FinalizeTurn()
		s.LastErrorOutcome = TurnRuntimeErrorSafetyAccessBlock
		s.addHistoryEvent(TurnRuntimeHistorySafetyAccessBlock, "", historycell.NewSafetyAccessBlockEvent())
		s.RequestRedraw()
		s.MaybeSendNextQueuedInput()
		return TurnRuntimeErrorSafetyAccessBlock
	}
	if kind, ok := AppServerRateLimitErrorKind(info); ok {
		if kind == RateLimitErrorServerOverloaded {
			s.OnServerOverloadedError(message)
			return TurnRuntimeErrorServerOverloaded
		}
		s.OnRateLimitError(kind, message)
		return TurnRuntimeErrorRateLimit
	}
	s.OnError(message)
	return TurnRuntimeErrorGeneric
}

func IsSafetyAccessBlockMessage(message string) bool {
	if strings.HasPrefix(message, SafetyAccessBlockPrefix) {
		return true
	}
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(message), &response); err != nil {
		return false
	}
	return strings.HasPrefix(response.Error.Message, SafetyAccessBlockPrefix)
}

func AppServerRateLimitErrorKind(info *TurnRuntimeCodexErrorInfo) (RateLimitErrorKind, bool) {
	if info == nil {
		return "", false
	}
	switch info.Kind {
	case TurnRuntimeCodexErrorServerOverloaded:
		return RateLimitErrorServerOverloaded, true
	case TurnRuntimeCodexErrorUsageLimitExceeded:
		return RateLimitErrorUsageLimit, true
	case TurnRuntimeCodexErrorResponseTooManyFailedTries:
		if info.HTTPStatusCode == 429 {
			return RateLimitErrorGeneric, true
		}
	}
	return "", false
}

func (s *TurnRuntimeState) InterruptedTurnMessage(reason TurnAbortReason) string {
	if reason == TurnAbortBudgetLimited {
		return "Goal budget reached - the turn was stopped."
	}
	return "Conversation interrupted - tell the model what to do differently. Something went wrong? Hit `/feedback` to report the issue."
}

func (s *TurnRuntimeState) MaybeSendNextQueuedInput() bool {
	if s == nil || s.InputQueue.SuppressQueueAutosend {
		return false
	}
	if _, _, ok := s.InputQueue.PopNextQueuedUserMessage(); ok {
		s.InputQueue.UserTurnPendingStart = true
		s.FollowUpStartedCount++
		s.PendingInputPreview = s.InputQueue.Preview()
		return true
	}
	return false
}

func (s *TurnRuntimeState) MaybeShowPendingRateLimitPrompt() bool {
	if s == nil || s.RateLimitSwitchPrompt != RateLimitSwitchPromptPending {
		return false
	}
	if s.LowerCostSwitchPreset == nil {
		s.RateLimitSwitchPrompt = RateLimitSwitchPromptIdle
		return false
	}
	view := NewRateLimitSwitchPromptView(*s.LowerCostSwitchPreset)
	s.RateLimitSwitchPromptView = &view
	s.RateLimitSwitchPrompt = RateLimitSwitchPromptShown
	return true
}

func (s *TurnRuntimeState) OpenWorkspaceOwnerNudgePrompt(creditType AddCreditsNudgeCreditType) {
	if s == nil {
		return
	}
	value := creditType
	s.WorkspaceOwnerNudge = &value
}

func (s *TurnRuntimeState) Notify(notification TurnRuntimeNotification) {
	if s == nil {
		return
	}
	s.Notifications = append(s.Notifications, notification)
}

func (s *TurnRuntimeState) RequestRedraw() {
	if s != nil {
		s.RequestRedrawCount++
	}
}

func (s *TurnRuntimeState) RequestStatusLineBranchRefresh() {
	if s != nil {
		s.BranchRefreshRequests++
	}
}

func (s *TurnRuntimeState) RequestStatusLineGitSummaryRefresh() {
	if s != nil {
		s.GitSummaryRefreshRequests++
	}
}

func (s *TurnRuntimeState) sawPlanItemThisTurn() bool {
	return s.SawPlanItemThisTurn || s.Streaming.SawPlanItemThisTurn
}

func (s *TurnRuntimeState) latestProposedPlan() string {
	if strings.TrimSpace(s.LatestProposedPlan) != "" {
		return s.LatestProposedPlan
	}
	return s.Streaming.LatestProposedPlan
}

func (s *TurnRuntimeState) addHistory(cell historycell.HistoryCell) {
	if s != nil && cell != nil {
		s.History = append(s.History, cell)
	}
}

func (s *TurnRuntimeState) addHistoryEvent(kind TurnRuntimeHistoryKind, message string, cell historycell.HistoryCell) {
	if s == nil {
		return
	}
	s.HistoryEvents = append(s.HistoryEvents, TurnRuntimeHistoryEvent{
		Kind:    kind,
		Message: strings.TrimSpace(message),
	})
	s.addHistory(cell)
}

func mergeRuntimeMetricCountDuration(target *historycell.RuntimeMetricCountDuration, delta historycell.RuntimeMetricCountDuration) {
	if target == nil {
		return
	}
	target.Count += delta.Count
	target.DurationMS += delta.DurationMS
}

func runtimeMetricsEmpty(summary historycell.RuntimeMetricsSummary) bool {
	return summary.ToolCalls.Count == 0 &&
		summary.ToolCalls.DurationMS == 0 &&
		summary.APICalls.Count == 0 &&
		summary.APICalls.DurationMS == 0 &&
		summary.WebSocketCalls.Count == 0 &&
		summary.WebSocketCalls.DurationMS == 0 &&
		summary.StreamingEvents.Count == 0 &&
		summary.StreamingEvents.DurationMS == 0 &&
		summary.WebSocketEvents.Count == 0 &&
		summary.WebSocketEvents.DurationMS == 0 &&
		summary.ResponsesAPIOverheadMS == 0 &&
		summary.ResponsesAPIInferenceTimeMS == 0 &&
		summary.ResponsesAPIEngineIAPITTFTMS == 0 &&
		summary.ResponsesAPIEngineServiceTTFTMS == 0 &&
		summary.ResponsesAPIEngineIAPITBTMS == 0 &&
		summary.ResponsesAPIEngineServiceTBTMS == 0
}
