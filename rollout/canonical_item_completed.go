package rollout

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"codex_go/utils"
)

// This file replays persisted item_completed records through 0.166's
// ItemCompletedEvent/TurnItem types. The migration adapter decodes every
// record into the modern types before rewriting it, so a legacy item that no
// longer decodes drops the whole line while a decodable item comes back in
// declaration order without unknown fields.

// canonicalSerdeString encodes a string the way serde_json does: Go's encoder
// runs with HTML escaping disabled so text keeps its literal <, >, and &.
func canonicalSerdeString(text string) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(text); err != nil {
		return []byte(`""`)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
}

// writeItemCompletedRecord replays one item_completed payload. The event keeps
// the rollout thread id (Rust rewrites a mismatched payload id), defaults a
// missing completed_at_ms to 0, and drops the line when any required field or
// nested item fails to decode.
func (w *canonicalReplayWriter) writeItemCompletedRecord(entry canonicalReplayLine) error {
	fields, err := canonicalRawFields(entry.payload)
	if err != nil {
		return err
	}
	turnID, ok := canonicalStrictString(fields["turn_id"])
	if !ok {
		return nil
	}
	threadID, ok := canonicalStrictString(fields["thread_id"])
	if !ok || !canonicalUUIDDecodes(threadID) {
		return nil
	}
	itemRaw, present := fields["item"]
	if !present || canonicalRawIsNull(itemRaw) {
		return nil
	}
	item, ok, err := canonicalTurnItemPayload(itemRaw)
	if err != nil || !ok {
		return err
	}
	completedAt := int64(0)
	if raw, present := fields["completed_at_ms"]; present && !canonicalRawIsNull(raw) {
		value, ok := canonicalStrictInt(raw)
		if !ok {
			return nil
		}
		completedAt = value
	}
	startedAt, hasStarted := int64(0), false
	if raw, present := fields["started_at_ms"]; present && !canonicalRawIsNull(raw) {
		value, ok := canonicalStrictInt(raw)
		if !ok {
			return nil
		}
		startedAt, hasStarted = value, true
	}
	payload := newCanonicalJSONObject()
	_ = payload.field("type", "item_completed")
	_ = payload.rawField("thread_id", canonicalSerdeString(w.threadID))
	_ = payload.rawField("turn_id", canonicalSerdeString(turnID))
	_ = payload.rawField("item", item)
	if hasStarted {
		_ = payload.field("started_at_ms", startedAt)
	}
	_ = payload.field("completed_at_ms", completedAt)
	return w.writeRecord(entry.timestamp, "event_msg", payload.finish())
}

// canonicalTurnItemPayload decodes a TurnItem and returns its modern encoding.
// The boolean is false when the item does not decode, which drops the line.
func canonicalTurnItemPayload(source json.RawMessage) ([]byte, bool, error) {
	switch canonicalRawString(source, "type") {
	case "UserMessage":
		return canonicalUserMessageTurnItem(source)
	case "FunctionCallOutput":
		return canonicalFunctionCallOutputTurnItem(source)
	case "HookPrompt":
		return canonicalHookPromptTurnItem(source)
	case "AgentMessage":
		return canonicalAgentMessageTurnItem(source)
	case "Plan":
		return canonicalPlanTurnItem(source)
	case "Reasoning":
		return canonicalReasoningTurnItem(source)
	case "CommandExecution":
		return canonicalCommandExecutionTurnItem(source)
	case "DynamicToolCall":
		return canonicalDynamicToolCallTurnItem(source)
	case "CollabAgentToolCall":
		return canonicalCollabAgentToolCallTurnItem(source)
	case "SubAgentActivity":
		return canonicalSubAgentActivityTurnItem(source)
	case "WebSearch":
		return canonicalWebSearchTurnItem(source)
	case "ImageView":
		return canonicalImageViewTurnItem(source)
	case "Extension":
		return canonicalExtensionTurnItem(source)
	case "ImageGeneration":
		return canonicalImageGenerationTurnItem(source)
	case "EnteredReviewMode":
		return canonicalEnteredReviewModeTurnItem(source)
	case "ExitedReviewMode":
		return canonicalExitedReviewModeTurnItem(source)
	case "FileChange":
		return canonicalFileChangeItemPayload(source)
	case "McpToolCall":
		return canonicalMcpToolCallItemPayload(source)
	case "ContextCompaction":
		return canonicalContextCompactionTurnItem(source)
	}
	return nil, false, nil
}

// canonicalItemObject writes TurnItem fields in declaration order while
// decoding them with serde's rules. Any decode failure clears ok, which makes
// the caller drop the record.
type canonicalItemObject struct {
	fields map[string]json.RawMessage
	out    *canonicalJSONObject
	ok     bool
}

func newCanonicalItemObject(source json.RawMessage, itemType string) (*canonicalItemObject, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, err
	}
	out := newCanonicalJSONObject()
	if err := out.field("type", itemType); err != nil {
		return nil, err
	}
	return &canonicalItemObject{fields: fields, out: out, ok: true}, nil
}

func (o *canonicalItemObject) requireStringField(key string) string {
	value, ok := canonicalStrictString(o.fields[key])
	if !ok {
		o.ok = false
		return ""
	}
	_ = o.out.rawField(key, canonicalSerdeString(value))
	return value
}

