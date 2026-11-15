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

// Mirrors Rust #61562/#61587: the hook input carries the shared session id,
// the issuing thread id, the sampling kind and the per-attempt identity.
func TestModelRequestHookInputCarriesAttemptIdentityLikeRust(t *testing.T) {
	responseID := "resp-1"
	pre, err := HookInputJSON(&HookModelRequestRequest{
		ThreadID:      "thread-1",
		SessionID:     "session-1",
		TurnID:        "turn-1",
		CWD:           "/workspace",
		Model:         "gpt-test",
		Phase:         "pre",
		Kind:          string(model.SamplingKindBackgroundCompaction),
		RequestID:     "request-1",
		AttemptNumber: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var preInput map[string]any
	if err := json.Unmarshal([]byte(pre), &preInput); err != nil {
		t.Fatal(err)
	}
	if preInput["session_id"] != "session-1" || preInput["thread_id"] != "thread-1" {
		t.Fatalf("session/thread identity = %#v", preInput)
	}
	if preInput["kind"] != "background_compaction" || preInput["request_id"] != "request-1" || preInput["attempt_number"] != float64(2) {
		t.Fatalf("attempt identity = %#v", preInput)
	}
	if _, ok := preInput["response_id"]; ok {
		t.Fatalf("pre input must not report a response id: %#v", preInput)
	}

	post, err := HookInputJSON(&HookModelRequestRequest{
		ThreadID: "thread-1", SessionID: "session-1", TurnID: "turn-1", CWD: "/workspace",
		Model: "gpt-test", Phase: "post", Status: "completed", Kind: "sampling",
		RequestID: "request-1", AttemptNumber: 2, ResponseID: &responseID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var postInput map[string]any
	if err := json.Unmarshal([]byte(post), &postInput); err != nil {
		t.Fatal(err)
	}
	if postInput["status"] != "completed" || postInput["response_id"] != "resp-1" || postInput["kind"] != "sampling" {
		t.Fatalf("post input = %#v", postInput)
	}
}

func stringPtrForModelHookTest(value string) *string {
	return &value
}
