package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// guardianNativeMultiAgentFunctionNames are the MAv2 calls Rust keeps intact in
// synchronous Guardian history so the backend can decrypt them (#57448).
var guardianNativeMultiAgentFunctionNames = map[string]struct{}{
	"spawn_agent":     {},
	"send_message":    {},
	"followup_task":   {},
	"interrupt_agent": {},
}

// guardianNativeMultiAgentCall returns the complete stored function call when it
// is one of the MAv2 calls the configured native namespace preserves. Rust keeps
// the serialized response item so ciphertext arguments reach the reviewer
// unchanged; `nativeNamespace` is empty when the feature is off.
func guardianNativeMultiAgentCall(item *session.Item, nativeNamespace string) (json.RawMessage, bool) {
	nativeNamespace = strings.TrimSpace(nativeNamespace)
	if item == nil || nativeNamespace == "" {
		return nil, false
	}
	if strings.ToLower(strings.TrimSpace(item.Type)) != "function_call" {
		return nil, false
	}
	name := guardianNativeFunctionCallName(item)
	if _, ok := guardianNativeMultiAgentFunctionNames[name]; !ok {
		return nil, false
	}
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == "" {
		namespace = strings.TrimSpace(stringFromAny(item.Data["namespace"]))
	}
	if namespace == "" {
		namespace = "functions"
	}
	if namespace != nativeNamespace {
		return nil, false
	}
	return guardianNativeFunctionCallItem(item, name, namespace)
}

// guardianNativeFunctionCallName returns the unqualified tool name. Rust
// compares the raw `FunctionCall::name`, while the text transcript renders the
// namespace-qualified label.
func guardianNativeFunctionCallName(item *session.Item) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(
		item.Name,
		stringFromAny(item.Data["name"]),
		stringFromAny(item.Data["tool"]),
	))
}

// guardianNativeFunctionCallItem serializes the stored response item as Rust's
// `ResponseItem::FunctionCall`, preferring the recorded raw item so encrypted
// arguments and passthrough metadata survive.
func guardianNativeFunctionCallItem(item *session.Item, name string, namespace string) (json.RawMessage, bool) {
	if item == nil || name == "" {
		return nil, false
	}
	callID := strings.TrimSpace(item.CallID)
	if callID == "" {
		callID = strings.TrimSpace(stringFromAny(item.Data["call_id"]))
	}
	if callID == "" {
		return nil, false
	}
	candidates := make([]any, 0, 3)
	if item.Metadata != nil {
		if raw, ok := item.Metadata["rawResponseItem"]; ok {
			candidates = append(candidates, raw)
		}
	}
	if len(item.Raw) > 0 {
		candidates = append(candidates, item.Raw)
	}
	if len(item.Data) > 0 {
		candidates = append(candidates, item.Data)
	}
	for _, candidate := range candidates {
		if payload, ok := guardianFunctionCallPayload(candidate, callID); ok {
			return payload, true
		}
	}
	arguments := strings.TrimSpace(item.Text)
	if arguments == "" && len(item.Content) > 0 {
		for _, part := range item.Content {
			if strings.TrimSpace(part.Text) != "" {
				arguments = strings.TrimSpace(part.Text)
				break
			}
		}
	}
	payload := map[string]any{
		"type":      "function_call",
		"name":      name,
		"arguments": arguments,
		"call_id":   callID,
	}
	if namespace != "" {
		payload["namespace"] = namespace
	}
	for _, key := range []string{"id", "status", "encrypted_content", "encrypted_function_args", "internal_chat_message_metadata_passthrough"} {
		if value, ok := item.Data[key]; ok && value != nil {
			payload[key] = value
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return data, true
}

// guardianFunctionCallPayload re-serializes a stored payload when it is a
// function call for the same call id. The round trip clones the map so the
// reviewer never shares storage with the live session item.
func guardianFunctionCallPayload(value any, callID string) (json.RawMessage, bool) {
	var raw []byte
	switch typed := value.(type) {
	case map[string]any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, false
		}
		raw = encoded
	case json.RawMessage:
		raw = typed
	case []byte:
		raw = typed
	default:
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return nil, false
	}
	if strings.TrimSpace(stringFromAny(payload["type"])) != "function_call" {
		return nil, false
	}
	if recorded := strings.TrimSpace(stringFromAny(payload["call_id"])); recorded != "" && recorded != callID {
		return nil, false
	}
	payload["call_id"] = callID
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return data, true
}