func (o *canonicalItemObject) optionalStringField(key string) (string, bool) {
	value, present, ok := canonicalItemStringField(o.fields, key)
	if !ok {
		o.ok = false
		return "", false
	}
	if present {
		_ = o.out.rawField(key, canonicalSerdeString(value))
	}
	return value, present
}

func (o *canonicalItemObject) nullableStringField(key string) {
	value, present, ok := canonicalItemStringField(o.fields, key)
	if !ok {
		o.ok = false
		return
	}
	if present {
		_ = o.out.rawField(key, canonicalSerdeString(value))
		return
	}
	_ = o.out.rawField(key, []byte("null"))
}

func (o *canonicalItemObject) requireEnumField(key string, allowed ...string) string {
	value, ok := canonicalStrictString(o.fields[key])
	if !ok || !canonicalItemEnumAllowed(value, allowed) {
		o.ok = false
		return ""
	}
	_ = o.out.rawField(key, canonicalSerdeString(value))
	return value
}

func (o *canonicalItemObject) optionalEnumField(key string, allowed ...string) (string, bool) {
	value, present, ok := canonicalItemStringField(o.fields, key)
	if !ok || !present {
		if !ok {
			o.ok = false
		}
		return "", false
	}
	if !canonicalItemEnumAllowed(value, allowed) {
		o.ok = false
		return "", false
	}
	_ = o.out.rawField(key, canonicalSerdeString(value))
	return value, true
}

func (o *canonicalItemObject) requireRawField(key string) {
	raw, present := o.fields[key]
	if !present {
		o.ok = false
		return
	}
	_ = o.out.rawField(key, raw)
}

func (o *canonicalItemObject) optionalRawField(key string) (json.RawMessage, bool) {
	raw, present := o.fields[key]
	if !present || canonicalRawIsNull(raw) {
		return nil, false
	}
	_ = o.out.rawField(key, raw)
	return raw, true
}

func (o *canonicalItemObject) optionalIntField(key string) (int64, bool) {
	value, present, ok := canonicalItemIntField(o.fields, key)
	if !ok || !present {
		if !ok {
			o.ok = false
		}
		return 0, false
	}
	_ = o.out.field(key, value)
	return value, true
}

func (o *canonicalItemObject) optionalBoolField(key string) (bool, bool) {
	raw, present := o.fields[key]
	if !present || canonicalRawIsNull(raw) {
		return false, false
	}
	flag, ok := canonicalRawJSONBool(raw)
	if !ok {
		o.ok = false
		return false, false
	}
	_ = o.out.rawField(key, []byte(strconv.FormatBool(flag)))
	return flag, true
}

func (o *canonicalItemObject) pathURIField(key string) string {
	value, ok := canonicalStrictString(o.fields[key])
	if !ok {
		o.ok = false
		return ""
	}
	uri, err := utils.Parse(value)
	if err != nil || uri == nil {
		o.ok = false
		return ""
	}
	_ = o.out.rawField(key, canonicalSerdeString(uri.String()))
	return uri.String()
}

func (o *canonicalItemObject) absolutePathField(key string) {
	value, ok := canonicalAbsolutePath(o.fields[key])
	if !ok {
		o.ok = false
		return
	}
	_ = o.out.rawField(key, canonicalSerdeString(value))
}

func (o *canonicalItemObject) stringArrayField(key string, fallbackEmpty bool) {
	raw, present := o.fields[key]
	if !present || canonicalRawIsNull(raw) {
		if !fallbackEmpty {
			o.ok = false
			return
		}
		_ = o.out.rawField(key, []byte("[]"))
		return
	}
	converted, ok := canonicalStringList(raw)
	if !ok {
		o.ok = false
		return
	}
	_ = o.out.rawField(key, converted)
}

func (o *canonicalItemObject) finish() ([]byte, bool, error) {
	if !o.ok {
		return nil, false, nil
	}
	return o.out.finish(), true, nil
}

func canonicalItemEnumAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// canonicalItemStringField decodes an Option<String> field: the third result
// reports whether the value decodes at all.
func canonicalItemStringField(fields map[string]json.RawMessage, key string) (string, bool, bool) {
	raw, present := fields[key]
	if !present || canonicalRawIsNull(raw) {
		return "", false, true
	}
	value, ok := canonicalRawJSONStringValue(raw)
	return value, true, ok
}

func canonicalItemIntField(fields map[string]json.RawMessage, key string) (int64, bool, bool) {
	raw, present := fields[key]
	if !present || canonicalRawIsNull(raw) {
		return 0, false, true
	}
	value, ok := canonicalStrictInt(raw)
	return value, true, ok
}

// canonicalItemList decodes a required Vec<T> field and re-encodes every entry.
func canonicalItemList(raw json.RawMessage, convert func(json.RawMessage) ([]byte, bool)) ([]byte, bool) {
	if len(raw) == 0 || canonicalRawIsNull(raw) {
		return nil, false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	converted := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		value, ok := convert(entry)
		if !ok {
			return nil, false
		}
		converted = append(converted, value)
	}
	return canonicalContentArray(converted), true
}

