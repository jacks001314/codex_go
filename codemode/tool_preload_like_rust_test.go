package codemode

import (
	"context"
	"strings"
	"testing"
)

func toolPreloadDefinition(binding string, name ToolName) ProtocolToolDefinition {
	return ProtocolToolDefinition{
		Name:     binding,
		ToolName: name,
		Kind:     ProtocolToolKindFunction,
	}
}

func executeToolPreloadCell(t *testing.T, session *SessionRuntime, tools []ProtocolToolDefinition, source string) RuntimeResponse {
	t.Helper()
	started, err := session.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:   "cell",
		EnabledTools: tools,
		Source:       source,
	}, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if started.InitialResponse.Variant != "Result" {
		t.Fatalf("initial response = %#v, want a completed cell", started.InitialResponse)
	}
	return started.InitialResponse
}

func toolPreloadCellText(t *testing.T, response RuntimeResponse) string {
	t.Helper()
	if response.ErrorText != nil {
		t.Fatalf("cell failed: %s", *response.ErrorText)
	}
	if len(response.ContentItems) != 1 || response.ContentItems[0].Type != "input_text" {
		t.Fatalf("content items = %#v", response.ContentItems)
	}
	return response.ContentItems[0].Text
}

// Mirrors Rust #62430 `preloads_follow_the_session_and_the_current_cells_tool_binding`:
// each session selects its own helpers and a helper only runs for a cell whose
// catalog exposes the binding.
func TestToolPreloadsFollowSessionAndCellBindingLikeRust(t *testing.T) {
	plain := NewSessionRuntime()
	lookup := NewSessionRuntime()
	lookup.SetToolPreloads([]ToolPreload{{
		Tool: "lookup", ToolName: "lookup",
		Source: `globalThis.findItem = () => "on";`,
	}})
	alternate := NewSessionRuntime()
	alternate.SetToolPreloads([]ToolPreload{{
		Tool: "lookup", ToolName: "lookup",
		Source: `globalThis.findItem = () => "off";`,
	}})
	source := `text(typeof findItem === "function" ? findItem() : typeof findItem);`
	lookupTool := []ProtocolToolDefinition{toolPreloadDefinition("lookup", PlainToolName("lookup"))}
	otherTool := []ProtocolToolDefinition{toolPreloadDefinition("lookup_other", PlainToolName("lookup_other"))}
	for _, test := range []struct {
		name    string
		session *SessionRuntime
		tools   []ProtocolToolDefinition
		want    string
	}{
		{name: "helper session", session: lookup, tools: lookupTool, want: "on"},
		{name: "plain session", session: plain, tools: lookupTool, want: "undefined"},
		{name: "alternate session", session: alternate, tools: lookupTool, want: "off"},
		{name: "other binding", session: lookup, tools: otherTool, want: "undefined"},
		{name: "helper session again", session: lookup, tools: lookupTool, want: "on"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toolPreloadCellText(t, executeToolPreloadCell(t, test.session, test.tools, source)); got != test.want {
				t.Fatalf("cell text = %q, want %q", got, test.want)
			}
		})
	}
}

// Mirrors Rust #62430 `preload_requires_one_binding_with_the_exact_dispatch_identity`.
func TestToolPreloadRequiresExactDispatchIdentityLikeRust(t *testing.T) {
	const binding = "send_message"
	session := NewSessionRuntime()
	session.SetToolPreloads([]ToolPreload{{
		Tool: binding, ToolName: binding,
		Source: `globalThis.install = () => "installed";`,
	}})
	source := `text(typeof install === "function" ? install() : typeof install);`
	namespace := "mcp__apps"
	for _, test := range []struct {
		name  string
		tools []ProtocolToolDefinition
		want  string
	}{
		{
			name:  "canonical",
			tools: []ProtocolToolDefinition{toolPreloadDefinition(binding, PlainToolName(binding))},
			want:  "installed",
		},
		{
			name:  "reassigned name",
			tools: []ProtocolToolDefinition{toolPreloadDefinition(binding, PlainToolName("unrelated"))},
			want:  "undefined",
		},
		{
			name:  "wrong namespace",
			tools: []ProtocolToolDefinition{toolPreloadDefinition(binding, NamespacedToolName(namespace, binding))},
			want:  "undefined",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toolPreloadCellText(t, executeToolPreloadCell(t, session, test.tools, source)); got != test.want {
				t.Fatalf("cell text = %q, want %q", got, test.want)
			}
		})
	}

	namespaced := NewSessionRuntime()
	namespaced.SetToolPreloads([]ToolPreload{{
		Tool: binding, ToolName: "user_message_send_message", ToolNamespace: &namespace,
		Source: `globalThis.install = () => "namespaced";`,
	}})
	split := []ProtocolToolDefinition{toolPreloadDefinition(binding, NamespacedToolName(namespace, "user_message_send_message"))}
	if got := toolPreloadCellText(t, executeToolPreloadCell(t, namespaced, split, source)); got != "namespaced" {
		t.Fatalf("namespaced cell text = %q, want namespaced", got)
	}
}

// Mirrors Rust #62430 `ambiguous bindings` coverage: two catalog entries that
// normalize to the same binding make the cell fail instead of picking one.
func TestToolPreloadsRejectAmbiguousBindingsLikeRust(t *testing.T) {
	session := NewSessionRuntime()
	session.SetToolPreloads([]ToolPreload{{
		Tool: "a_b", ToolName: "a_b", Source: `globalThis.install = () => "installed";`,
	}})
	tools := []ProtocolToolDefinition{
		toolPreloadDefinition("a-b", PlainToolName("a-b")),
		toolPreloadDefinition("a.b", PlainToolName("a.b")),
	}
	response := executeToolPreloadCell(t, session, tools, `text("never runs")`)
	if response.ErrorText == nil || *response.ErrorText != "ambiguous binding: tool preload #1 for `a_b`" {
		t.Fatalf("error text = %v, want the bounded ambiguous-binding error", response.ErrorText)
	}
}

// Mirrors Rust #62430 bounded helper failures: compile and execution errors
// report the preload label and truncate long binding names.
func TestToolPreloadsReportBoundedHelperFailuresLikeRust(t *testing.T) {
	tools := []ProtocolToolDefinition{toolPreloadDefinition("lookup", PlainToolName("lookup"))}
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "compile", source: "const =", want: "failed to compile tool preload #1 for `lookup`"},
		{name: "execute", source: `throw new Error("boom")`, want: "failed to execute tool preload #1 for `lookup`"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := NewSessionRuntime()
			session.SetToolPreloads([]ToolPreload{{Tool: "lookup", ToolName: "lookup", Source: test.source}})
			response := executeToolPreloadCell(t, session, tools, `text("never runs")`)
			if response.ErrorText == nil || *response.ErrorText != test.want {
				t.Fatalf("error text = %v, want %q", response.ErrorText, test.want)
			}
		})
	}

	longTool := strings.Repeat("a", maxToolPreloadBindingChars+20)
	session := NewSessionRuntime()
	session.SetToolPreloads([]ToolPreload{{Tool: longTool, ToolName: longTool, Source: "const ="}})
	longTools := []ProtocolToolDefinition{toolPreloadDefinition(longTool, PlainToolName(longTool))}
	response := executeToolPreloadCell(t, session, longTools, `text("never runs")`)
	want := "failed to compile tool preload #1 for `" + strings.Repeat("a", maxToolPreloadBindingChars) + "`"
	if response.ErrorText == nil || *response.ErrorText != want {
		t.Fatalf("error text = %v, want the truncated label %q", response.ErrorText, want)
	}
}
