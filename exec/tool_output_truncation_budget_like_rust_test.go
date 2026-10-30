package exec

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/tool"
	"codex_go/turn"
	"codex_go/utils"
)

func TestSessionItemForToolOutputStampsHistoryBudgetLikeRust(t *testing.T) {
	body := strings.Repeat("diagnostic line\n", 300)
	policy := utils.TokensPolicy(10)
	for _, test := range []struct {
		name string
		kind tool.PayloadKind
	}{
		{name: "function", kind: tool.PayloadFunction},
		{name: "custom", kind: tool.PayloadCustom},
	} {
		t.Run(test.name, func(t *testing.T) {
			execution := &turn.ToolExecutionResult{
				Invocation: &tool.Invocation{
					CallID:     "call-" + test.name,
					ToolName:   tool.PlainName("exec_command"),
					Payload:    tool.Payload{Kind: test.kind},
					Truncation: &policy,
				},
				Output: &tool.Output{Body: body, Success: true},
			}
			item, ok := sessionItemForToolOutput("turn-budget", "1", execution, fixedExecTime(), nil)
			if !ok {
				t.Fatal("sessionItemForToolOutput() = false")
			}
			raw, _ := item.Data["harness_metadata"].(json.RawMessage)
			var metadata struct {
				Limit *uint64 `json:"fallback_token_limit_override"`
			}
			if err := json.Unmarshal(raw, &metadata); err != nil {
				t.Fatalf("decode harness metadata %q: %v", raw, err)
			}
			if metadata.Limit == nil || *metadata.Limit != 12 {
				t.Fatalf("saved history budget = %v, want 12", metadata.Limit)
			}
			bounded, _ := item.Data["output"].(string)
			if bounded == body || strings.Count(bounded, "tokens truncated") != 1 {
				t.Fatalf("recorded output was not bounded exactly once: %q", bounded)
			}
		})
	}
}

func TestSessionItemForToolSearchOutputHasNoHistoryBudgetLikeRust(t *testing.T) {
	policy := utils.TokensPolicy(10)
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:     "search-1",
			ToolName:   tool.PlainName("tool_search"),
			Payload:    tool.Payload{Kind: tool.PayloadToolSearch},
			Truncation: &policy,
		},
		Output: &tool.Output{Body: `{"tools":[]}`, Success: true},
	}
	item, ok := sessionItemForToolOutput("turn-budget", "1", execution, fixedExecTime(), nil)
	if !ok {
		t.Fatal("sessionItemForToolOutput() = false")
	}
	if _, present := item.Data["harness_metadata"]; present {
		t.Fatalf("tool-search output carried a function-output budget: %#v", item.Data)
	}
}