func canonicalStringList(raw json.RawMessage) ([]byte, bool) {
	return canonicalItemList(raw, func(entry json.RawMessage) ([]byte, bool) {
		value, ok := canonicalRawJSONStringValue(entry)
		if !ok {
			return nil, false
		}
		return canonicalSerdeString(value), true
	})
}

// canonicalUserMessageTurnItem replays TurnItem::UserMessage.
func canonicalUserMessageTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "UserMessage")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	// UserMessageItem.client_id keeps its snake_case name; the camelCase
	// spelling is not a serde alias and the decode drops it.
	item.optionalStringField("client_id")
	content, ok := canonicalItemList(item.fields["content"], canonicalUserInputContent)
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("content", content)
	return item.finish()
}

// canonicalUserInputContent replays one UserInput variant.
func canonicalUserInputContent(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	switch kind {
	case "text":
		out := newCanonicalJSONObject()
		_ = out.field("type", "text")
		text, ok := canonicalStrictString(fields["text"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("text", canonicalSerdeString(text))
		elements := []byte("[]")
		if raw, present := fields["text_elements"]; present && !canonicalRawIsNull(raw) {
			converted, ok := canonicalItemList(raw, canonicalTextElement)
			if !ok {
				return nil, false
			}
			elements = converted
		}
		_ = out.rawField("text_elements", elements)
		return out.finish(), true
	case "image":
		out := newCanonicalJSONObject()
		_ = out.field("type", "image")
		// ImageReference is untagged: the inline form decodes first, then the
		// durable file form.
		if url, ok := canonicalStrictString(fields["image_url"]); ok {
			_ = out.rawField("image_url", canonicalSerdeString(url))
		} else if fileID, ok := canonicalStrictString(fields["file_id"]); ok {
			_ = out.rawField("file_id", canonicalSerdeString(fileID))
		} else {
			return nil, false
		}
		if !canonicalImageDetailField(out, fields) {
			return nil, false
		}
		return out.finish(), true
	case "local_image":
		out := newCanonicalJSONObject()
		_ = out.field("type", "local_image")
		path, ok := canonicalStrictString(fields["path"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("path", canonicalSerdeString(path))
		if !canonicalImageDetailField(out, fields) {
			return nil, false
		}
		return out.finish(), true
	case "audio":
		out := newCanonicalJSONObject()
		_ = out.field("type", "audio")
		url, ok := canonicalStrictString(fields["audio_url"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("audio_url", canonicalSerdeString(url))
		return out.finish(), true
	case "local_audio":
		out := newCanonicalJSONObject()
		_ = out.field("type", "local_audio")
		path, ok := canonicalStrictString(fields["path"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("path", canonicalSerdeString(path))
		return out.finish(), true
	case "skill":
		out := newCanonicalJSONObject()
		_ = out.field("type", "skill")
		name, ok := canonicalStrictString(fields["name"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("name", canonicalSerdeString(name))
		path, ok := canonicalStrictString(fields["path"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("path", canonicalSerdeString(path))
		return out.finish(), true
	case "mention":
		out := newCanonicalJSONObject()
		_ = out.field("type", "mention")
		name, ok := canonicalStrictString(fields["name"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("name", canonicalSerdeString(name))
		path, ok := canonicalStrictString(fields["path"])
		if !ok {
			return nil, false
		}
		_ = out.rawField("path", canonicalSerdeString(path))
		return out.finish(), true
	}
	return nil, false
}

// canonicalImageDetailField appends the optional ImageDetail of an image input.
func canonicalImageDetailField(out *canonicalJSONObject, fields map[string]json.RawMessage) bool {
	detail, present, ok := canonicalItemStringField(fields, "detail")
	if !ok {
		return false
	}
	if !present {
		return true
	}
	switch detail {
	case "auto", "low", "high", "original":
	default:
		return false
	}
	_ = out.rawField("detail", canonicalSerdeString(detail))
	return true
}

// canonicalTextElement replays TextElement, whose placeholder always serializes.
func canonicalTextElement(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	rangeFields, err := canonicalRawFields(fields["byte_range"])
	if err != nil {
		return nil, false
	}
	start, ok := canonicalStrictInt(rangeFields["start"])
	if !ok || start < 0 {
		return nil, false
	}
	end, ok := canonicalStrictInt(rangeFields["end"])
	if !ok || end < 0 {
		return nil, false
	}
	byteRange := newCanonicalJSONObject()
	_ = byteRange.field("start", start)
	_ = byteRange.field("end", end)
	out := newCanonicalJSONObject()
	_ = out.rawField("byte_range", byteRange.finish())
	placeholder, present, ok := canonicalItemStringField(fields, "placeholder")
	if !ok {
		return nil, false
	}
	if present {
		_ = out.rawField("placeholder", canonicalSerdeString(placeholder))
	} else {
		_ = out.rawField("placeholder", []byte("null"))
	}
	return out.finish(), true
}

// canonicalFunctionCallOutputTurnItem replays TurnItem::FunctionCallOutput.
func canonicalFunctionCallOutputTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "FunctionCallOutput")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireStringField("name")
	item.optionalStringField("namespace")
	output, ok := canonicalFunctionCallOutputBody(item.fields["output"])
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("output", output)
	return item.finish()
}

// canonicalFunctionCallOutputBody replays the untagged FunctionCallOutputBody,
// which decodes as plain text first and as content items otherwise.
func canonicalFunctionCallOutputBody(raw json.RawMessage) ([]byte, bool) {
	if len(raw) == 0 || canonicalRawIsNull(raw) {
		return nil, false
	}
	if text, ok := canonicalRawJSONStringValue(raw); ok {
		return canonicalSerdeString(text), true
	}
	return canonicalItemList(raw, canonicalFunctionCallOutputContent)
}

// canonicalFunctionCallOutputContent replays FunctionCallOutputContentItem.
func canonicalFunctionCallOutputContent(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	switch kind {
	case "input_text", "input_audio", "encrypted_content":
		key := map[string]string{
			"input_text":        "text",
			"input_audio":       "audio_url",
			"encrypted_content": "encrypted_content",
		}[kind]
		value, ok := canonicalStrictString(fields[key])
		if !ok {
			return nil, false
		}
		out := newCanonicalJSONObject()
		_ = out.field("type", kind)
		_ = out.rawField(key, canonicalSerdeString(value))
		return out.finish(), true
	case "input_image":
		out := newCanonicalJSONObject()
		_ = out.field("type", kind)
		if url, ok := canonicalStrictString(fields["image_url"]); ok {
			_ = out.rawField("image_url", canonicalSerdeString(url))
		} else if fileID, ok := canonicalStrictString(fields["file_id"]); ok {
			_ = out.rawField("file_id", canonicalSerdeString(fileID))
		} else {
			return nil, false
		}
		if !canonicalImageDetailField(out, fields) {
			return nil, false
		}
		return out.finish(), true
	}
	return nil, false
}

// canonicalHookPromptTurnItem replays TurnItem::HookPrompt.
func canonicalHookPromptTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "HookPrompt")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	fragments, ok := canonicalItemList(item.fields["fragments"], canonicalHookPromptFragment)
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("fragments", fragments)
	return item.finish()
}

// canonicalHookPromptFragment replays HookPromptFragment, whose metadata field
// is skipped on the wire.
func canonicalHookPromptFragment(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	text, ok := canonicalStrictString(fields["text"])
	if !ok {
		return nil, false
	}
	runID, ok := canonicalStrictString(fields["hookRunId"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.rawField("text", canonicalSerdeString(text))
	_ = out.rawField("hookRunId", canonicalSerdeString(runID))
	return out.finish(), true
}

// canonicalAgentMessageTurnItem replays TurnItem::AgentMessage.
func canonicalAgentMessageTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "AgentMessage")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	content, ok := canonicalItemList(item.fields["content"], canonicalAgentMessageContent)
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("content", content)
	item.optionalEnumField("phase", "commentary", "partial_answer", "final_answer")
	item.optionalRawField("memory_citation")
	item.optionalEnumField("delivery", "async")
	if raw, present := item.fields["questions"]; present && !canonicalRawIsNull(raw) {
		questions, ok := canonicalItemList(raw, canonicalAsyncUserInputQuestion)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("questions", questions)
	}
	return item.finish()
}

// canonicalAgentMessageContent replays AgentMessageContent, whose tag keeps the
// Rust variant name "Text".
func canonicalAgentMessageContent(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok || kind != "Text" {
		return nil, false
	}
	text, ok := canonicalStrictString(fields["text"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", "Text")
	_ = out.rawField("text", canonicalSerdeString(text))
	return out.finish(), true
}

// canonicalAsyncUserInputQuestion replays AsyncUserInputQuestion, whose options
// always serialize and which rejects unknown fields.
func canonicalAsyncUserInputQuestion(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	for key := range fields {
		if key != "title" && key != "options" {
			return nil, false
		}
	}
	title, ok := canonicalStrictString(fields["title"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.rawField("title", canonicalSerdeString(title))
	if raw, present := fields["options"]; present && !canonicalRawIsNull(raw) {
		options, ok := canonicalStringList(raw)
		if !ok {
			return nil, false
		}
		_ = out.rawField("options", options)
	} else {
		_ = out.rawField("options", []byte("null"))
	}
	return out.finish(), true
}

// canonicalPlanTurnItem replays TurnItem::Plan.
func canonicalPlanTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "Plan")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireStringField("text")
	return item.finish()
}

// canonicalReasoningTurnItem replays TurnItem::Reasoning, whose raw_content
// defaults to an empty list.
func canonicalReasoningTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "Reasoning")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.stringArrayField("summary_text", false)
	item.stringArrayField("raw_content", true)
	return item.finish()
}

// canonicalCommandExecutionTurnItem replays TurnItem::CommandExecution. The
// analytics-only sandbox_type and model_context fields never serialize.
func canonicalCommandExecutionTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "CommandExecution")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	for _, key := range []string{"plugin_id", "script_path", "process_id"} {
		item.optionalStringField(key)
	}
	command, ok := canonicalStringList(item.fields["command"])
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("command", command)
	item.pathURIField("cwd")
	parsed, ok := canonicalItemList(item.fields["parsed_cmd"], canonicalParsedCommand)
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("parsed_cmd", parsed)
	item.requireEnumField("source", "agent", "user_shell", "unified_exec_startup", "unified_exec_interaction")
	item.optionalStringField("interaction_input")
	item.requireEnumField("status", "in_progress", "completed", "failed", "declined")
	item.optionalStringField("aggregated_output")
	item.optionalIntField("exit_code")
	if raw, present := item.fields["duration"]; present && !canonicalRawIsNull(raw) {
		duration, ok := canonicalDuration(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("duration", duration)
	}
	return item.finish()
}

// canonicalParsedCommand replays ParsedCommand.
func canonicalParsedCommand(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	cmd, ok := canonicalStrictString(fields["cmd"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	switch kind {
	case "read":
		name, ok := canonicalStrictString(fields["name"])
		if !ok {
			return nil, false
		}
		path, ok := canonicalStrictString(fields["path"])
		if !ok {
			return nil, false
		}
		_ = out.field("type", kind)
		_ = out.rawField("cmd", canonicalSerdeString(cmd))
		_ = out.rawField("name", canonicalSerdeString(name))
		_ = out.rawField("path", canonicalSerdeString(path))
	case "list_files":
		_ = out.field("type", kind)
		_ = out.rawField("cmd", canonicalSerdeString(cmd))
		if !canonicalNullableStringField(out, fields, "path") {
			return nil, false
		}
	case "search":
		_ = out.field("type", kind)
		_ = out.rawField("cmd", canonicalSerdeString(cmd))
		if !canonicalNullableStringField(out, fields, "query") {
			return nil, false
		}
		if !canonicalNullableStringField(out, fields, "path") {
			return nil, false
		}
	case "unknown":
		_ = out.field("type", kind)
		_ = out.rawField("cmd", canonicalSerdeString(cmd))
	default:
		return nil, false
	}
	return out.finish(), true
}

// canonicalNullableStringField appends an Option<String> field that always
// serializes.
func canonicalNullableStringField(out *canonicalJSONObject, fields map[string]json.RawMessage, key string) bool {
	value, present, ok := canonicalItemStringField(fields, key)
	if !ok {
		return false
	}
	if present {
		_ = out.rawField(key, canonicalSerdeString(value))
	} else {
		_ = out.rawField(key, []byte("null"))
	}
	return true
}

// canonicalDynamicToolCallTurnItem replays TurnItem::DynamicToolCall.
func canonicalDynamicToolCallTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "DynamicToolCall")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.optionalStringField("namespace")
	item.requireStringField("tool")
	item.requireRawField("arguments")
	item.requireEnumField("status", "in_progress", "completed", "failed")
	if raw, present := item.fields["content_items"]; present && !canonicalRawIsNull(raw) {
		contentItems, ok := canonicalItemList(raw, canonicalDynamicToolCallContent)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("content_items", contentItems)
	}
	item.optionalBoolField("success")
	item.optionalStringField("error")
	if raw, present := item.fields["duration"]; present && !canonicalRawIsNull(raw) {
		duration, ok := canonicalDuration(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("duration", duration)
	}
	return item.finish()
}

// canonicalDynamicToolCallContent replays DynamicToolCallOutputContentItem,
// whose tags are camelCase.
func canonicalDynamicToolCallContent(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	key := map[string]string{
		"inputText":  "text",
		"inputImage": "image_url",
		"inputAudio": "audio_url",
	}[kind]
	if key == "" {
		return nil, false
	}
	value, ok := canonicalStrictString(fields[key])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.field("type", kind)
	_ = out.rawField(key, canonicalSerdeString(value))
	return out.finish(), true
}

// canonicalCollabAgentToolCallTurnItem replays TurnItem::CollabAgentToolCall.
func canonicalCollabAgentToolCallTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "CollabAgentToolCall")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireEnumField("tool", "spawn_agent", "send_input", "resume_agent", "wait", "close_agent",
		"send_message", "followup_task", "interrupt_agent", "list_agents")
	item.requireEnumField("status", "in_progress", "completed", "failed", "interrupted")
	senderID := item.requireStringField("sender_thread_id")
	if !canonicalUUIDDecodes(senderID) {
		return nil, false, nil
	}
	receiverIDs := []byte("[]")
	if raw, present := item.fields["receiver_thread_ids"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalItemList(raw, canonicalUUIDValue)
		if !ok {
			return nil, false, nil
		}
		receiverIDs = converted
	}
	_ = item.out.rawField("receiver_thread_ids", receiverIDs)
	receiverAgents := []byte("[]")
	if raw, present := item.fields["receiver_agents"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalItemList(raw, canonicalCollabAgentRef)
		if !ok {
			return nil, false, nil
		}
		receiverAgents = converted
	}
	_ = item.out.rawField("receiver_agents", receiverAgents)
	item.optionalStringField("prompt")
	item.optionalStringField("model")
	item.optionalStringField("reasoning_effort")
	agentStates := []byte("{}")
	if raw, present := item.fields["agents_states"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalAgentStates(raw)
		if !ok {
			return nil, false, nil
		}
		agentStates = converted
	}
	_ = item.out.rawField("agents_states", agentStates)
	return item.finish()
}

// canonicalUUIDValue replays a ThreadId that stands on its own.
func canonicalUUIDValue(source json.RawMessage) ([]byte, bool) {
	value, ok := canonicalRawJSONStringValue(source)
	if !ok || !canonicalUUIDDecodes(value) {
		return nil, false
	}
	return canonicalSerdeString(value), true
}

// canonicalCollabAgentRef replays CollabAgentRef.
func canonicalCollabAgentRef(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	threadID, ok := canonicalStrictString(fields["thread_id"])
	if !ok || !canonicalUUIDDecodes(threadID) {
		return nil, false
	}
	out := newCanonicalJSONObject()
	_ = out.rawField("thread_id", canonicalSerdeString(threadID))
	for _, key := range []string{"agent_nickname", "agent_role"} {
		value, present, ok := canonicalItemStringField(fields, key)
		if !ok {
			return nil, false
		}
		if present {
			_ = out.rawField(key, canonicalSerdeString(value))
		}
	}
	return out.finish(), true
}

// canonicalAgentStates replays the agents_states map. Rust hashes the map, so
// Go sorts the keys to stay deterministic.
func canonicalAgentStates(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := newCanonicalJSONObject()
	for _, key := range keys {
		if !canonicalUUIDDecodes(key) {
			return nil, false
		}
		status, ok := canonicalAgentStatus(fields[key])
		if !ok {
			return nil, false
		}
		_ = out.rawField(key, status)
	}
	return out.finish(), true
}

// canonicalAgentStatus replays AgentStatus, including the externally tagged
// completed/errored payloads.
func canonicalAgentStatus(source json.RawMessage) ([]byte, bool) {
	if text, ok := canonicalRawJSONStringValue(source); ok {
		switch text {
		case "pending_init", "running", "interrupted", "shutdown", "not_found":
			return canonicalSerdeString(text), true
		}
		return nil, false
	}
	fields, err := canonicalRawFields(source)
	if err != nil || len(fields) != 1 {
		return nil, false
	}
	for key, value := range fields {
		out := newCanonicalJSONObject()
		switch key {
		case "completed":
			_ = out.rawField("completed", []byte("null"))
			if !canonicalRawIsNull(value) {
				text, ok := canonicalRawJSONStringValue(value)
				if !ok {
					return nil, false
				}
				out = newCanonicalJSONObject()
				_ = out.rawField("completed", canonicalSerdeString(text))
			}
			return out.finish(), true
		case "errored":
			text, ok := canonicalRawJSONStringValue(value)
			if !ok {
				return nil, false
			}
			_ = out.rawField("errored", canonicalSerdeString(text))
			return out.finish(), true
		}
	}
	return nil, false
}

// canonicalSubAgentActivityTurnItem replays TurnItem::SubAgentActivity, whose
// model and reasoning_effort always serialize.
func canonicalSubAgentActivityTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "SubAgentActivity")
	if err != nil {
		return nil, false, err
	}
	item.nullableStringField("model")
	item.nullableStringField("reasoning_effort")
	item.requireStringField("id")
	item.requireEnumField("kind", "started", "interacted", "interrupted", "completed")
	agentThreadID := item.requireStringField("agent_thread_id")
	if !canonicalUUIDDecodes(agentThreadID) {
		return nil, false, nil
	}
	agentPath, ok := canonicalStrictString(item.fields["agent_path"])
	if !ok || !canonicalAgentPathDecodes(agentPath) {
		return nil, false, nil
	}
	_ = item.out.rawField("agent_path", canonicalSerdeString(agentPath))
	return item.finish()
}

// canonicalWebSearchTurnItem replays TurnItem::WebSearch.
func canonicalWebSearchTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "WebSearch")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireStringField("query")
	action, ok := canonicalCoreWebSearchAction(item.fields["action"])
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("action", action)
	if raw, present := item.fields["results"]; present && !canonicalRawIsNull(raw) {
		var results []json.RawMessage
		if err := json.Unmarshal(raw, &results); err != nil {
			return nil, false, nil
		}
		_ = item.out.rawField("results", raw)
	}
	return item.finish()
}

// canonicalCoreWebSearchAction replays the snake_case responses-API action,
// whose unknown tags collapse to "other".
func canonicalCoreWebSearchAction(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	switch kind {
	case "search":
		_ = out.field("type", kind)
		if !canonicalOptionalStringField(out, fields, "query") {
			return nil, false
		}
		if raw, present := fields["queries"]; present && !canonicalRawIsNull(raw) {
			queries, ok := canonicalStringList(raw)
			if !ok {
				return nil, false
			}
			_ = out.rawField("queries", queries)
		}
	case "open_page":
		_ = out.field("type", kind)
		if !canonicalOptionalStringField(out, fields, "url") {
			return nil, false
		}
	case "find_in_page":
		_ = out.field("type", kind)
		if !canonicalOptionalStringField(out, fields, "url") {
			return nil, false
		}
		if !canonicalOptionalStringField(out, fields, "pattern") {
			return nil, false
		}
	default:
		_ = out.field("type", "other")
	}
	return out.finish(), true
}

// canonicalOptionalStringField appends an Option<String> that is omitted when
// unset.
func canonicalOptionalStringField(out *canonicalJSONObject, fields map[string]json.RawMessage, key string) bool {
	value, present, ok := canonicalItemStringField(fields, key)
	if !ok {
		return false
	}
	if present {
		_ = out.rawField(key, canonicalSerdeString(value))
	}
	return true
}

// canonicalImageViewTurnItem replays TurnItem::ImageView.
func canonicalImageViewTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "ImageView")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.pathURIField("path")
	return item.finish()
}

// canonicalExtensionTurnItem replays the extension-owned envelope, whose kind
// selects one of three schemas.
func canonicalExtensionTurnItem(source json.RawMessage) ([]byte, bool, error) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false, err
	}
	kind, ok := canonicalStrictString(fields["kind"])
	if !ok {
		return nil, false, nil
	}
	switch kind {
	case "image_gen.generation":
		return canonicalExtensionImageGeneration(source, kind)
	case "clock.sleep":
		item, err := newCanonicalItemObject(source, "Extension")
		if err != nil {
			return nil, false, err
		}
		_ = item.out.field("kind", kind)
		item.requireStringField("id")
		duration, ok := canonicalStrictInt(fields["durationMs"])
		if !ok || duration < 0 {
			return nil, false, nil
		}
		_ = item.out.field("durationMs", duration)
		return item.finish()
	case "web.search":
		return canonicalExtensionWebSearch(source, kind)
	}
	return nil, false, nil
}

