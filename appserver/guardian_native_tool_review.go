package appserver

import (
	"context"
	"encoding/json"
	"strings"

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
		// Rust #58075 discards a serving assessment when a hook rewrites the
		// tool input: it no longer describes the call being reviewed.
		if invocation != nil && len(invocation.NativeCall) > 0 {
			if recorded := nativeCallArguments(invocation.NativeCall); recorded != "" && recorded != invocation.Payload.Arguments {
				r.forgetGuardianServingAssessment(threadID, turnID, invocation.CallID)
			}
		}
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

// nativeCallArguments returns the retained call's original argument string.
func nativeCallArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var call struct {
		Arguments string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return ""
	}
	return call.Arguments
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
	raw, err := nativeToolCallEvidenceItem(invocation.NativeCall, invocation)
	if err != nil {
		return state.Action{}, err
	}
	return state.Action{Type: state.ActionNativeToolCall, NativeCall: raw}, nil
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
