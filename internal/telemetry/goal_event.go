package telemetry

import (
	"context"
	"strings"
)

const CodexGoalEventType = "codex_goal_event"

const (
	GoalEventKindCreated        = "created"
	GoalEventKindUsageAccounted = "usage_accounted"
	GoalEventKindStatusChanged  = "status_changed"
	GoalEventKindCleared        = "cleared"
)

type GoalEventSink interface {
	TrackCodexGoalEvent(context.Context, CodexGoalEventRequest)
}

type CodexGoalEventRequest struct {
	EventType   string               `json:"event_type"`
	EventParams CodexGoalEventParams `json:"event_params"`
}

type CodexGoalEventParams struct {
	ThreadID                       string                       `json:"thread_id"`
	SessionID                      string                       `json:"session_id"`
	TurnID                         *string                      `json:"turn_id"`
	AppServerClient                CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime                        CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource                   *string                      `json:"thread_source"`
	SubagentSource                 *string                      `json:"subagent_source"`
	ParentThreadID                 *string                      `json:"parent_thread_id"`
	GoalID                         string                       `json:"goal_id"`
	EventKind                      string                       `json:"event_kind"`
	GoalStatus                     string                       `json:"goal_status"`
	HasTokenBudget                 bool                         `json:"has_token_budget"`
	CumulativeTokensAccounted      *int64                       `json:"cumulative_tokens_accounted"`
	CumulativeTimeAccountedSeconds *int64                       `json:"cumulative_time_accounted_seconds"`
}

type CodexGoalEventInput struct {
	ThreadID                       string
	SessionID                      string
	TurnID                         *string
	AppServerClient                CodexAppServerClientMetadata
	ThreadOriginator               string
	Runtime                        CodexRuntimeMetadata
	ThreadSource                   *string
	SubagentSource                 *string
	ParentThreadID                 *string
	GoalID                         string
	EventKind                      string
	GoalStatus                     string
	HasTokenBudget                 bool
	CumulativeTokensAccounted      *int64
	CumulativeTimeAccountedSeconds *int64
}

func NewCodexGoalEvent(input CodexGoalEventInput) CodexGoalEventRequest {
	client := input.AppServerClient
	if originator := strings.TrimSpace(input.ThreadOriginator); originator != "" {
		client.ProductClientID = originator
	}
	return CodexGoalEventRequest{
		EventType: CodexGoalEventType,
		EventParams: CodexGoalEventParams{
			ThreadID:                       input.ThreadID,
			SessionID:                      firstNonEmptyTelemetry(input.SessionID, input.ThreadID),
			TurnID:                         input.TurnID,
			AppServerClient:                client,
			Runtime:                        input.Runtime,
			ThreadSource:                   input.ThreadSource,
			SubagentSource:                 input.SubagentSource,
			ParentThreadID:                 input.ParentThreadID,
			GoalID:                         input.GoalID,
			EventKind:                      firstNonEmptyTelemetry(input.EventKind, GoalEventKindCreated),
			GoalStatus:                     input.GoalStatus,
			HasTokenBudget:                 input.HasTokenBudget,
			CumulativeTokensAccounted:      input.CumulativeTokensAccounted,
			CumulativeTimeAccountedSeconds: input.CumulativeTimeAccountedSeconds,
		},
	}
}
