package model

import (
	"encoding/json"
	"testing"
)

func TestResponsesIncludeOutputTokenReplayOnlyForOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		enabled  bool
		want     []string
	}{
		{name: "disabled", provider: OpenAIProviderName, enabled: false, want: []string{"reasoning.encrypted_content"}},
		{name: "enabled openai", provider: OpenAIProviderName, enabled: true, want: []string{"reasoning.encrypted_content", "output.encrypted_content"}},
		{name: "enabled other provider", provider: "mock", enabled: true, want: []string{"reasoning.encrypted_content"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &ResponsesAgentRunner{
				Provider:                 &APIProvider{Name: testCase.provider},
				OutputTokenReplayEnabled: testCase.enabled,
			}
			got := runner.responsesInclude()
			if len(got) != len(testCase.want) {
				t.Fatalf("responsesInclude() = %#v, want %#v", got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("responsesInclude() = %#v, want %#v", got, testCase.want)
				}
			}
		})
	}
}

func TestOutputTokenReplayPreservesMessageAndCallMetadata(t *testing.T) {
	encrypted := "encrypted-answer"
	status := "completed"
	annotations := []any{map[string]any{"type": "url_citation", "url": "https://example.test"}}
	logprobs := []any{map[string]any{"token": "done", "logprob": -0.1}}
	message, err := agentItemFromResponseOutput(&responsesAgentOutputItem{
		ID:               "msg-1",
		Type:             "message",
		Role:             "assistant",
		Status:           status,
		EncryptedContent: &encrypted,
		Content: []responsesAgentContentBlock{{
			Type: "output_text", Text: "done", Annotations: &annotations, Logprobs: &logprobs,
		}},
	}, nil, 0, false)
	if err != nil {
		t.Fatalf("message agentItemFromResponseOutput() error = %v", err)
	}
	assertOutputReplayFields(t, message, encrypted, status, annotations, logprobs)

	call, ok := toolCallAgentItem(&responsesAgentOutputItem{
		ID:               "call-1",
		Type:             "function_call",
		Name:             "echo",
		CallID:           "call-1",
		Arguments:        `{"text":"hi"}`,
		Status:           status,
		EncryptedContent: &encrypted,
	}, 0)
	if !ok {
		t.Fatal("function_call was not converted")
	}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal(call) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(call) error = %v", err)
	}
	if decoded["encrypted_content"] != encrypted || decoded["status"] != status {
		t.Fatalf("call metadata = %s", encoded)
	}
}

func TestOutputTokenReplayCiphertextIsClearedOnlyInNonOpenAIRequestCopies(t *testing.T) {
	encrypted := "sealed"
	message := &AgentItem{
		ID: "msg-1", Type: "agent_message", Text: "done",
		Data: map[string]any{
			"encrypted_content": encrypted,
			"status":            "completed",
			"outputContent": []responsesAgentContentBlock{{
				Type: "output_text", Text: "done", Annotations: &[]any{},
			}},
		},
	}
	call := &AgentItem{
		ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`,
		Data: map[string]any{"encrypted_content": encrypted},
	}
	reasoning := &AgentItem{
		ID: "reasoning-1", Type: "reasoning",
		Data: map[string]any{"encrypted_content": encrypted},
	}
	request := &AgentRequest{InputItems: []any{message, call, reasoning, map[string]any{
		"type": "message", "role": "assistant", "encrypted_content": encrypted,
		"content": []any{map[string]any{"type": "output_text", "text": "map"}},
	}}}

	openAI := responsesInputItemsForProvider(request, OpenAIProviderName)
	if got := outputReplayEncryptedContent(t, openAI[0]); got != encrypted {
		t.Fatalf("OpenAI message ciphertext = %q", got)
	}
	nonOpenAI := responsesInputItemsForProvider(request, "mock")
	for i := 0; i < 2; i++ {
		if got := outputReplayEncryptedContent(t, nonOpenAI[i]); got != "" {
			t.Fatalf("non-OpenAI item %d retained ciphertext %q", i, got)
		}
	}
	if got := outputReplayEncryptedContent(t, nonOpenAI[2]); got != encrypted {
		t.Fatalf("non-OpenAI reasoning ciphertext = %q, want preserved", got)
	}
	if got := outputReplayEncryptedContent(t, nonOpenAI[3]); got != "" {
		t.Fatalf("non-OpenAI map retained ciphertext %q", got)
	}
	if message.Data["encrypted_content"] != encrypted {
		t.Fatal("request-copy filtering mutated persisted message metadata")
	}
}

func assertOutputReplayFields(t *testing.T, item *AgentItem, encrypted string, status string, annotations []any, logprobs []any) {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal(item) error = %v", err)
	}
	var decoded struct {
		EncryptedContent string `json:"encrypted_content"`
		Status           string `json:"status"`
		Content          []struct {
			Annotations []any `json:"annotations"`
			Logprobs    []any `json:"logprobs"`
		} `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(item) error = %v", err)
	}
	if decoded.EncryptedContent != encrypted || decoded.Status != status {
		t.Fatalf("message metadata = %s", encoded)
	}
	if len(decoded.Content) != 1 || len(decoded.Content[0].Annotations) != len(annotations) || len(decoded.Content[0].Logprobs) != len(logprobs) {
		t.Fatalf("message content metadata = %s", encoded)
	}
}

func outputReplayEncryptedContent(t *testing.T, value any) string {
	t.Helper()
	switch item := value.(type) {
	case *AgentItem:
		return stringValueFromAgentItemMap(item.Data, "encrypted_content", "encryptedContent")
	case AgentItem:
		return stringValueFromAgentItemMap(item.Data, "encrypted_content", "encryptedContent")
	case map[string]any:
		if encrypted, ok := item["encrypted_content"].(string); ok {
			return encrypted
		}
	}
	return ""
}
