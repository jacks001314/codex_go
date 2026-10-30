package chatwidget

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

const delegatedReplayOutput = "<codex_delegation>\n  <source_thread_id>source-task</source_thread_id>\n  <input>Inspect &lt;main&gt; &amp; report</input>\n</codex_delegation>"

// TestReplayDelegatedToolOutputLikeRust mirrors Rust #41046
// parse_delegated_tool_output: only create_thread / send_message_to_thread
// outputs from the codex_tui or codex_app namespaces are treated as delegated
// prompts, and the wrapper entities decode in Rust's order.
func TestReplayDelegatedToolOutputLikeRust(t *testing.T) {
	item := appserver.ThreadItem{Type: "function_call_output", Name: "create_thread", Namespace: "codex_tui", Data: map[string]any{"output": delegatedReplayOutput}}
	source, prompt, ok := ReplayDelegatedToolOutput(item)
	if !ok || source != "source-task" || prompt != "Inspect <main> & report" {
		t.Fatalf("delegated = (%q,%q,%v)", source, prompt, ok)
	}

	item.Namespace = "codex_app"
	if _, _, ok := ReplayDelegatedToolOutput(item); !ok {
		t.Fatalf("codex_app namespace was rejected")
	}
	item.Namespace = "untrusted"
	if _, _, ok := ReplayDelegatedToolOutput(item); ok {
		t.Fatalf("untrusted namespace was accepted")
	}
	item.Namespace = "codex_tui"
	item.Name = "other"
	if _, _, ok := ReplayDelegatedToolOutput(item); ok {
		t.Fatalf("non-delegation tool was accepted")
	}
	item.Name = "send_message_to_thread"
	item.Data = map[string]any{"output": "plain output"}
	if _, _, ok := ReplayDelegatedToolOutput(item); ok {
		t.Fatalf("plain output was accepted")
	}
	item.Data = nil
	item.Text = "plain text"
	if _, _, ok := ReplayDelegatedToolOutput(item); ok {
		t.Fatalf("plain item text was accepted")
	}
	item.Type = "agentMessage"
	item.Text = delegatedReplayOutput
	if _, _, ok := ReplayDelegatedToolOutput(item); ok {
		t.Fatalf("non function-call-output item was accepted")
	}

	escaped := appserver.ThreadItem{Type: "function_call_output", Name: "create_thread", Namespace: "codex_tui", Text: "<codex_delegation>\n  <source_thread_id>s</source_thread_id>\n  <input>&amp;lt;literal&amp;gt;</input>\n</codex_delegation>"}
	_, prompt, ok = ReplayDelegatedToolOutput(escaped)
	if !ok || prompt != "&lt;literal&gt;" {
		t.Fatalf("escaped prompt = %q, want &lt;literal&gt;", prompt)
	}
}

// TestReplayDelegatedToolOutputCellRendersLikeRust freezes the history cell
// shape Rust's replay and transcript renderers share (#41046).
func TestReplayDelegatedToolOutputCellRendersLikeRust(t *testing.T) {
	item := appserver.ThreadItem{Type: "function_call_output", Name: "send_message_to_thread", Namespace: "codex_app", Data: map[string]any{"output": delegatedReplayOutput}}
	cell, ok := ReplayDelegatedToolOutputCell(item)
	if !ok {
		t.Fatalf("delegated output did not render")
	}
	lines := cell.DisplayLines(80)
	want := []string{"• Sent by Codex from task source-task", "  Inspect <main> & report"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines = %#v, want %#v", lines, want)
	}
	if cell.RawLines()[0] != "Sent by Codex from task source-task" {
		t.Fatalf("raw lines = %#v", cell.RawLines())
	}

	if _, ok := ReplayDelegatedToolOutputCell(appserver.ThreadItem{Type: "function_call_output", Name: "other", Namespace: "codex_tui", Text: delegatedReplayOutput}); ok {
		t.Fatalf("non-delegation output rendered")
	}
}

// TestReplayFunctionCallOutputRouteLikeRust keeps replayed function outputs off
// the composer/user-message routes.
func TestReplayFunctionCallOutputRouteLikeRust(t *testing.T) {
	decision := ClassifyReplayThreadItem(ReplayThreadItemFunctionCallOutput, "", ReplayResumeInitialMessages, "turn-1", false)
	if decision.Route != ReplayItemRouteFunctionCallOutput {
		t.Fatalf("route = %q, want %q", decision.Route, ReplayItemRouteFunctionCallOutput)
	}
}
