package chatwidget

import (
	"strings"

	"codex_go/appserver"
	"codex_go/context"
	historycell "codex_go/tui/history_cell"
)

type ReplayState struct {
	Kind ReplayKind
}

type ReplayThreadItemKind string

const (
	ReplayThreadItemUserMessage        ReplayThreadItemKind = "user_message"
	ReplayThreadItemAgentMessage       ReplayThreadItemKind = "agent_message"
	ReplayThreadItemPlan               ReplayThreadItemKind = "plan"
	ReplayThreadItemReasoning          ReplayThreadItemKind = "reasoning"
	ReplayThreadItemCommandExecution   ReplayThreadItemKind = "command_execution"
	ReplayThreadItemFileChange         ReplayThreadItemKind = "file_change"
	ReplayThreadItemMcpToolCall        ReplayThreadItemKind = "mcp_tool_call"
	ReplayThreadItemWebSearch          ReplayThreadItemKind = "web_search"
	ReplayThreadItemImageView          ReplayThreadItemKind = "image_view"
	ReplayThreadItemImageGeneration    ReplayThreadItemKind = "image_generation"
	ReplayThreadItemEnteredReviewMode  ReplayThreadItemKind = "entered_review_mode"
	ReplayThreadItemExitedReviewMode   ReplayThreadItemKind = "exited_review_mode"
	ReplayThreadItemContextCompaction  ReplayThreadItemKind = "context_compaction"
	ReplayThreadItemHookPrompt         ReplayThreadItemKind = "hook_prompt"
	ReplayThreadItemCollabAgent        ReplayThreadItemKind = "collab_agent_tool_call"
	ReplayThreadItemSubAgentActivity   ReplayThreadItemKind = "sub_agent_activity"
	ReplayThreadItemDynamicToolCall    ReplayThreadItemKind = "dynamic_tool_call"
	ReplayThreadItemSleep              ReplayThreadItemKind = "sleep"
	ReplayThreadItemFunctionCallOutput ReplayThreadItemKind = "function_call_output"
)

type ReplayItemRoute string

const (
	ReplayItemRouteCommittedUserMessage   ReplayItemRoute = "committed_user_message"
	ReplayItemRouteAgentMessageCompleted  ReplayItemRoute = "agent_message_completed"
	ReplayItemRoutePlanCompleted          ReplayItemRoute = "plan_completed"
	ReplayItemRouteReasoningFinalizeOnly  ReplayItemRoute = "reasoning_finalize_only"
	ReplayItemRouteReasoningReplaySummary ReplayItemRoute = "reasoning_replay_summary"
	ReplayItemRouteReasoningReplayRaw     ReplayItemRoute = "reasoning_replay_raw"
	ReplayItemRouteCommandStarted         ReplayItemRoute = "command_started"
	ReplayItemRouteCommandCompleted       ReplayItemRoute = "command_completed"
	ReplayItemRouteIgnoreInProgressPatch  ReplayItemRoute = "ignore_in_progress_patch"
	ReplayItemRouteFileChangeCompleted    ReplayItemRoute = "file_change_completed"
	ReplayItemRouteMcpToolStarted         ReplayItemRoute = "mcp_tool_started"
	ReplayItemRouteMcpToolCompleted       ReplayItemRoute = "mcp_tool_completed"
	ReplayItemRouteWebSearchBeginEnd      ReplayItemRoute = "web_search_begin_end"
	ReplayItemRouteViewImage              ReplayItemRoute = "view_image"
	ReplayItemRouteImageGenerationEnd     ReplayItemRoute = "image_generation_end"
	ReplayItemRouteEnterReviewMode        ReplayItemRoute = "enter_review_mode"
	ReplayItemRouteExitReviewMode         ReplayItemRoute = "exit_review_mode"
	ReplayItemRouteContextCompactedInfo   ReplayItemRoute = "context_compacted_info"
	ReplayItemRouteIgnoreHookPrompt       ReplayItemRoute = "ignore_hook_prompt"
	ReplayItemRouteCollabAgentToolCall    ReplayItemRoute = "collab_agent_tool_call"
	ReplayItemRouteSubAgentActivity       ReplayItemRoute = "sub_agent_activity"
	ReplayItemRouteIgnoreDynamicToolCall  ReplayItemRoute = "ignore_dynamic_tool_call"
	ReplayItemRouteIgnoreSleep            ReplayItemRoute = "ignore_sleep"
	ReplayItemRouteFunctionCallOutput     ReplayItemRoute = "function_call_output"
	ReplayItemRouteUnknown                ReplayItemRoute = "unknown"
)

type ReplayItemDecision struct {
	Route                ReplayItemRoute
	FromReplay           bool
	ReplayKind           ReplayKind
	RequestRedraw        bool
	ReplayReasoningDelta bool
}

func (s ReplayState) FromReplay() bool {
	return s.Kind != ReplayNone
}

func (s ReplayState) IsResumeInitialMessages() bool {
	return s.Kind == ReplayResumeInitialMessages
}

