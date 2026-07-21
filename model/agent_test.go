package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLocalAgentRunnerProducesMessageAndUsage(t *testing.T) {
	response, err := NewLocalAgentRunner().Run(context.Background(), &AgentRequest{
		Prompt:     "hello world\nsecond line",
		Model:      "gpt-5.5",
		ProviderID: OpenAIProviderID,
		TurnID:     "turn-abc",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if response.Message != "Go Codex exec stub received: hello world" {
		t.Fatalf("Message = %q", response.Message)
	}
	if len(response.Items) != 1 || response.Items[0].ID != "agent-message-abc" {
		t.Fatalf("Items = %#v", response.Items)
	}
	if response.ResponseID != "resp-turn-abc" {
		t.Fatalf("ResponseID = %q", response.ResponseID)
	}
	if response.Usage.InputTokens == 0 || response.Usage.OutputTokens == 0 {
		t.Fatalf("Usage = %#v", response.Usage)
	}
	if response.Model != "gpt-5.5" || response.ProviderID != OpenAIProviderID {
		t.Fatalf("response metadata = %#v", response)
	}
}

func TestUnavailableAgentRunnerNeverReturnsLocalStubSuccess(t *testing.T) {
	response, err := (&UnavailableAgentRunner{}).Run(context.Background(), &AgentRequest{Prompt: "hello"})
	if err == nil || response != nil || strings.Contains(err.Error(), "Go Codex exec stub received") {
		t.Fatalf("Run() response = %#v, error = %v", response, err)
	}
}

func TestLocalAgentRunnerRejectsEmptyPrompt(t *testing.T) {
	_, err := NewLocalAgentRunner().Run(context.Background(), &AgentRequest{Prompt: " \n\t"})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("Run error = %v, want prompt required", err)
	}
}

func TestLocalAgentRunnerHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewLocalAgentRunner().Run(ctx, &AgentRequest{Prompt: "hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestAgentItemMarshalToolCalls(t *testing.T) {
	item := &AgentItem{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-1",
		Arguments: `{"text":"hi"}`,
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"id":"call-1","type":"function_call","name":"echo","arguments":"{\"text\":\"hi\"}","call_id":"call-1"}` {
		t.Fatalf("json = %s", data)
	}

	search := &AgentItem{
		ID:        "search-1",
		Type:      "tool_search_call",
		Execution: "client",
		Search:    map[string]any{"query": "docs"},
	}
	data, err = json.Marshal(search)
	if err != nil {
		t.Fatalf("Marshal(search) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(search) error = %v", err)
	}
	if decoded["type"] != "tool_search_call" || decoded["call_id"] != "search-1" {
		t.Fatalf("search json = %s", data)
	}
	args, ok := decoded["arguments"].(map[string]any)
	if !ok || args["query"] != "docs" {
		t.Fatalf("search args = %#v", decoded["arguments"])
	}

	searchWithoutArguments := &AgentItem{Type: "tool_search_call"}
	data, err = json.Marshal(searchWithoutArguments)
	if err != nil {
		t.Fatalf("Marshal(searchWithoutArguments) error = %v", err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(searchWithoutArguments) error = %v", err)
	}
	for _, key := range []string{"call_id", "arguments"} {
		if _, ok := decoded[key]; !ok || decoded[key] != nil {
			t.Fatalf("%s = %#v in %s", key, decoded[key], data)
		}
	}
}

func TestAgentItemMarshalAgentMessageAsResponsesMessage(t *testing.T) {
	item := &AgentItem{ID: "msg-1", Type: "agent_message", Text: "hello"}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["type"] != "message" || decoded["role"] != "assistant" {
		t.Fatalf("message json = %s", data)
	}
	content, ok := decoded["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v", decoded["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "output_text" || block["text"] != "hello" {
		t.Fatalf("block = %#v", content[0])
	}
}

func TestAgentItemMarshalPreservesCommentaryPhase(t *testing.T) {
	data, err := json.Marshal(&AgentItem{ID: "msg-commentary", Type: "agent_message", Text: "I will check the weather.", Data: map[string]any{"phase": "commentary"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"phase":"commentary"`) {
		t.Fatalf("json = %s", data)
	}
}

func TestAgentItemMarshalImageGenerationCall(t *testing.T) {
	item := &AgentItem{
		ID:     "ig_123",
		Type:   "image_generation_call",
		Status: "completed",
		Text:   "Zm9v",
		Data: map[string]any{
			"revisedPrompt": "A small blue square",
		},
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["type"] != "image_generation_call" || decoded["id"] != "ig_123" || decoded["status"] != "completed" || decoded["result"] != "Zm9v" || decoded["revised_prompt"] != "A small blue square" {
		t.Fatalf("image json = %s", data)
	}
}
