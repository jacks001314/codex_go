package context

import (
	"strings"
)

// delegatedPromptPrefix is the Rust TUI delegation marker used when a
// create_thread / send_message_to_thread tool starts a delegated turn
// (#41046).
const delegatedPromptPrefix = "<codex_delegation>\n  <source_thread_id>"
const delegatedPromptSuffix = "</codex_delegation>"

// ParseDelegatedPrompt extracts the source thread id and delegated prompt from
// a codex delegation-marked prompt (Rust parse_delegated_prompt, #41046).
// Returns nil,false when the prompt is not a well-formed delegation marker.
func ParseDelegatedPrompt(prompt string) (string, string, bool) {
	if !strings.HasPrefix(prompt, delegatedPromptPrefix) {
		return "", "", false
	}
	rest := prompt[len(delegatedPromptPrefix):]
	idx := strings.Index(rest, "</source_thread_id>\n  <input>")
	if idx < 0 {
		return "", "", false
	}
	source := rest[:idx]
	rest = rest[idx+len("</source_thread_id>\n  <input>"):]
	outputSuffix := "</input>\n" + delegatedPromptSuffix
	if !strings.HasSuffix(rest, outputSuffix) {
		return "", "", false
	}
	delegated := strings.TrimSuffix(rest, outputSuffix)
	// Rust decodes `&lt;`/`&gt;` before `&amp;`, so `&amp;lt;` stays escaped
	// (`&lt;`) instead of being decoded twice.
	unescape := func(value string) string {
		value = strings.ReplaceAll(value, "&lt;", "<")
		value = strings.ReplaceAll(value, "&gt;", ">")
		return strings.ReplaceAll(value, "&amp;", "&")
	}
	return unescape(source), unescape(delegated), true
}

// ParseDelegatedToolOutput reports whether a tool output is a delegation from a
// trusted Codex namespace (#41046), returning the source thread id and prompt.
func ParseDelegatedToolOutput(name string, namespace string, output string) (string, string, bool) {
	if namespace != "codex_tui" && namespace != "codex_app" {
		return "", "", false
	}
	if name != "create_thread" && name != "send_message_to_thread" {
		return "", "", false
	}
	return ParseDelegatedPrompt(output)
}

// DelegatedToolOutputNotice is the history text Rust renders for a replayed
// delegated tool output (#41046): the source task attribution followed by the
// delegated prompt on the next line.
func DelegatedToolOutputNotice(sourceThreadID string, prompt string) string {
	return "Sent by Codex from task " + sourceThreadID + "\n" + prompt
}
