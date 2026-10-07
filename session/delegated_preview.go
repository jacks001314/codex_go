package session

import "strings"

// Display-only previews for recognized delegated inputs; model content is never
// rewritten.
//
// Rust #50462 (codex-rs/state/src/delegated_preview.rs,
// codex_state::delegated_output_preview): a delegated turn can begin without a
// user message, so the thread preview is derived from the delegated tool input
// to keep the thread discoverable before a user follow-up.
//
// Only the codex_app / codex_tui namespaces and the create_thread /
// send_message_to_thread tools are recognized. A recognized
// `<codex_delegation>` wrapper is unwrapped and its escaped task text decoded;
// any other non-blank body is returned unchanged (escapes are intentionally left
// alone); a blank body yields no preview.
func DelegatedOutputPreview(namespace, name, text string) (string, bool) {
	if namespace != "codex_app" && namespace != "codex_tui" {
		return "", false
	}
	if name != "create_thread" && name != "send_message_to_thread" {
		return "", false
	}
	// The Rust implementation unwraps only when the exact wrapper shape matches
	// and otherwise falls back to the raw text via `unwrap_or(text)`.
	preview := text
	if input, ok := unwrapCodexDelegation(text); ok {
		preview = decodeCodexDelegationEntities(input)
	}
	if strings.TrimSpace(preview) == "" {
		return "", false
	}
	return preview, true
}

const (
	codexDelegationPrefix    = "<codex_delegation>\n  <source_thread_id>"
	codexDelegationSeparator = "</source_thread_id>\n  <input>"
	codexDelegationSuffix    = "</input>\n</codex_delegation>"
)

// unwrapCodexDelegation mirrors the Rust strip_prefix / split_once /
// strip_suffix chain that extracts the `<input>` body of a recognized wrapper.
func unwrapCodexDelegation(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, codexDelegationPrefix)
	if !ok {
		return "", false
	}
	_, input, ok := strings.Cut(rest, codexDelegationSeparator)
	if !ok {
		return "", false
	}
	inner, ok := strings.CutSuffix(input, codexDelegationSuffix)
	if !ok {
		return "", false
	}
	return inner, true
}

// decodeCodexDelegationEntities mirrors the Rust decode order (&lt;, &gt;, &amp;)
// so `&amp;lt;` decodes to `&lt;` rather than `<`.
func decodeCodexDelegationEntities(input string) string {
	input = strings.ReplaceAll(input, "&lt;", "<")
	input = strings.ReplaceAll(input, "&gt;", ">")
	input = strings.ReplaceAll(input, "&amp;", "&")
	return input
}

// delegatedFunctionCallOutputTypes are the item types that carry a
// FunctionCallOutputBody, matching Rust TurnItem::FunctionCallOutput.
func delegatedFunctionCallOutputItem(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call_output", "tool_output":
		return true
	default:
		return false
	}
}

// DelegatedItemPreview resolves the delegated-task preview for a tool output
// item. Rust #50462: only completed `create_thread` / `send_message_to_thread`
// outputs in the codex_app / codex_tui namespaces qualify.
func DelegatedItemPreview(item *Item) (string, bool) {
	if item == nil || !delegatedFunctionCallOutputItem(item.Type) {
		return "", false
	}
	namespace := firstNonEmpty(item.Namespace, stringValue(item.Data, "namespace"))
	name := firstNonEmpty(item.Name, stringValue(item.Data, "name"))
	text, ok := delegatedItemOutputText(item)
	if !ok {
		return "", false
	}
	return DelegatedOutputPreview(namespace, name, text)
}

// delegatedItemOutputText is the Go counterpart of
// FunctionCallOutputBody::to_text (Rust #50462): the lossy plain-text rendering
// of a function-call output body. Reports false when the body has no text, which
// is the Rust `None` case.
func delegatedItemOutputText(item *Item) (string, bool) {
	if item.Text != "" {
		return item.Text, true
	}
	for _, key := range []string{"output", "text", "content_items"} {
		if value, ok := item.Data[key]; ok {
			if text, ok := delegatedOutputTextFromValue(value); ok {
				return text, true
			}
		}
	}
	segments := make([]string, 0, len(item.Content))
	for i := range item.Content {
		if strings.TrimSpace(item.Content[i].Text) != "" {
			segments = append(segments, item.Content[i].Text)
		}
	}
	if len(segments) == 0 {
		return "", false
	}
	return strings.Join(segments, "\n"), true
}

// delegatedOutputTextFromValue renders an untyped output payload (a plain string
// or a list of content blocks) as text, joining text segments with newlines and
// dropping non-text blocks exactly like function_call_output_content_items_to_text.
func delegatedOutputTextFromValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []any:
		segments := make([]string, 0, len(typed))
		for _, entry := range typed {
			block, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
				segments = append(segments, text)
			}
		}
		if len(segments) == 0 {
			return "", false
		}
		return strings.Join(segments, "\n"), true
	default:
		return "", false
	}
}