// canonicalExtensionImageGeneration replays the camelCase image-generation
// extension item, whose optional fields still serialize as null.
func canonicalExtensionImageGeneration(source json.RawMessage, kind string) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "Extension")
	if err != nil {
		return nil, false, err
	}
	_ = item.out.field("kind", kind)
	item.requireStringField("id")
	item.requireStringField("status")
	item.nullableStringField("revisedPrompt")
	// GeneratedImageRef is flattened; inline base64 and durable file identity
	// are the two shapes it decodes.
	if raw, present := item.fields["result"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("result", canonicalSerdeString(text))
	} else if raw, present := item.fields["fileId"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("fileId", canonicalSerdeString(text))
	} else {
		return nil, false, nil
	}
	if _, present := item.optionalBoolField("transparentBackground"); !present {
		_ = item.out.rawField("transparentBackground", []byte("null"))
	}
	if raw, present := item.fields["failure"]; present && !canonicalRawIsNull(raw) {
		failure, ok := canonicalImageGenerationFailure(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("failure", failure)
	} else {
		_ = item.out.rawField("failure", []byte("null"))
	}
	if raw, present := item.fields["savedPath"]; present && !canonicalRawIsNull(raw) {
		path, ok := canonicalAbsolutePath(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("savedPath", canonicalSerdeString(path))
	}
	if raw, present := item.fields["inputImages"]; present && !canonicalRawIsNull(raw) {
		images, ok := canonicalImageGenerationInputImages(raw)
		if !ok {
			return nil, false, nil
		}
		if string(images) != "[]" {
			_ = item.out.rawField("inputImages", images)
		}
	}
	if raw, present := item.fields["generationId"]; present && !canonicalRawIsNull(raw) {
		text, ok := canonicalRawJSONStringValue(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("generationId", canonicalSerdeString(text))
	} else {
		_ = item.out.rawField("generationId", []byte("null"))
	}
	return item.finish()
}

// canonicalExtensionWebSearch replays the camelCase standalone web-search
// extension item, whose action and results always serialize.
func canonicalExtensionWebSearch(source json.RawMessage, kind string) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "Extension")
	if err != nil {
		return nil, false, err
	}
	_ = item.out.field("kind", kind)
	item.requireStringField("id")
	item.requireStringField("query")
	if raw, present := item.fields["action"]; present && !canonicalRawIsNull(raw) {
		action, ok := canonicalExtensionWebSearchAction(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("action", action)
	} else {
		_ = item.out.rawField("action", []byte("null"))
	}
	if raw, present := item.fields["results"]; present && !canonicalRawIsNull(raw) {
		var results []json.RawMessage
		if err := json.Unmarshal(raw, &results); err != nil {
			return nil, false, nil
		}
		_ = item.out.rawField("results", raw)
	} else {
		_ = item.out.rawField("results", []byte("null"))
	}
	return item.finish()
}

// canonicalExtensionWebSearchAction replays the camelCase extension action.
func canonicalExtensionWebSearchAction(source json.RawMessage) ([]byte, bool) {
	fields, err := canonicalRawFields(source)
	if err != nil {
		return nil, false
	}
	kind, ok := canonicalStrictString(fields["type"])
	if !ok {
		return nil, false
	}
	out := newCanonicalJSONObject()
	switch kind {
	case "search":
		_ = out.field("type", kind)
		if !canonicalNullableStringField(out, fields, "query") {
			return nil, false
		}
		if raw, present := fields["queries"]; present && !canonicalRawIsNull(raw) {
			queries, ok := canonicalStringList(raw)
			if !ok {
				return nil, false
			}
			_ = out.rawField("queries", queries)
		} else {
			_ = out.rawField("queries", []byte("null"))
		}
	case "openPage":
		_ = out.field("type", kind)
		if !canonicalNullableStringField(out, fields, "url") {
			return nil, false
		}
	case "findInPage":
		_ = out.field("type", kind)
		if !canonicalNullableStringField(out, fields, "url") {
			return nil, false
		}
		if !canonicalNullableStringField(out, fields, "pattern") {
			return nil, false
		}
	default:
		_ = out.field("type", "other")
	}
	return out.finish(), true
}

// canonicalImageGenerationTurnItem replays the core OpenAPI
// TurnItem::ImageGeneration variant.
func canonicalImageGenerationTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "ImageGeneration")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireStringField("status")
	item.optionalStringField("revised_prompt")
	item.requireStringField("result")
	item.absolutePathField("saved_path")
	return item.finish()
}

