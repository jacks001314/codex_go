package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
	"codex_go/utils"
)

// Rust #44248 saves the model budget that produced a function or custom tool
// output and reuses it when the thread is resumed or forked under a model with
// a different truncation policy.
func TestAppToolOutputPreservesOriginBudgetAcrossResumeAndForkLikeRust(t *testing.T) {
	body := strings.Repeat("diagnostic line\n", 300)
	for _, test := range []struct {
		name     string
		kind     tool.PayloadKind
		wantType string
	}{
		{name: "function", kind: tool.PayloadFunction, wantType: "function_call_output"},
		{name: "custom", kind: tool.PayloadCustom, wantType: "custom_tool_call_output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := utils.TokensPolicy(20)
			execution := &turn.ToolExecutionResult{
				Invocation: &tool.Invocation{
					CallID:     "call-budget-" + test.name,
					ToolName:   tool.PlainName("exec_command"),
					Payload:    tool.Payload{Kind: test.kind},
					Truncation: &policy,
				},
				Output: &tool.Output{Body: body, Success: true},
			}
			item, ok := sessionItemForAppToolOutput("turn-budget", execution, time.Now().UTC(), nil)
			if !ok {
				t.Fatal("sessionItemForAppToolOutput() = false")
			}
			metadata := harnessMetadataFromSessionItem(&item)
			if metadata == nil || metadata.HistoryTruncationTokenLimit == nil {
				t.Fatalf("item harness metadata = %#v", item.Data["harness_metadata"])
			}
			if got := *metadata.HistoryTruncationTokenLimit; got != 24 {
				t.Fatalf("saved history budget = %d, want 24", got)
			}
			bounded, _ := item.Data["output"].(string)
			if bounded == body || strings.Count(bounded, "tokens truncated") != 1 {
				t.Fatalf("recorded output was not bounded exactly once: %q", bounded)
			}

			replayPolicy := utils.TokensPolicy(2)
			callType := "function_call"
			if test.kind == tool.PayloadCustom {
				callType = "custom_tool_call"
			}
			history := []session.Item{
				{ID: "call-budget-" + test.name, Type: callType, CallID: "call-budget-" + test.name, Name: "exec_command", Text: "{}"},
				item,
			}
			items := session.InputItemsFromItems(history, &session.HistoryBuildOptions{
				IncludeToolOutputs: true,
				TruncationPolicy:   &replayPolicy,
			})
			if len(items) != 2 {
				t.Fatalf("replayed items = %#v", items)
			}
			replayed, _ := items[1].(map[string]any)
			if replayed["type"] != test.wantType {
				t.Fatalf("replayed type = %#v, want %q", replayed["type"], test.wantType)
			}
			replayedBody, _ := replayed["output"].(string)
			if strings.TrimSpace(replayedBody) != strings.TrimSpace(bounded) {
				t.Fatalf("replay used the new model budget: got %q, want %q", replayedBody, bounded)
			}
		})
	}
}

func TestHistoryReplayUsesCurrentBudgetForLegacyToolOutputLikeRust(t *testing.T) {
	body := strings.Repeat("legacy output\n", 300)
	for _, test := range []struct {
		name     string
		itemType string
		wantType string
	}{
		{name: "function", itemType: "function_call_output", wantType: "function_call_output"},
		{name: "custom", itemType: "custom_tool_call_output", wantType: "custom_tool_call_output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := utils.TokensPolicy(20)
			items := session.InputItemsFromItems([]session.Item{{
				ID:       "legacy-" + test.name,
				Type:     test.itemType,
				Name:     "exec_command",
				Text:     body,
				Data:     map[string]any{"output": body},
				Metadata: map[string]any{"payloadKind": string(tool.PayloadFunction)},
			}}, &session.HistoryBuildOptions{IncludeToolOutputs: true, TruncationPolicy: &policy})
			if len(items) != 1 {
				t.Fatalf("replayed items = %#v", items)
			}
			output, _ := items[0].(map[string]any)
			text, _ := output["output"].(string)
			if output["type"] != test.wantType || !strings.Contains(text, "tokens truncated") || text == body {
				t.Fatalf("legacy output = %#v, want current-model truncation", output)
			}
		})
	}
}
