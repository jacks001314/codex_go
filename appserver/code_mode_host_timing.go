package appserver

import (
	"context"
	"strconv"
	"strings"

	"codex_go/tool"
)

// emitCodeModeHostTiming mirrors Rust's codex.code_mode.host_timing record
// (#41452): the host operation duration is correlated with the conversation,
// turn, call, cell and tool.
func (r *RuntimeRouter) emitCodeModeHostTiming(threadID, turnID string, observation tool.CodeModeCallObservation) {
	if r == nil || observation.CodeModeHostDurationNS == 0 {
		return
	}
	fields := codeModeHostTimingEventFields(threadID, turnID, observation)
	if len(fields) == 0 {
		return
	}
	if session := r.sessionTelemetryForThread(threadID); session != nil {
		session.LogAndTraceEvent(context.Background(), "codex.code_mode.host_timing", fields, nil, nil)
	}
}

func codeModeHostTimingEventFields(threadID, turnID string, observation tool.CodeModeCallObservation) map[string]string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	callID := strings.TrimSpace(observation.CallID)
	if threadID == "" || turnID == "" || callID == "" || observation.CodeModeHostDurationNS == 0 {
		return nil
	}
	fields := map[string]string{
		"conversation_id":            threadID,
		"turn_id":                    turnID,
		"call_id":                    callID,
		"tool_name":                  firstNonEmpty(strings.TrimSpace(observation.ToolName), tool.CodeModeExecToolName),
		"code_mode_host_duration_ns": strconv.FormatUint(observation.CodeModeHostDurationNS, 10),
	}
	if cellID := strings.TrimSpace(observation.CellID); cellID != "" {
		fields["cell_id"] = cellID
	}
	return fields
}
