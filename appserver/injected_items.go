package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/session"
)

// Injected item retention policies (Rust #61344 `ItemRetention`). Raw response
// items keep the thread's configured rules; a wrapped item must state one.
const (
	injectedRetentionDefault = "default"
	injectedRetentionAlways  = "always"
	// injectedRetentionAlwaysInThread keeps the item in this thread's later
	// context windows and excludes it from spawned agents (Rust #62654).
	injectedRetentionAlwaysInThread = "alwaysInThread"
	// injectedRetentionAlwaysInChildren passes the item to spawned agents while
	// it is in the parent's context; each thread then applies its ordinary
	// context-window rules.
	injectedRetentionAlwaysInChildren = "alwaysInChildren"
)

// injectedResponseItem is one history injection input: a raw Responses item
// plus the optional explicit retention policy carried by the wrapped shape.
type injectedResponseItem struct {
	Payload   map[string]any
	Retention string
}

// parseInjectedResponseItems accepts both the legacy raw response item and the
// wrapped `{item, retention}` shape. A wrapped item must name its policy, so an
// explicit "default" can opt out of the thread's client-message retention.
func parseInjectedResponseItems(rawItems []json.RawMessage) ([]injectedResponseItem, error) {
	items := make([]injectedResponseItem, 0, len(rawItems))
	for index, raw := range rawItems {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("items[%d] is not a valid response item: %v", index, err))
		}
		if _, rawItem := value["type"]; rawItem {
			items = append(items, injectedResponseItem{Payload: value})
			continue
		}
		inner, ok := value["item"].(map[string]any)
		if !ok {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("items[%d] is not a valid response item: missing field `item`", index))
		}
		retention, present := value["retention"].(string)
		if !present {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("items[%d] is not a valid response item: missing field `retention`", index))
		}
		switch retention {
		case injectedRetentionDefault, injectedRetentionAlways,
			injectedRetentionAlwaysInThread, injectedRetentionAlwaysInChildren:
		default:
			return nil, jsonRPCInvalidRequest(fmt.Sprintf(
				"items[%d] is not a valid response item: unknown variant `%s`, expected `default`, `always`, `alwaysInThread` or `alwaysInChildren`", index, retention,
			))
		}
		if _, ok := inner["type"]; !ok {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("items[%d] is not a valid response item: item is missing `type`", index))
		}
		items = append(items, injectedResponseItem{Payload: inner, Retention: retention})
	}
	return items, nil
}

// injectedAgentMailBatch reports whether an injection batch is plaintext agent
// mail that the native mailbox can carry (Rust #62292/#62537): every item is an
// agent_message with exactly one input_text block, valid author and recipient
// agent paths, and no explicit retention policy. Mixed batches and multipart or
// encrypted messages keep the raw history-injection path.
func injectedAgentMailBatch(items []injectedResponseItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Retention != "" {
			return false
		}
		if strings.TrimSpace(stringFromMap(item.Payload, "type")) != "agent_message" {
			return false
		}
		if err := agent.ValidateAgentPath(stringFromMap(item.Payload, "author")); err != nil {
			return false
		}
		if err := agent.ValidateAgentPath(stringFromMap(item.Payload, "recipient")); err != nil {
			return false
		}
		blocks, ok := item.Payload["content"].([]any)
		if !ok || len(blocks) != 1 {
			return false
		}
		block, ok := blocks[0].(map[string]any)
		if !ok || strings.TrimSpace(stringFromMap(block, "type")) != "input_text" {
			return false
		}
	}
	return true
}

// validateInjectedResponseItems runs the shared uploaded-file admission checks
// on each unwrapped item.
func validateInjectedResponseItems(items []injectedResponseItem) error {
	for _, item := range items {
		raw, err := json.Marshal(item.Payload)
		if err != nil {
			return jsonRPCInvalidRequest("items are not valid response items")
		}
		if err := validateResponseItemImageURLs([]json.RawMessage{raw}); err != nil {
			return err
		}
		if err := validateResponseItemAudioFiles([]json.RawMessage{raw}); err != nil {
			return err
		}
	}
	return nil
}

// validateRetainedToolAdjacency mirrors Rust `record_injected_items`: a retained
// tool call must be immediately followed by its matching output, and both must
// carry the same retention policy. A retained output without that pair is
// rejected too.
func validateRetainedToolAdjacency(items []injectedResponseItem) error {
	for index := 0; index < len(items); index++ {
		item := items[index]
		if item.Retention == "" || item.Retention == injectedRetentionDefault {
			continue
		}
		needsCall := injectedOutputCallID(item.Payload) != "" && !isServerToolSearchOutput(item.Payload)
		if injectedInputCallID(item.Payload) == "" && !needsCall {
			continue
		}
		mismatch := fmt.Errorf("items[%d]: retained tool calls and outputs must be adjacent in the same request, call first, with the same retention policy", index)
		if index+1 >= len(items) {
			return jsonRPCInvalidRequest(mismatch.Error())
		}
		output := items[index+1]
		if output.Retention != item.Retention || !injectedToolOutputMatches(item.Payload, output.Payload) {
			return jsonRPCInvalidRequest(mismatch.Error())
		}
		index++
	}
	return nil
}

