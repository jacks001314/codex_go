package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func toolResultDispatchRequest(t *testing.T, apiRequest *responsesAgentRequest) *AgentRequest {
	t.Helper()
	return &AgentRequest{
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ToolResults: []ToolResultAttachment{{
			ItemIndex: 0,
			CallIndex: 1,
			Body: mustToolResultBody(t, ToolResultBodySource{Content: []any{
				map[string]any{"type": "text", "text": "connector payload"},
			}}),
		}},
	}
}

func toolResultCarrierInput() []any {
	return []any{
		map[string]any{
			"type":    "function_call_output",
			"call_id": "call-1",
			"output":  "ok",
			internalChatMessageMetadataPassthroughField: map[string]any{
				executedToolCallsField: []any{
					map[string]any{"name": "first", "arguments": map[string]any{"a": float64(1)}},
					map[string]any{"name": "second", "arguments": map[string]any{"b": float64(2)}},
				},
			},
		},
		map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
		},
	}
}

func TestResponsesHTTPRequestAttachesToolResultsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"}})
	apiRequest := &responsesAgentRequest{Model: "gpt-test", Input: toolResultCarrierInput()}
	request := toolResultDispatchRequest(t, apiRequest)

	httpRequest, err := runner.newResponsesHTTPRequestWithToolResults(context.Background(), request, apiRequest, "application/json", true)
	if err != nil {
		t.Fatalf("newResponsesHTTPRequestWithToolResults error = %v", err)
	}
	enriched, err := io.ReadAll(httpRequest.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}
	var decoded struct {
		Input []struct {
			Metadata struct {
				Calls []map[string]any `json:"executed_tool_calls"`
			} `json:"internal_chat_message_metadata_passthrough"`
		} `json:"input"`
	}
	if err := json.Unmarshal(enriched, &decoded); err != nil {
		t.Fatalf("enriched body is not valid JSON: %v", err)
	}
	calls := decoded.Input[0].Metadata.Calls
	if len(calls) != 2 {
		t.Fatalf("executed tool calls = %d, want 2", len(calls))
	}
	if _, present := calls[0]["tool_result"]; present {
		t.Fatalf("unselected call row was enriched: %v", calls[0])
	}
	result, present := calls[1]["tool_result"].(map[string]any)
	if !present {
		t.Fatalf("selected call row lacks tool_result: %v", calls[1])
	}
	if content, ok := result["content"].([]any); !ok || len(content) != 1 ||
		content[0].(map[string]any)["text"] != "connector payload" {
		t.Fatalf("tool_result content = %v", result["content"])
	}

	plainRequest, err := runner.newResponsesHTTPRequestWithToolResults(context.Background(), request, apiRequest, "application/json", false)
	if err != nil {
		t.Fatalf("newResponsesHTTPRequestWithToolResults error = %v", err)
	}
	plain, err := io.ReadAll(plainRequest.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}
	if strings.Contains(string(plain), "tool_result") {
		t.Fatalf("redirect fallback body kept the connector result: %s", plain)
	}
}

func TestResponsesHTTPRedirectReissuesOriginalBodyWithoutToolResultsLikeRust(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts []string
		moved    int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if r.URL.Path == "/v1/responses-moved" {
			moved++
		} else {
			attempts = append(attempts, string(body))
		}
		mu.Unlock()
		if r.URL.Path == "/v1/responses" && len(attempts) == 1 {
			w.Header().Set("Location", "/v1/responses-moved")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:   &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		HTTPClient: server.Client(),
	})
	apiRequest := &responsesAgentRequest{Model: "gpt-test", Input: toolResultCarrierInput()}
	request := toolResultDispatchRequest(t, apiRequest)
	response, err := runner.doResponsesHTTPRequestWithRetry(context.Background(), request, apiRequest, "application/json", 3)
	if err != nil {
		t.Fatalf("doResponsesHTTPRequestWithRetry error = %v", err)
	}
	defer response.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if moved != 0 {
		t.Fatalf("redirect destination saw %d requests, want none", moved)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want the enriched attempt and one original-body retry", len(attempts))
	}
	if !strings.Contains(attempts[0], `"tool_result"`) {
		t.Fatalf("first attempt did not carry the connector result: %s", attempts[0])
	}
	if strings.Contains(attempts[1], "tool_result") {
		t.Fatalf("redirect fallback forwarded the connector result: %s", attempts[1])
	}
}

func TestWebsocketToolResultsFrameBypassesPayloadTracingLikeRust(t *testing.T) {
	apiRequest := &responsesAgentRequest{Model: "gpt-test", Input: toolResultCarrierInput()}
	request := toolResultDispatchRequest(t, apiRequest)
	payload := websocketResponseCreatePayload(apiRequest, "", nil, nil)
	message := marshalResponsesMessage(payload)
	if strings.Contains(string(message), "tool_result") {
		t.Fatalf("canonical payload carried connector results: %s", message)
	}
	enriched, ok := WithToolResults(string(message), request.ToolResults)
	if !ok {
		t.Fatal("WithToolResults reported no enrichment for the websocket frame")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(enriched), &decoded); err != nil {
		t.Fatalf("enriched websocket frame is not valid JSON: %v", err)
	}
	if decoded["type"] != "response.create" {
		t.Fatalf("enriched frame type = %v", decoded["type"])
	}
}
