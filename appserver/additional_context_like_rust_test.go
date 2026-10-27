package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func newAdditionalContextTestRouter(t *testing.T, features string) (*RuntimeRouter, *recordingRuntimeAgent, *NotificationBuffer, *session.Store) {
	t.Helper()
	home := t.TempDir()
	if features != "" {
		if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(config.ConfigPath(home), []byte(features), 0o600); err != nil {
			t.Fatalf("write config error = %v", err)
		}
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		Config:       config.NewConfigService(home),
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	return router, agent, sink, store
}

// additionalContextToolOutputs extracts the function_call_output input items of
// a request, including items replayed from the thread history.
func additionalContextToolOutputs(t *testing.T, items []any) []map[string]any {
	t.Helper()
	var outputs []map[string]any
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal input item: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal input item: %v", err)
		}
		if decoded["type"] == "function_call_output" {
			outputs = append(outputs, decoded)
		}
	}
	return outputs
}

// additionalContextUserMessageText joins the text of the request's user message
// items so a test can prove context no longer rides in user messages.
func additionalContextUserMessageText(t *testing.T, items []any) string {
	t.Helper()
	var texts []string
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal input item: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal input item: %v", err)
		}
		if decoded["type"] != "message" || decoded["role"] != "user" {
			continue
		}
		texts = append(texts, inputItemText(item))
	}
	return strings.Join(texts, "\n")
}

func TestRuntimeRouterAdditionalContextToolOutputsLikeRust(t *testing.T) {
	router, agent, sink, _ := newAdditionalContextTestRouter(t, "[features]\nuntrusted_as_tool_output = true\n")
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello context",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	runTurn := func(id int64, context map[string]turn.AdditionalContextEntry, prompt string) *model.AgentRequest {
		t.Helper()
		start := router.Handle(requestWithParams(t, IntID(id), MethodTurnStart, turn.TurnStartParams{
			ThreadID:          threadID,
			Prompt:            prompt,
			AdditionalContext: context,
		}))
		if start.Error != nil {
			t.Fatalf("turn start error: %+v", start.Error)
		}
		request := waitForRuntimeAgentRequest(t, agent)
		waitForTurnCompletedStatus(t, sink, start.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
		return &request
	}

	narrow := "browser"
	first := runTurn(2, map[string]turn.AdditionalContextEntry{
		"automation_info": {Value: "run one", Kind: turn.AdditionalContextApplication},
		"browser_info":    {Value: "tab one", Kind: turn.AdditionalContextUntrusted},
	}, "inspect context")
	if !strings.Contains(first.Instructions, "run one") {
		t.Fatalf("application context left the developer instructions: %q", first.Instructions)
	}
	outputs := additionalContextToolOutputs(t, first.InputItems)
	if len(outputs) != 1 {
		t.Fatalf("tool outputs = %#v, want one", outputs)
	}
	if got := outputs[0]; got["namespace"] != "agent" || got["name"] != "browser_info" || got["output"] != "tab one" {
		t.Fatalf("tool output = %#v", got)
	}
	if _, ok := outputs[0]["call_id"]; ok {
		t.Fatalf("tool output kept a call_id: %#v", outputs[0])
	}
	if strings.Contains(additionalContextUserMessageText(t, first.InputItems), "tab one") {
		t.Fatalf("untrusted context leaked into user text: %#v", first.InputItems)
	}

	second := runTurn(3, map[string]turn.AdditionalContextEntry{
		"automation_info": {Value: "run one", Kind: turn.AdditionalContextApplication},
		"browser_info":    {Value: "tab one", Kind: turn.AdditionalContextUntrusted},
	}, "same context")
	if got := len(additionalContextToolOutputs(t, second.InputItems)); got != 0 {
		t.Fatalf("unchanged context was re-emitted: %d tool outputs", got)
	}
	if strings.Contains(second.Instructions, "run one") {
		t.Fatalf("unchanged application context was re-emitted: %q", second.Instructions)
	}

	third := runTurn(4, map[string]turn.AdditionalContextEntry{
		"browser_info": {Value: "tab two", Kind: turn.AdditionalContextUntrusted},
	}, "changed context")
	outputs = additionalContextToolOutputs(t, third.InputItems)
	if len(outputs) != 1 || outputs[0]["output"] != "tab two" {
		t.Fatalf("changed context output = %#v", outputs)
	}

	fourth := runTurn(5, map[string]turn.AdditionalContextEntry{
		"browser_info": {Value: "tab two", Kind: turn.AdditionalContextUntrusted, Name: &narrow},
	}, "renamed context")
	outputs = additionalContextToolOutputs(t, fourth.InputItems)
	if len(outputs) != 1 {
		t.Fatalf("renamed context was not emitted: %#v", outputs)
	}
	if got := outputs[0]; got["name"] != "browser" || got["output"] != "tab two" {
		t.Fatalf("renamed context output = %#v", got)
	}
}

func TestDerivedAdditionalContextToolNameLikeRust(t *testing.T) {
	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "browser_info", want: "browser_info"},
		{key: "browser info", want: "browser_info"},
		{key: "browser-2!", want: "browser2"},
		{key: "  ", want: "__"},
		{key: "!!!", want: fallbackAdditionalContextToolName},
		{key: strings.Repeat("a", 200), want: strings.Repeat("a", maxDerivedAdditionalContextNameChars)},
	} {
		if got := derivedAdditionalContextToolName(test.key); got != test.want {
			t.Fatalf("derivedAdditionalContextToolName(%q) = %q, want %q", test.key, got, test.want)
		}
	}
}

