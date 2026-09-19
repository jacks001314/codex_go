package appserver

import (
	"context"
	"strings"

	"codex_go/telemetry"
	"codex_go/tool"
)

// emitCodeModeToolCallEvent mirrors Rust #36729's code-mode dynamic tool-call
// event: every finished code-mode exec/wait call reports one event with the cell
// it ran in, its observed window and its terminal status. Rust builds it from the
// CodeModeToolCallFact::Completed fact, sets the cell id explicitly, and buffers
// the event like every other correlated tool event.
func (r *RuntimeRouter) emitCodeModeToolCallEvent(threadID string, turnID string, observation tool.CodeModeCallObservation) {
	if r == nil || r.services.Analytics == nil {
		return
	}
	callID := strings.TrimSpace(observation.CallID)
	if callID == "" || r.threadAnalyticsDisabled(threadID) {
		return
	}
	sink, ok := r.services.Analytics.(telemetry.DynamicToolCallEventSink)
	if !ok {
		return
	}
	connectionID := ""
	if active := r.activeRuntimeTurnStateSnapshot(threadID, turnID); active != nil {
		connectionID = active.ConnectionID
	}
	client, ok := r.analyticsAppServerClient(connectionID)
	if !ok {
		return
	}
	lineage := r.responsesMetadataLineage(threadID)
	toolName := firstNonEmpty(strings.TrimSpace(observation.ToolName), tool.CodeModeExecToolName)
	terminalStatus, failureKind := codeModeCallAnalyticsOutcome(observation.Status)
	durationMS := uint64PtrFromNonNegativeInt64(int64(observation.CompletedAtMS) - int64(observation.StartedAtMS))
	base := telemetry.CodexToolItemEventBase{
		ThreadID:        threadID,
		SessionID:       strings.TrimSpace(lineage.SessionID),
		TurnID:          turnID,
		ItemID:          callID,
		CellID:          stringPtrIfNotEmpty(observation.CellID),
		AppServerClient: client,
		Runtime:         telemetry.CurrentRuntimeMetadata(),
		ThreadSource:    stringPtrIfNotEmpty(lineage.ThreadSource),
		SubagentSource:  stringPtrIfNotEmpty(lineage.SubagentKind),
		ParentThreadID:  stringPtrIfNotEmpty(lineage.ParentThreadID),
		ToolName:        toolName,
		ToolEventType:   r.toolEventTypeForCall(threadID, turnID, callID),
		StartedAtMS:     observation.StartedAtMS,
		CompletedAtMS:   observation.CompletedAtMS,
		DurationMS:      durationMS,
		// Rust passes the same observed window as the execution duration for a
		// code-mode call.
		ExecutionDurationMS:  durationMS,
		FinalApprovalOutcome: telemetry.FinalApprovalOutcomeUnknown,
		TerminalStatus:       terminalStatus,
		FailureKind:          failureKind,
	}
	r.enrichToolEventBase(&base, threadID, turnID)
	params := telemetry.CodexDynamicToolCallEventParams{
		CodexToolItemEventBase: base,
		DynamicToolName:        toolName,
		Success:                boolPtrAppserver(observation.Status == tool.CodeModeCallStatusCompleted),
	}
	r.emitToolEvent(threadID, turnID, &params.CodexToolItemEventBase, func() {
		sink.TrackCodexDynamicToolCallEvent(context.Background(), telemetry.NewCodexDynamicToolCallEvent(params))
	})
}

func codeModeCallAnalyticsOutcome(status tool.CodeModeCallStatus) (string, *string) {
	switch status {
	case tool.CodeModeCallStatusFailed:
		return telemetry.ToolItemTerminalStatusFailed, stringPtrIfNotEmpty(telemetry.ToolItemFailureKindToolError)
	case tool.CodeModeCallStatusInterrupted:
		return telemetry.ToolItemTerminalStatusInterrupted, nil
	default:
		return telemetry.ToolItemTerminalStatusCompleted, nil
	}
}

func boolPtrAppserver(value bool) *bool {
	return &value
}