// canonicalEnteredReviewModeTurnItem replays TurnItem::EnteredReviewMode.
func canonicalEnteredReviewModeTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "EnteredReviewMode")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	target, ok := canonicalReviewTarget(item.fields["target"])
	if !ok {
		return nil, false, nil
	}
	_ = item.out.rawField("target", target)
	item.requireStringField("user_facing_hint")
	return item.finish()
}

// canonicalExitedReviewModeTurnItem replays TurnItem::ExitedReviewMode, whose
// review_output always serializes.
func canonicalExitedReviewModeTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "ExitedReviewMode")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	if raw, present := item.fields["review_output"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalReviewOutput(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("review_output", converted)
	} else {
		_ = item.out.rawField("review_output", []byte("null"))
	}
	return item.finish()
}

// canonicalFileChangeItemPayload replays TurnItem::FileChange.
func canonicalFileChangeItemPayload(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "FileChange")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	changes, present := item.fields["changes"]
	if !present || canonicalRawIsNull(changes) {
		return nil, false, nil
	}
	if _, err := canonicalRawFields(changes); err != nil {
		return nil, false, nil
	}
	_ = item.out.rawField("changes", changes)
	item.optionalEnumField("status", "completed", "failed", "declined")
	item.optionalBoolField("auto_approved")
	item.optionalStringField("stdout")
	item.optionalStringField("stderr")
	return item.finish()
}