func TestRuntimeRouterAdditionalContextToolOutputTruncationLikeRust(t *testing.T) {
	router, agent, sink, _ := newAdditionalContextTestRouter(t, "[features]\nuntrusted_as_tool_output = true\n")
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello truncation",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	runTurn := func(id int64, value string, prompt string) *model.AgentRequest {
		t.Helper()
		start := router.Handle(requestWithParams(t, IntID(id), MethodTurnStart, turn.TurnStartParams{
			ThreadID: threadID,
			Prompt:   prompt,
			AdditionalContext: map[string]turn.AdditionalContextEntry{
				"browser_info": {Value: value, Kind: turn.AdditionalContextUntrusted},
			},
		}))
		if start.Error != nil {
			t.Fatalf("turn start error: %+v", start.Error)
		}
		request := waitForRuntimeAgentRequest(t, agent)
		waitForTurnCompletedStatus(t, sink, start.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
		return &request
	}

	longValue := "browser-head-" + strings.Repeat("b", 20_000) + "-browser-tail"
	first := runTurn(2, longValue, "long context")
	outputs := additionalContextToolOutputs(t, first.InputItems)
	if len(outputs) != 1 {
		t.Fatalf("tool outputs = %#v, want one", outputs)
	}
	body, _ := outputs[0]["output"].(string)
	if !strings.HasPrefix(body, "browser-head-") || !strings.HasSuffix(body, "-browser-tail") {
		t.Fatalf("truncated body lost its edges: %q", body)
	}
	if len(body) >= len(longValue) {
		t.Fatalf("body length = %d, want less than %d", len(body), len(longValue))
	}
	if !strings.Contains(body, "tokens truncated") {
		t.Fatalf("truncated body lacks the marker: %q", body)
	}
	if len(body) > 5*1024 {
		t.Fatalf("body length = %d, want at most %d", len(body), 5*1024)
	}

	changedMiddle := longValue[:10_000] + "c" + longValue[10_001:]
	second := runTurn(3, changedMiddle, "changed middle")
	if got := len(additionalContextToolOutputs(t, second.InputItems)); got != 0 {
		t.Fatalf("truncated-middle change was re-emitted: %d tool outputs", got)
	}

	changedHead := strings.Replace(longValue, "browser-head", "browser-view", 1)
	third := runTurn(4, changedHead, "changed head")
	outputs = additionalContextToolOutputs(t, third.InputItems)
	if len(outputs) != 1 {
		t.Fatalf("changed head was not emitted: %#v", outputs)
	}
	if !strings.HasPrefix(outputs[0]["output"].(string), "browser-view") {
		t.Fatalf("changed head output = %q", outputs[0]["output"])
	}
}

func TestRuntimeRouterAdditionalContextMessagesPreservedWhenFeatureDisabled(t *testing.T) {
	router, agent, sink, _ := newAdditionalContextTestRouter(t, "")
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello legacy",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	start := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "legacy context",
		AdditionalContext: map[string]turn.AdditionalContextEntry{
			"browser_info": {Value: "tab one", Kind: turn.AdditionalContextUntrusted},
		},
	}))
	if start.Error != nil {
		t.Fatalf("turn start error: %+v", start.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if got := len(additionalContextToolOutputs(t, request.InputItems)); got != 0 {
		t.Fatalf("tool outputs with the feature disabled = %d, want 0", got)
	}
	if !strings.Contains(inputItemText(request.InputItems), "tab one") {
		t.Fatalf("legacy user message missing the context: %#v", request.InputItems)
	}
	waitForTurnCompletedStatus(t, sink, start.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}

func TestRuntimeRouterTurnSteerAdditionalContextPrecedesUserInputLikeRust(t *testing.T) {
	for _, test := range []struct {
		name     string
		features string
		toolOut  bool
	}{
		{name: "legacy messages"},
		{name: "tool outputs", features: "[features]\nuntrusted_as_tool_output = true\n", toolOut: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, _, _, _ := newAdditionalContextTestRouter(t, test.features)
			params := &turn.TurnSteerParams{
				ThreadID: "thread-steer-context",
				Prompt:   "steer",
				AdditionalContext: map[string]turn.AdditionalContextEntry{
					"browser_info": {Value: "tab one", Kind: turn.AdditionalContextUntrusted},
				},
			}
			items := router.inputItemsFromTurnSteer(params, true)
			if len(items) != 2 {
				t.Fatalf("steer items = %#v", items)
			}
			first := items[0].(map[string]any)
			second := items[1].(map[string]any)
			if test.toolOut {
				if first["type"] != "function_call_output" || first["name"] != "browser_info" || first["namespace"] != "agent" {
					t.Fatalf("steer context item = %#v", first)
				}
			} else if !strings.Contains(inputItemText(first), "tab one") {
				t.Fatalf("steer context item = %#v", first)
			}
			if second["role"] != "user" || !strings.Contains(inputItemText(second), "steer") {
				t.Fatalf("steer user item = %#v", second)
			}
		})
	}
}
