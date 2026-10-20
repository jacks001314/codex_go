package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/model"
)

func TestModelRequestHookInputAndSelectionLikeRust(t *testing.T) {
	errorText := "boom"
	request := &HookModelRequestRequest{
		ThreadID:   "thread-1",
		TurnID:     "turn-1",
		CWD:        "/workspace",
		Model:      "gpt-test",
		Phase:      "post",
		Status:     "failed",
		Error:      &errorText,
		TokenUsage: &model.AgentUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
		Hooks: []HookMetadata{{
			EventName:   HookEventPostModelRequest,
			Source:      HookSourceUser,
			Enabled:     true,
			HandlerType: HookHandlerCommand,
			Command:     stringPtrForModelHookTest("true"),
			TrustStatus: HookTrustTrusted,
		}},
	}
	inputJSON, err := HookInputJSON(request)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input["hook_event_name"] != "PostModelRequest" || input["session_id"] != "thread-1" || input["turn_id"] != "turn-1" || input["status"] != "failed" || input["error"] != "boom" {
		t.Fatalf("input = %#v", input)
	}
	usage, _ := input["token_usage"].(map[string]any)
	if usage["total_tokens"] != float64(3) {
		t.Fatalf("token_usage = %#v", input["token_usage"])
	}
	if selected := selectHookHandlers(request.Hooks, HookEventPreModelRequest, nil); len(selected) != 0 {
		t.Fatalf("pre selected post hook: %#v", selected)
	}
	if selected := selectHookHandlers(request.Hooks, HookEventPostModelRequest, nil); len(selected) != 1 {
		t.Fatalf("post hooks = %#v", selected)
	}
}

func stringPtrForModelHookTest(value string) *string {
	return &value
}
