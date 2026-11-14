package app

import (
	"testing"

	"codex_go/appserver"
)

// Both lifecycle tools must retain the delegated prompt in model history (#61210).
func TestDynamicToolDelegationRetainsPromptLikeRust(t *testing.T) {
	for _, tool := range []string{"create_thread", "send_message_to_thread"} {
		t.Run(tool, func(t *testing.T) {
			server := daybreakDynamicToolServer(false, "chatgpt", true)
			args := map[string]any{"prompt": "Check <main> & report"}
			if tool == "send_message_to_thread" {
				args["threadId"] = "thread-target"
			}
			response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
				ThreadID: "thread-caller", Tool: tool, Arguments: args,
			}, DynamicToolOptions{FeatureSettings: map[string]bool{"cli_daybreak": true}})
			if !response.Success {
				t.Fatalf("response = %#v", response)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if len(server.turns) != 1 {
				t.Fatalf("turns = %#v", server.turns)
			}
			output := server.turns[0].ToolOutput
			if output == nil {
				t.Fatal("missing tool output")
			}
			if !output.Retain {
				t.Error("delegated prompt is not retained")
			}
			if output.Name != tool || output.Namespace != DynamicToolNamespace {
				t.Fatalf("tool identity = %#v", output)
			}
			want := "<codex_delegation>\n  <source_thread_id>thread-caller</source_thread_id>\n  <input>Check &lt;main&gt; &amp; report</input>\n</codex_delegation>"
			if output.Output.Text != want {
				t.Fatalf("prompt = %q, want %q", output.Output.Text, want)
			}
		})
	}
}