func ShouldSuppressDuringReplay(replay ReplayKind, notification ServerNotificationKind) bool {
	return replay == ReplayResumeInitialMessages && (notification == NotificationTurnStarted || notification == NotificationError)
}

func ClassifyReplayThreadItem(kind ReplayThreadItemKind, status string, replay ReplayKind, turnID string, showRawReasoning bool) ReplayItemDecision {
	decision := ReplayItemDecision{
		FromReplay:    replay != ReplayNone,
		ReplayKind:    replay,
		RequestRedraw: replay == ReplayThreadSnapshot && turnID == "",
	}
	switch kind {
	case ReplayThreadItemUserMessage:
		decision.Route = ReplayItemRouteCommittedUserMessage
	case ReplayThreadItemAgentMessage:
		decision.Route = ReplayItemRouteAgentMessageCompleted
	case ReplayThreadItemPlan:
		decision.Route = ReplayItemRoutePlanCompleted
	case ReplayThreadItemReasoning:
		if replay == ReplayNone {
			decision.Route = ReplayItemRouteReasoningFinalizeOnly
		} else if showRawReasoning {
			decision.Route = ReplayItemRouteReasoningReplayRaw
			decision.ReplayReasoningDelta = true
		} else {
			decision.Route = ReplayItemRouteReasoningReplaySummary
			decision.ReplayReasoningDelta = true
		}
	case ReplayThreadItemCommandExecution:
		if isReplayInProgressStatus(status) {
			decision.Route = ReplayItemRouteCommandStarted
		} else {
			decision.Route = ReplayItemRouteCommandCompleted
		}
	case ReplayThreadItemFileChange:
		if isReplayInProgressStatus(status) {
			decision.Route = ReplayItemRouteIgnoreInProgressPatch
		} else {
			decision.Route = ReplayItemRouteFileChangeCompleted
		}
	case ReplayThreadItemMcpToolCall:
		if isReplayInProgressStatus(status) {
			decision.Route = ReplayItemRouteMcpToolStarted
		} else {
			decision.Route = ReplayItemRouteMcpToolCompleted
		}
	case ReplayThreadItemWebSearch:
		decision.Route = ReplayItemRouteWebSearchBeginEnd
	case ReplayThreadItemImageView:
		decision.Route = ReplayItemRouteViewImage
	case ReplayThreadItemImageGeneration:
		decision.Route = ReplayItemRouteImageGenerationEnd
	case ReplayThreadItemEnteredReviewMode:
		if replay == ReplayNone {
			decision.Route = ReplayItemRouteUnknown
		} else {
			decision.Route = ReplayItemRouteEnterReviewMode
		}
	case ReplayThreadItemExitedReviewMode:
		decision.Route = ReplayItemRouteExitReviewMode
	case ReplayThreadItemContextCompaction:
		decision.Route = ReplayItemRouteContextCompactedInfo
	case ReplayThreadItemHookPrompt:
		decision.Route = ReplayItemRouteIgnoreHookPrompt
	case ReplayThreadItemCollabAgent:
		decision.Route = ReplayItemRouteCollabAgentToolCall
	case ReplayThreadItemSubAgentActivity:
		decision.Route = ReplayItemRouteSubAgentActivity
	case ReplayThreadItemDynamicToolCall:
		decision.Route = ReplayItemRouteIgnoreDynamicToolCall
	case ReplayThreadItemSleep:
		decision.Route = ReplayItemRouteIgnoreSleep
	case ReplayThreadItemFunctionCallOutput:
		// Rust #41046: delegated create_thread / send_message_to_thread outputs
		// render a source-attributed notice; other function outputs are ignored.
		decision.Route = ReplayItemRouteFunctionCallOutput
	default:
		decision.Route = ReplayItemRouteUnknown
	}
	return decision
}

// ReplayDelegatedToolOutput parses a replayed function-call output item that
// carries a delegated prompt from a trusted Codex namespace (Rust #41046
// parse_delegated_tool_output).
func ReplayDelegatedToolOutput(item appserver.ThreadItem) (string, string, bool) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(item.Type), "_", ""), "-", "")
	if !strings.EqualFold(normalized, "functioncalloutput") {
		return "", "", false
	}
	output := ""
	if item.Data != nil {
		if value, ok := item.Data["output"].(string); ok {
			output = value
		}
	}
	if output == "" {
		output = item.Text
	}
	return context.ParseDelegatedToolOutput(item.Name, item.Namespace, output)
}

// ReplayDelegatedToolOutputCell returns the history cell Rust renders for a
// replayed delegated tool output, reporting whether the item was one (#41046).
func ReplayDelegatedToolOutputCell(item appserver.ThreadItem) (historycell.HistoryCell, bool) {
	sourceThreadID, prompt, ok := ReplayDelegatedToolOutput(item)
	if !ok {
		return nil, false
	}
	return historycell.NewDelegatedPromptCell(sourceThreadID, prompt), true
}

func isReplayInProgressStatus(status string) bool {
	return status == "in_progress" || status == "InProgress" || status == "running"
}