// injectedSessionItems converts parsed injections into persisted session items
// and applies the explicit retention policy.
func injectedSessionItems(items []injectedResponseItem, now time.Time, retainClientDeveloperMessages bool) ([]session.Item, error) {
	out := make([]session.Item, 0, len(items))
	for index, injected := range items {
		raw, err := json.Marshal(injected.Payload)
		if err != nil {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("items[%d] is not a valid response item", index))
		}
		item, err := sessionItemFromRaw(raw, now, index)
		if err != nil {
			return nil, err
		}
		applyInjectedToolShape(&item, injected.Payload)
		switch injected.Retention {
		case injectedRetentionAlways:
			// Rust `From<InjectedResponseItem>`: Always records both scopes.
			inheritedByChildren := true
			setItemRetention(&item, true, &inheritedByChildren)
		case injectedRetentionAlwaysInThread:
			keptInThreadOnly := false
			setItemRetention(&item, true, &keptInThreadOnly)
		case injectedRetentionAlwaysInChildren:
			inheritedByChildren := true
			setItemRetention(&item, false, &inheritedByChildren)
		case injectedRetentionDefault:
			setItemRetention(&item, false, nil)
		default:
			annotateInjectedItemRetention(&item, retainClientDeveloperMessages)
		}
		out = append(out, item)
	}
	return out, nil
}

// injectedSessionItemsForRequest parses, validates, and persists one injection
// request. It mirrors Rust's `TurnRequestProcessor::inject_items` followed by
// `CodexThread::record_injected_items`: shaped items are validated per unwrapped
// response item, retained tool exchanges must be adjacent, and the explicit
// policy is stored separately from legacy client-authored provenance.
func injectedSessionItemsForRequest(params *ThreadInjectItemsParams, now time.Time, retainClientDeveloperMessages bool) ([]injectedResponseItem, []session.Item, error) {
	if params == nil {
		return nil, nil, jsonRPCInvalidRequest("thread/injectItems requires a request")
	}
	parsed, err := parseInjectedResponseItems(params.Items)
	if err != nil {
		return nil, nil, err
	}
	if err := validateInjectedResponseItems(parsed); err != nil {
		return nil, nil, err
	}
	if err := validateRetainedToolAdjacency(parsed); err != nil {
		return nil, nil, err
	}
	items, err := injectedSessionItems(parsed, now, retainClientDeveloperMessages)
	if err != nil {
		return nil, nil, err
	}
	return parsed, items, nil
}

// annotateInjectedItemRetention mirrors Rust `annotate_client_response_item`:
// a raw developer message supplied while client developer-message retention is
// enabled is retained unless the request already stated a policy.
func annotateInjectedItemRetention(item *session.Item, retainClientDeveloperMessages bool) {
	if item == nil || !retainClientDeveloperMessages {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return
	}
	if _, explicit := sessionItemRetentionOverride(item); explicit {
		return
	}
	setItemRetainAcrossWindows(item, true)
}

// sessionItemRetentionOverride reads the explicit per-item policy from the
// persisted harness metadata.
func sessionItemRetentionOverride(item *session.Item) (bool, bool) {
	var metadata map[string]any
	if raw := harnessMetadataRawFromItem(item); len(raw) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	retain, ok := metadata[compact.RetainAcrossWindowsMetadataKey].(bool)
	return retain, ok
}

// sessionItemIsAlwaysRetained reports whether the client explicitly asked the
// persisted item to survive context-window changes.
func sessionItemIsAlwaysRetained(item *session.Item) bool {
	retain, ok := sessionItemRetentionOverride(item)
	return ok && retain
}

// sessionItemChildRetention resolves whether a spawned agent inherits the
// persisted item, and whether the item states that decision (Rust #62654
// `ResponseItemEnvelope::child_retention`). An explicit retain_in_children wins;
// otherwise an older rollout that pinned the item across windows inherits it too.
func sessionItemChildRetention(item *session.Item) (bool, bool) {
	var metadata map[string]any
	if raw := harnessMetadataRawFromItem(item); len(raw) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	if inherit, ok := metadata[compact.RetainInChildrenMetadataKey].(bool); ok {
		return inherit, true
	}
	if retain, ok := metadata[compact.RetainAcrossWindowsMetadataKey].(bool); ok && retain {
		return true, true
	}
	return false, false
}

