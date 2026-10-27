package appserver

import (
	"context"
	"encoding/json"
	"strings"

	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
)

// Mirrors Rust #57433 (`core/src/tools/native_tool_review.rs`): an opted-in
// function call is reviewed synchronously before its handler runs, and the
// proposed call reaches the reviewer as native evidence rather than action JSON.

// nativeToolReviewSource identifies the invocation sources whose arguments are
// plaintext, so a synthesized call carries an empty encrypted-argument list
// (Rust's `ToolCallSource::DirectPlaintextMessage | CodeMode`).
func nativeToolReviewPlaintextSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "", "direct", "direct_plaintext_message":
		return true
	case tool.InvocationSourceCodeMode:
		return true
	default:
		return false
	}
}

// nativeToolGuardianReview returns the router's pre-dispatch review for one
// turn. It never falls back to user approval: an unavailable or failed review
// rejects the call before the handler executes.
func (r *RuntimeRouter) nativeToolGuardianReview(threadID, turnID string) tool.GuardianReviewFunc {
	if r == nil {
		return nil
	}
	return func(ctx context.Context, invocation *tool.Invocation) error {
		action, err := r.nativeToolGuardianAction(threadID, invocation)
		if err != nil {
			return err
		}
		outcome := r.reviewApprovalWithGuardian(ctx, threadID, turnID, invocation.CallID, action)
		switch {
		case outcome.Approved:
			return nil
		case outcome.Abort:
			reason := strings.TrimSpace(outcome.DenyReason)
			if reason == "" {
				reason = "automatic approval review was cancelled"
			}
			return tool.RespondToModel(reason)
		default:
			return tool.RespondToModel(outcome.DenyReason)
		}
	}
}

// nativeToolGuardianAction builds Rust's `GuardianApprovalRequest::NativeToolCall`
// for one invocation: the persisted function call when history holds it, so
// encrypted replay content survives, otherwise the invocation's own call.
// Hook-rewritten arguments replace the recorded ones and invalidate the
// original encrypted replay content.
func (r *RuntimeRouter) nativeToolGuardianAction(threadID string, invocation *tool.Invocation) (state.Action, error) {
	if invocation == nil {
		return state.Action{}, tool.RespondToModel("Native review requires a function call")
	}
	if invocation.Payload.Kind != tool.PayloadFunction {
		return state.Action{}, tool.RespondToModel("Native review requires a function call")
	}
	raw, err := nativeToolCallEvidenceItem(r.nativeToolHistoryCall(threadID, invocation.CallID), invocation)
	if err != nil {
		return state.Action{}, err
	}
	return state.Action{Type: state.ActionNativeToolCall, NativeCall: raw}, nil
}

// nativeToolHistoryCall returns the thread's most recent persisted call item for
// the invocation, or nil when history does not hold it.
func (r *RuntimeRouter) nativeToolHistoryCall(threadID, callID string) json.RawMessage {
	callID = strings.TrimSpace(callID)
	if r == nil || callID == "" {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil || record == nil {
		return nil
	}
	for index := len(record.Items) - 1; index >= 0; index-- {
		item := &record.Items[index]
		if item == nil || len(item.Raw) == 0 || !strings.EqualFold(strings.TrimSpace(item.Type), "function_call") {
			continue
		}
		if itemCallID, _, ok := state.GuardianNativeFunctionCall(item.Raw); ok && itemCallID == callID {
			return append(json.RawMessage(nil), item.Raw...)
		}
		if strings.TrimSpace(item.CallID) == callID {
			return append(json.RawMessage(nil), item.Raw...)
		}
	}
	return nil
}

// nativeToolCallEvidenceItem reconciles the recorded call with the invocation's
// final arguments. A rewrite replaces the arguments and drops the original
// encrypted replay content, because that content no longer describes the call.
func nativeToolCallEvidenceItem(recorded json.RawMessage, invocation *tool.Invocation) (json.RawMessage, error) {
	item := map[string]any{}
	if len(recorded) > 0 {
		_ = json.Unmarshal(recorded, &item)
	}
	callID, _, ok := state.GuardianNativeFunctionCall(recorded)
	if !ok || callID != strings.TrimSpace(invocation.CallID) {
		item = map[string]any{
			"type":      "function_call",
			"name":      invocation.ToolName.Name,
			"arguments": invocation.Payload.Arguments,
			"call_id":   invocation.CallID,
		}
		if namespace := strings.TrimSpace(invocation.ToolName.Namespace); namespace != "" {
			item["namespace"] = namespace
		}
		if nativeToolReviewPlaintextSource(invocation.Source) {
			item["encrypted_function_args"] = []string{}
		}
	}
	arguments, _ := item["arguments"].(string)
	if arguments != invocation.Payload.Arguments {
		item["arguments"] = invocation.Payload.Arguments
		delete(item, "encrypted_content")
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, tool.RespondToModel("Native review requires a function call")
	}
	if _, _, ok := state.GuardianNativeFunctionCall(raw); !ok {
		return nil, tool.RespondToModel("Native review requires a function call")
	}
	return raw, nil
}
