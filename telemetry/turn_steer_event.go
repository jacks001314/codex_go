package telemetry

import (
	"context"
	"strings"
)

const CodexTurnSteerEventType = "codex_turn_steer_event"

const (
	TurnSteerResultAccepted = "accepted"
	TurnSteerResultRejected = "rejected"
)

const (
	TurnSteerRejectionNoActiveTurn        = "no_active_turn"
	TurnSteerRejectionExpectedMismatch    = "expected_turn_mismatch"
	TurnSteerRejectionNonSteerableReview  = "non_steerable_review"
	TurnSteerRejectionNonSteerableCompact = "non_steerable_compact"
	TurnSteerRejectionEmptyInput          = "empty_input"
	TurnSteerRejectionInputTooLarge       = "input_too_large"
)

type TurnSteerEventSink interface {
	TrackCodexTurnSteerEvent(context.Context, CodexTurnSteerEventRequest)
}

type CodexTurnSteerEventRequest struct {
	EventType   string                    `json:"event_type"`
	EventParams CodexTurnSteerEventParams `json:"event_params"`
}

type CodexTurnSteerEventParams struct {
	ThreadID        string                       `json:"thread_id"`
	SessionID       string                       `json:"session_id"`
	ExpectedTurnID  *string                      `json:"expected_turn_id"`
	AcceptedTurnID  *string                      `json:"accepted_turn_id"`
	AppServerClient CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime         CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource    *string                      `json:"thread_source"`
	SubagentSource  *string                      `json:"subagent_source"`
	ParentThreadID  *string                      `json:"parent_thread_id"`
	NumInputImages  int                          `json:"num_input_images"`
	Result          string                       `json:"result"`
	RejectionReason *string                      `json:"rejection_reason"`
	CreatedAt       uint64                       `json:"created_at"`
}

type CodexTurnSteerEventInput struct {
	ThreadID         string
	SessionID        string
	ExpectedTurnID   *string
	AcceptedTurnID   *string
	AppServerClient  CodexAppServerClientMetadata
	ThreadOriginator string
	Runtime          CodexRuntimeMetadata
	ThreadSource     *string
	SubagentSource   *string
	ParentThreadID   *string
	NumInputImages   int
	Result           string
	RejectionReason  *string
	CreatedAt        uint64
}

func NewCodexTurnSteerEvent(input CodexTurnSteerEventInput) CodexTurnSteerEventRequest {
	client := input.AppServerClient
	if originator := strings.TrimSpace(input.ThreadOriginator); originator != "" {
		client.ProductClientID = originator
	}
	result := strings.TrimSpace(input.Result)
	if result == "" {
		result = TurnSteerResultAccepted
	}
	return CodexTurnSteerEventRequest{
		EventType: CodexTurnSteerEventType,
		EventParams: CodexTurnSteerEventParams{
			ThreadID:        input.ThreadID,
			SessionID:       input.SessionID,
			ExpectedTurnID:  input.ExpectedTurnID,
			AcceptedTurnID:  input.AcceptedTurnID,
			AppServerClient: client,
			Runtime:         input.Runtime,
			ThreadSource:    input.ThreadSource,
			SubagentSource:  input.SubagentSource,
			ParentThreadID:  input.ParentThreadID,
			NumInputImages:  input.NumInputImages,
			Result:          result,
			RejectionReason: input.RejectionReason,
			CreatedAt:       input.CreatedAt,
		},
	}
}