// canonicalMcpToolCallItemPayload replays TurnItem::McpToolCall, whose fields
// are camelCase.
func canonicalMcpToolCallItemPayload(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "McpToolCall")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	item.requireStringField("server")
	item.requireStringField("tool")
	item.requireRawField("arguments")
	item.optionalStringField("connectorId")
	item.optionalStringField("mcpAppResourceUri")
	if raw, present := item.fields["mcpAppUi"]; present && !canonicalRawIsNull(raw) {
		converted, ok := canonicalMcpAppUi(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("mcpAppUi", converted)
	}
	for _, key := range []string{"linkId", "appName", "actionName", "pluginId"} {
		item.optionalStringField(key)
	}
	item.optionalBoolField("readOnlyHint")
	item.requireEnumField("status", "inProgress", "completed", "failed")
	if raw, present := item.fields["result"]; present && !canonicalRawIsNull(raw) {
		converted, _, ok := canonicalCallToolResult(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("result", converted)
	}
	if raw, present := item.fields["error"]; present && !canonicalRawIsNull(raw) {
		errorFields, err := canonicalRawFields(raw)
		if err != nil {
			return nil, false, nil
		}
		message, ok := canonicalStrictString(errorFields["message"])
		if !ok {
			return nil, false, nil
		}
		errorOut := newCanonicalJSONObject()
		_ = errorOut.rawField("message", canonicalSerdeString(message))
		_ = item.out.rawField("error", errorOut.finish())
	}
	if raw, present := item.fields["duration"]; present && !canonicalRawIsNull(raw) {
		duration, ok := canonicalDuration(raw)
		if !ok {
			return nil, false, nil
		}
		_ = item.out.rawField("duration", duration)
	}
	return item.finish()
}

// canonicalContextCompactionTurnItem replays TurnItem::ContextCompaction.
func canonicalContextCompactionTurnItem(source json.RawMessage) ([]byte, bool, error) {
	item, err := newCanonicalItemObject(source, "ContextCompaction")
	if err != nil {
		return nil, false, err
	}
	item.requireStringField("id")
	return item.finish()
}

// canonicalUUIDDecodes mirrors uuid's accepted spellings, which ThreadId uses
// when it decodes a rollout record.
func canonicalUUIDDecodes(text string) bool {
	value := text
	if len(value) >= 9 && strings.EqualFold(value[:9], "urn:uuid:") {
		value = value[9:]
	}
	switch len(value) {
	case 36:
		if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
			return false
		}
		value = value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	case 32:
	default:
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ('0' <= ch && ch <= '9') || ('a' <= ch && ch <= 'f') || ('A' <= ch && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}

// canonicalAgentPathDecodes replays AgentPath's absolute-path validation.
func canonicalAgentPathDecodes(path string) bool {
	if path == "/morpheus" {
		return true
	}
	stripped, ok := strings.CutPrefix(path, "/")
	if !ok {
		return false
	}
	segments := strings.Split(stripped, "/")
	if len(segments) == 0 || segments[0] != "root" {
		return false
	}
	if strings.HasSuffix(stripped, "/") {
		return false
	}
	for _, segment := range segments[1:] {
		if segment == "" || segment == "root" || segment == "." || segment == ".." {
			return false
		}
		for i := 0; i < len(segment); i++ {
			ch := segment[i]
			if ('a' <= ch && ch <= 'z') || ('0' <= ch && ch <= '9') || ch == '_' {
				continue
			}
			return false
		}
	}
	return true
}