// applyInjectedToolShape fills the persisted fields history replay needs for
// injected tool calls and outputs; text messages already round-trip through
// sessionItemFromRaw.
func applyInjectedToolShape(item *session.Item, payload map[string]any) {
	if item == nil || payload == nil {
		return
	}
	itemType := strings.TrimSpace(stringFromMap(payload, "type"))
	if itemType == "" {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	if callID := firstNonEmpty(injectedInputCallID(payload), injectedOutputCallID(payload)); callID != "" {
		item.CallID = callID
	}
	if name := strings.TrimSpace(stringFromMap(payload, "name")); name != "" {
		item.Name = name
	}
	if namespace := strings.TrimSpace(stringFromMap(payload, "namespace")); namespace != "" {
		item.Namespace = namespace
	}
	switch itemType {
	case "function_call":
		if arguments, ok := payload["arguments"].(string); ok {
			item.Data["arguments"] = arguments
			if strings.TrimSpace(item.Text) == "" {
				item.Text = arguments
			}
		}
	case "custom_tool_call":
		if input, ok := payload["input"].(string); ok {
			item.Data["input"] = input
			if strings.TrimSpace(item.Text) == "" {
				item.Text = input
			}
		}
	case "tool_search_call":
		if execution := strings.TrimSpace(stringFromMap(payload, "execution")); execution != "" {
			item.Data["execution"] = execution
		}
		if arguments, ok := payload["arguments"]; ok {
			item.Data["arguments"] = arguments
		}
	case "function_call_output", "custom_tool_call_output":
		if output, ok := payload["output"]; ok {
			item.Data["output"] = output
		}
	case "tool_search_output":
		if output, ok := payload["output"]; ok {
			item.Data["output"] = output
		}
		if execution := strings.TrimSpace(stringFromMap(payload, "execution")); execution != "" {
			item.Data["execution"] = execution
		} else {
			item.Data["execution"] = "client"
		}
		if status := strings.TrimSpace(stringFromMap(payload, "status")); status != "" {
			item.Status = status
		}
		if tools, ok := payload["tools"]; ok {
			item.Data["tools"] = tools
		}
	}
}

// setItemRetainAcrossWindows records only the context-window decision.
func setItemRetainAcrossWindows(item *session.Item, retain bool) {
	setItemRetention(item, retain, nil)
}

// setItemRetention records the explicit per-item policy in harness metadata, next
// to (not inside) legacy client-authored provenance: whether the item survives
// this thread's later context windows and whether spawned agents inherit it. A
// nil inheritInChildren omits the child override, which is what `default` and
// `always` persist so a rollout written before #62654 keeps its meaning.
func setItemRetention(item *session.Item, retainAcrossWindows bool, inheritInChildren *bool) {
	if item == nil {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	var metadata map[string]any
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &metadata)
	case string:
		if strings.TrimSpace(value) != "" {
			_ = json.Unmarshal([]byte(value), &metadata)
		}
	case map[string]any:
		metadata = cloneAnyMapForRouter(value)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata[compact.RetainAcrossWindowsMetadataKey] = retainAcrossWindows
	if inheritInChildren == nil {
		delete(metadata, compact.RetainInChildrenMetadataKey)
	} else {
		metadata[compact.RetainInChildrenMetadataKey] = *inheritInChildren
	}
	if encoded, err := json.Marshal(metadata); err == nil {
		item.Data["harness_metadata"] = json.RawMessage(encoded)
	}
}

// itemRetainAcrossWindows reports whether a persisted item was explicitly
// retained.
func itemRetainAcrossWindows(item session.Item) bool {
	if item.Data == nil {
		return false
	}
	var metadata map[string]any
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &metadata)
	case string:
		if strings.TrimSpace(value) != "" {
			_ = json.Unmarshal([]byte(value), &metadata)
		}
	case map[string]any:
		metadata = value
	}
	retain, _ := metadata[compact.RetainAcrossWindowsMetadataKey].(bool)
	return retain
}

func injectedInputCallID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	switch strings.TrimSpace(stringFromMap(payload, "type")) {
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call":
		return strings.TrimSpace(stringFromMap(payload, "call_id"))
	}
	return ""
}

func injectedOutputCallID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	switch strings.TrimSpace(stringFromMap(payload, "type")) {
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		return strings.TrimSpace(stringFromMap(payload, "call_id"))
	}
	return ""
}

func isServerToolSearchOutput(payload map[string]any) bool {
	return strings.TrimSpace(stringFromMap(payload, "type")) == "tool_search_output" &&
		strings.TrimSpace(stringFromMap(payload, "execution")) == "server"
}

// injectedToolOutputMatches mirrors Rust `is_matching_tool_output`: same call
// id and a call/output shape pair from the same tool family.
func injectedToolOutputMatches(call, output map[string]any) bool {
	callID := injectedInputCallID(call)
	if callID == "" || injectedOutputCallID(output) != callID {
		return false
	}
	switch strings.TrimSpace(stringFromMap(call, "type")) {
	case "function_call", "local_shell_call":
		return strings.TrimSpace(stringFromMap(output, "type")) == "function_call_output"
	case "custom_tool_call":
		return strings.TrimSpace(stringFromMap(output, "type")) == "custom_tool_call_output"
	case "tool_search_call":
		return strings.TrimSpace(stringFromMap(output, "type")) == "tool_search_output"
	}
	return false
}
