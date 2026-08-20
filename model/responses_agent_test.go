package model

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/auth"
	"codex_go/codexapi"

	"github.com/klauspost/compress/zstd"
)

func TestResponsesAgentRunnerPostsResponsesRequest(t *testing.T) {
	var recordedPath string
	var recordedAuth string
	var recordedProviderHeader string
	var recordedTimingHeader string
	var recordedBetaFeaturesHeader string
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedPath = r.URL.String()
		recordedAuth = r.Header.Get("Authorization")
		recordedProviderHeader = r.Header.Get("x-provider-test")
		recordedTimingHeader = r.Header.Get(responsesIncludeTimingMetricsHeader)
		recordedBetaFeaturesHeader = r.Header.Get(codexapi.ClientCodexBetaFeaturesHeader)
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(responsesRequestIDHeader, "req-1")
		w.Header().Set(responsesOpenAIModelHeader, "gpt-server")
		w.Header().Set(responsesCodexTurnStateHeader, "turn-state-1")
		w.Header().Set(responsesModelsETagHeader, `"models-etag-1"`)
		w.Header().Set(responsesReasoningHeader, "true")
		w.Header().Set("x-codex-primary-used-percent", "12.5")
		_, _ = w.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from api"}]}],
			"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":12,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":4},"output_tokens_details":{"reasoning_tokens":1},"codex_rollout_budget_units":2.5}
		}`))
	}))
	defer server.Close()

	provider := &APIProvider{
		Name:        OpenAIProviderName,
		BaseURL:     server.URL + "/v1",
		QueryParams: map[string]string{"api-version": "2026-06-30"},
		Headers:     http.Header{"x-provider-test": []string{"provider"}},
	}
	authHeaders := BearerAuthHeaders("sk-test", "", false)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: provider,
		Auth:     &authHeaders,
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:                       "gpt-test",
			SupportsReasoningSummaries: true,
			DefaultReasoningLevel:      "medium",
			DefaultReasoningSummary:    "auto",
			SupportVerbosity:           true,
			DefaultVerbosity:           "low",
		}}}),
		ProviderID: OpenAIProviderID,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{
		Prompt: "say hello",
		InputItems: []any{map[string]any{
			"type":    "function_call_output",
			"call_id": "call-1",
			"output":  "done",
		}},
		Tools: []any{map[string]any{
			"type":        "function",
			"name":        "echo",
			"description": "Echo text",
			"parameters":  map[string]any{"type": "object"},
			"strict":      false,
		}},
		Model:                        "gpt-test",
		ProviderID:                   OpenAIProviderID,
		ParallelToolCalls:            true,
		ReasoningEffort:              "ultra",
		ReasoningSummary:             "concise",
		ConcurrentReasoningSummaries: true,
		ModelVerbosity:               "high",
		IncludeTimingMetrics:         true,
		BetaFeaturesHeader:           "memories,remote_compaction_v2",
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedPath != "/v1/responses?api-version=2026-06-30" {
		t.Fatalf("path = %q", recordedPath)
	}
	if recordedAuth != "Bearer sk-test" || recordedProviderHeader != "provider" {
		t.Fatalf("headers auth=%q provider=%q", recordedAuth, recordedProviderHeader)
	}
	if recordedTimingHeader != "true" {
		t.Fatalf("timing metrics header = %q", recordedTimingHeader)
	}
	if recordedBetaFeaturesHeader != "memories,remote_compaction_v2" {
		t.Fatalf("beta features header = %q", recordedBetaFeaturesHeader)
	}
	if recordedBody["model"] != "gpt-test" || recordedBody["stream"] != false || recordedBody["store"] != false {
		t.Fatalf("body = %#v", recordedBody)
	}
	if recordedBody["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %#v", recordedBody["tool_choice"])
	}
	if recordedBody["parallel_tool_calls"] != true {
		t.Fatalf("parallel_tool_calls = %#v", recordedBody["parallel_tool_calls"])
	}
	reasoning, ok := recordedBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "max" || reasoning["summary"] != "concise" {
		t.Fatalf("reasoning = %#v", recordedBody["reasoning"])
	}
	streamOptions, ok := recordedBody["stream_options"].(map[string]any)
	if !ok || streamOptions["reasoning_summary_delivery"] != "sequential_cutoff" {
		t.Fatalf("stream_options = %#v", recordedBody["stream_options"])
	}
	include, ok := recordedBody["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", recordedBody["include"])
	}
	text, ok := recordedBody["text"].(map[string]any)
	if !ok || text["verbosity"] != "high" {
		t.Fatalf("text = %#v", recordedBody["text"])
	}
	inputs, ok := recordedBody["input"].([]any)
	if !ok || len(inputs) != 2 {
		t.Fatalf("input = %#v", recordedBody["input"])
	}
	toolOutput, ok := inputs[0].(map[string]any)
	if !ok || toolOutput["type"] != "function_call_output" || toolOutput["output"] != "done" {
		t.Fatalf("tool output input = %#v", inputs[0])
	}
	promptInput, ok := inputs[1].(map[string]any)
	if !ok || promptInput["type"] != "message" || promptInput["role"] != "user" {
		t.Fatalf("prompt input = %#v", inputs[1])
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", recordedBody["tools"])
	}
	toolDef, ok := tools[0].(map[string]any)
	if !ok || toolDef["type"] != "function" || toolDef["name"] != "echo" {
		t.Fatalf("tool def = %#v", tools[0])
	}
	if response.Message != "hello from api" || response.Model != "gpt-test" || response.ProviderID != OpenAIProviderID {
		t.Fatalf("response = %#v", response)
	}
	if response.Usage.InputTokens != 7 || response.Usage.CachedInputTokens != 2 || response.Usage.CacheWriteInputTokens != 4 || response.Usage.OutputTokens != 3 || response.Usage.ReasoningOutputTokens != 1 || response.Usage.TotalTokens != 12 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Usage.CodexRolloutBudgetUnits != "2.5" {
		t.Fatalf("codex rollout budget units = %q, want 2.5", response.Usage.CodexRolloutBudgetUnits)
	}
	if response.RequestID != "req-1" || response.ServerModel != "gpt-server" || response.TurnState != "turn-state-1" || response.ModelsETag != `"models-etag-1"` {
		t.Fatalf("response metadata = %#v", response)
	}
	if response.Headers["x-request-id"] != "req-1" || len(response.RateLimits) != 1 || response.RateLimits[0].Primary == nil || response.RateLimits[0].Primary.UsedPercent != 12.5 {
		t.Fatalf("response headers/rate limits = %#v / %#v", response.Headers, response.RateLimits)
	}
	if response.ReasoningIncluded == nil || !*response.ReasoningIncluded {
		t.Fatalf("reasoning included = %#v", response.ReasoningIncluded)
	}
}

func TestResponsesAgentRunnerConcurrentReasoningSummaryStreamOptionsOmittedForNoneSummary(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:                       "gpt-test",
			SupportsReasoningSummaries: true,
			DefaultReasoningLevel:      "medium",
			DefaultReasoningSummary:    "auto",
		}}}),
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:                       "hello",
		Model:                        "gpt-test",
		ConcurrentReasoningSummaries: true,
		ReasoningSummary:             "none",
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if _, ok := recordedBody["stream_options"]; ok {
		t.Fatalf("stream_options present for summary=none: %#v", recordedBody["stream_options"])
	}
}

func TestResponsesAgentRunnerConcurrentReasoningSummaryStreamOptionsOmittedForNonOpenAIProvider(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: "Azure OpenAI", BaseURL: server.URL + "/v1"},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:                       "gpt-test",
			SupportsReasoningSummaries: true,
			DefaultReasoningLevel:      "medium",
			DefaultReasoningSummary:    "auto",
		}}}),
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:                       "hello",
		Model:                        "gpt-test",
		ConcurrentReasoningSummaries: true,
		ReasoningSummary:             "concise",
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if _, ok := recordedBody["stream_options"]; ok {
		t.Fatalf("stream_options present for non-OpenAI provider: %#v", recordedBody["stream_options"])
	}
}

func TestResponsesAgentRunnerPrewarmUsesWebSocketGenerateFalse(t *testing.T) {
	var received map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, data, err := conn.Read(request.Context())
		if err != nil {
			t.Errorf("Read() error = %v", err)
			return
		}
		if err := json.Unmarshal(data, &received); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
			return
		}
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"warm-1"}}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"warm-1"}}`))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL, Headers: http.Header{"x-provider-test": []string{"present"}}},
		Auth:     &AuthHeaders{Headers: http.Header{"Authorization": []string{"Bearer secret"}}}, SupportsWebsockets: true, WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test", ThreadID: "guardian-thread", ClientMetadata: map[string]string{"x-openai-subagent": "guardian"}})
	if err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if response == nil || response.ResponseID != "warm-1" {
		t.Fatalf("response = %#v", response)
	}
	if received["type"] != "response.create" || received["generate"] != false || received["model"] != "gpt-test" {
		t.Fatalf("payload = %#v", received)
	}
	metadata, ok := received["client_metadata"].(map[string]any)
	if !ok || metadata["x-openai-subagent"] != "guardian" {
		t.Fatalf("metadata = %#v", received["client_metadata"])
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestResponsesAgentRunnerPrewarmNoopsWithoutWebSocketSupport(t *testing.T) {
	runner := NewResponsesAgentRunner(nil)
	response, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test"})
	if err != nil || response != nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestResponsesAgentRunnerPrewarmReportsResponseIncomplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.incomplete","response":{"id":"warm-1","incomplete_details":{"reason":"max_output_tokens"}}}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	_, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test"})
	if err == nil || err.Error() != "Incomplete response returned, reason: max_output_tokens" {
		t.Fatalf("Prewarm() error = %v", err)
	}
}

func TestResponsesAgentRunnerRunWebSocketUsesPreviousResponseID(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, data, _ := conn.Read(request.Context())
		_ = json.Unmarshal(data, &received)
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.output_text.delta","delta":"{\"outcome\":"}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.output_text.delta","delta":"\"allow\"}"}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"review-1"}}`))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "review", PreviousResponseID: "warm-1"})
	if err != nil || response == nil || response.ResponseID != "review-1" || response.Message != `{"outcome":"allow"}` {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if received["previous_response_id"] != "warm-1" {
		t.Fatalf("payload=%#v", received)
	}
	if _, ok := received["generate"]; ok {
		t.Fatalf("normal request should omit generate: %#v", received)
	}
}

func TestResponsesAgentRunnerRunWebSocketParsesFullResponsesEventModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		events := []string{
			`{"type":"response.created","response":{"id":"resp-ws-full","model":"gpt-test"}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"call-item","call_id":"call-1","delta":"{\"path\":"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"call-item","call_id":"call-1","delta":"\"plan.md\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"call-item","type":"function_call","name":"read_file","call_id":"call-1","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-ws-full","model":"gpt-test","output":[],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
		}
		for _, event := range events {
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(event))
		}
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "read plan"})
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if response.ResponseID != "resp-ws-full" || response.Usage.InputTokens != 11 || response.Usage.OutputTokens != 7 || response.Usage.TotalTokens != 18 {
		t.Fatalf("response=%#v", response)
	}
	if len(response.Items) != 1 || response.Items[0].Type != "function_call" || response.Items[0].Name != "read_file" || response.Items[0].Arguments != `{"path":"plan.md"}` {
		t.Fatalf("items=%#v", response.Items)
	}
}

func TestResponsesAgentRunnerRunWebSocketReadsModelsETagFromMetadataEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		events := []string{
			`{"type":"codex.response.metadata","headers":{"x-models-etag":"etag-ws-123"}}`,
			`{"type":"response.created","response":{"id":"resp-ws-etag"}}`,
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"id":"resp-ws-etag","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		}
		for _, event := range events {
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(event))
		}
	}))
	defer server.Close()

	var events []ResponsesStreamEvent
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: server.URL},
		SupportsWebsockets: true,
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event != nil {
				events = append(events, *event)
			}
		},
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "read etag"})
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	etag := firstEventByKind(events, ResponsesStreamEventModelsETag)
	if etag == nil || etag.ModelsETag != "etag-ws-123" {
		t.Fatalf("models etag event = %#v", etag)
	}
}

func TestResponsesAgentRunnerRunWebSocketRecoversDeclaredCustomToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		events := []string{
			`{"type":"response.created","response":{"id":"resp-ws-custom"}}`,
			`{"type":"response.custom_tool_call_input.delta","item_id":"ctc-1","call_id":"call-1","delta":"text(\"OK\")"}`,
			`{"type":"response.output_item.done","item":{"id":"ctc-1","type":"function_call","name":"exec","call_id":"call-1","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-ws-custom","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		}
		for _, event := range events {
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(event))
		}
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{
		Model:  "gpt-test",
		Prompt: "run exec",
		Tools:  []any{map[string]any{"type": "custom", "name": "exec"}},
	})
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if len(response.Items) != 1 || response.Items[0].Type != "custom_tool_call" || response.Items[0].Name != "exec" || response.Items[0].Input != `text("OK")` {
		t.Fatalf("items=%#v", response.Items)
	}
}

func TestResponsesAgentRunnerNonStreamingRecoversDeclaredCustomToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp-custom",
			"model":"gpt-test",
			"output":[{"id":"ctc-1","type":"function_call","name":"exec","call_id":"call-1","arguments":"text(\"OK\")"}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}})
	response, err := runner.Run(context.Background(), &AgentRequest{
		Model:  "gpt-test",
		Prompt: "run exec",
		Tools:  []any{map[string]any{"type": "custom", "name": "exec"}},
	})
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if len(response.Items) != 1 || response.Items[0].Type != "custom_tool_call" || response.Items[0].Name != "exec" || response.Items[0].Input != `text("OK")` || response.Items[0].Arguments != "" {
		t.Fatalf("items=%#v", response.Items)
	}
}

func TestResponsesAgentRunnerRunWebSocketReusesConnectionWithinTurn(t *testing.T) {
	connections := 0
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connections++
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for i := 1; i <= 2; i++ {
			if _, _, err := conn.Read(request.Context()); err != nil {
				return
			}
			requests++
			response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","output":[{"id":"msg-%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok-%d"}]}]}}`, i, i, i)
			_ = conn.Write(request.Context(), websocket.MessageText, []byte(response))
		}
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	for i := 1; i <= 2; i++ {
		response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello", ThreadID: "thread-1", TurnID: "turn-1"})
		if err != nil || response == nil || response.Message != fmt.Sprintf("ok-%d", i) {
			t.Fatalf("request %d response=%#v err=%v", i, response, err)
		}
	}
	if connections != 1 || requests != 2 {
		t.Fatalf("connections=%d requests=%d", connections, requests)
	}
}

func TestResponsesAgentRunnerRunWebSocketIsolatesTurns(t *testing.T) {
	connections := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connections++
		connectionID := connections
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","output":[{"id":"msg-%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"connection-%d"}]}]}}`, connectionID, connectionID, connectionID)
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(response))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	for i, turnID := range []string{"turn-1", "turn-2"} {
		response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello", ThreadID: "thread-1", TurnID: turnID})
		if err != nil || response == nil || response.Message != fmt.Sprintf("connection-%d", i+1) {
			t.Fatalf("turn %s response=%#v err=%v", turnID, response, err)
		}
	}
	if connections != 2 {
		t.Fatalf("connections=%d", connections)
	}
}

func TestResponsesAgentRunnerRunWebSocketReconnectsClosedReusedConnection(t *testing.T) {
	connections := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connections++
		connectionID := connections
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		_, _, _ = conn.Read(request.Context())
		response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","output":[{"id":"msg-%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok-%d"}]}]}}`, connectionID, connectionID, connectionID)
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(response))
		_ = conn.Close(websocket.StatusNormalClosure, "rotate")
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	for i := 1; i <= 2; i++ {
		response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello", ThreadID: "thread-1", TurnID: "turn-1"})
		if err != nil || response == nil || response.Message != fmt.Sprintf("ok-%d", i) {
			t.Fatalf("request %d response=%#v err=%v", i, response, err)
		}
	}
	if connections != 2 {
		t.Fatalf("connections=%d", connections)
	}
}

func TestResponsesAgentRunnerWebSocketRetryExhaustionPermanentlyFallsBackToHTTP(t *testing.T) {
	websocketConnections := 0
	httpRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			websocketConnections++
			conn, err := websocket.Accept(w, request, nil)
			if err != nil {
				t.Errorf("Accept() error = %v", err)
				return
			}
			_, _, _ = conn.Read(request.Context())
			_ = conn.Close(websocket.StatusInternalError, "failed")
			return
		}
		httpRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"http-%d","output_text":"fallback-%d","output":[]}`, httpRequests, httpRequests)))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	for i := 1; i <= 2; i++ {
		response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello", ThreadID: "thread-1", TurnID: fmt.Sprintf("turn-%d", i)})
		if err != nil || response == nil || response.Message != fmt.Sprintf("fallback-%d", i) {
			t.Fatalf("request %d response=%#v err=%v", i, response, err)
		}
	}
	prewarm, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test", ClientMetadata: map[string]string{"x-openai-subagent": "guardian"}})
	if err != nil || prewarm != nil {
		t.Fatalf("prewarm=%#v err=%v", prewarm, err)
	}
	if websocketConnections != 2 || httpRequests != 2 {
		t.Fatalf("websocketConnections=%d httpRequests=%d", websocketConnections, httpRequests)
	}
}

func TestResponsesAgentRunnerWebSocket426FallbackMatchesRust(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		posts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"http-fallback","output_text":"ok","output":[]}`))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL}, SupportsWebsockets: true})
	prewarm, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test"})
	if err != nil || prewarm != nil || posts != 0 {
		t.Fatalf("prewarm=%#v posts=%d err=%v", prewarm, posts, err)
	}
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"})
	if err != nil || response == nil || response.ResponseID != "http-fallback" || posts != 1 {
		t.Fatalf("response=%#v posts=%d err=%v", response, posts, err)
	}
}

func TestResponsesAgentRunnerWebSocketRefreshesAuthOnceAfter401(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts == 1 {
			if got := request.Header.Get("Authorization"); got != "Bearer old-token" {
				t.Fatalf("first auth = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer new-token" {
			t.Fatalf("second auth = %q", got)
		}
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-refreshed","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`))
	}))
	defer server.Close()
	plan := "pro"
	snapshot := auth.FromChatGPTAuthTokens("old-token", "account-old", &plan)
	headers := BearerAuthHeaders("old-token", "account-old", false)
	refreshCalls := 0
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: server.URL},
		SupportsWebsockets: true,
		Auth:               &headers,
		AuthSnapshot:       &snapshot,
		ExternalAuthRefresh: func(ctx context.Context, request *ExternalAuthRefreshRequest) (*ExternalAuthRefreshResponse, error) {
			refreshCalls++
			return &ExternalAuthRefreshResponse{AccessToken: "new-token", ChatGPTAccountID: "account-new", ChatGPTPlanType: &plan}, nil
		},
	})
	response, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"})
	if err != nil || response == nil || response.ResponseID != "resp-refreshed" || response.Message != "ok" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if attempts != 2 || refreshCalls != 1 {
		t.Fatalf("attempts=%d refreshCalls=%d", attempts, refreshCalls)
	}
}

func TestResponsesAgentRunnerAddsAttestationHeaderWhenEnabled(t *testing.T) {
	provider := codexapi.NewCountingAttestationProvider(" attest-token ")
	var recordedAttestation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedAttestation = r.Header.Get(codexapi.AttestationHeader)
		_, _ = w.Write([]byte(`{"id":"resp-attest","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: server.URL + "/v1"},
		IncludeAttestation: true,
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:              "hello",
		Model:               "gpt-test",
		ThreadID:            "thread-attest",
		AttestationProvider: provider,
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedAttestation != "attest-token" {
		t.Fatalf("attestation header = %q", recordedAttestation)
	}
	if provider.Calls() != 1 {
		t.Fatalf("provider calls = %d", provider.Calls())
	}
}

func TestResponsesAgentRunnerSkipsAttestationHeaderWhenDisabled(t *testing.T) {
	provider := codexapi.NewCountingAttestationProvider("attest-token")
	var recordedAttestation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedAttestation = r.Header.Get(codexapi.AttestationHeader)
		_, _ = w.Write([]byte(`{"id":"resp-no-attest","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL + "/v1"}})
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:              "hello",
		Model:               "gpt-test",
		ThreadID:            "thread-attest",
		AttestationProvider: provider,
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedAttestation != "" {
		t.Fatalf("attestation header = %q", recordedAttestation)
	}
	if provider.Calls() != 0 {
		t.Fatalf("provider calls = %d", provider.Calls())
	}
}

func TestResponsesAgentRunnerTurnStatePersistsWithinTurnAndResetsAfter(t *testing.T) {
	attempts := 0
	var recordedTurnStates []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		recordedTurnStates = append(recordedTurnStates, r.Header.Get(responsesCodexTurnStateHeader))
		w.Header().Set("Content-Type", "application/json")
		switch attempts {
		case 1:
			w.Header().Set(responsesCodexTurnStateHeader, "turn-state-1")
		case 2:
			w.Header().Set(responsesCodexTurnStateHeader, "turn-state-2")
		}
		_, _ = w.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "first", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "same turn follow-up", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "same turn second follow-up", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("third Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "next turn", Model: "gpt-test", TurnID: "turn-2"}); err != nil {
		t.Fatalf("fourth Run error = %v", err)
	}
	want := []string{"", "turn-state-1", "turn-state-1", ""}
	if strings.Join(recordedTurnStates, ",") != strings.Join(want, ",") {
		t.Fatalf("turn state headers = %#v, want %#v", recordedTurnStates, want)
	}
}

func TestResponsesAgentRunnerStreamsTurnStatePersistsWithinTurnAndResetsAfter(t *testing.T) {
	attempts := 0
	var recordedTurnStates []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		recordedTurnStates = append(recordedTurnStates, r.Header.Get(responsesCodexTurnStateHeader))
		w.Header().Set("Content-Type", "text/event-stream")
		switch attempts {
		case 1:
			w.Header().Set(responsesCodexTurnStateHeader, "turn-state-1")
		case 2:
			w.Header().Set(responsesCodexTurnStateHeader, "turn-state-2")
		}
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-stream"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-stream","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-stream","delta":"done"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-stream","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-stream","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "first", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "same turn follow-up", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "same turn second follow-up", Model: "gpt-test", TurnID: "turn-1"}); err != nil {
		t.Fatalf("third Run error = %v", err)
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "next turn", Model: "gpt-test", TurnID: "turn-2"}); err != nil {
		t.Fatalf("fourth Run error = %v", err)
	}
	want := []string{"", "turn-state-1", "turn-state-1", ""}
	if strings.Join(recordedTurnStates, ",") != strings.Join(want, ",") {
		t.Fatalf("stream turn state headers = %#v, want %#v", recordedTurnStates, want)
	}
}

func TestResponsesAgentRunnerCompressesCodexBackendOpenAIRequests(t *testing.T) {
	var recordedEncoding string
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedEncoding = r.Header.Get("Content-Encoding")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll request body error = %v", err)
		}
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			t.Fatalf("NewReader error = %v", err)
		}
		defer decoder.Close()
		decoded, err := decoder.DecodeAll(body, nil)
		if err != nil {
			t.Fatalf("DecodeAll request body error = %v", err)
		}
		if err := json.Unmarshal(decoded, &recordedBody); err != nil {
			t.Fatalf("Unmarshal request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
		},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"access_token": "token"},
		},
		EnableRequestCompression: true,
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedEncoding != "zstd" {
		t.Fatalf("Content-Encoding = %q, want zstd", recordedEncoding)
	}
	if recordedBody["model"] != "gpt-test" {
		t.Fatalf("body = %#v", recordedBody)
	}
}

func TestResponsesAgentRunnerDoesNotCompressAPIKeyRequests(t *testing.T) {
	var recordedEncoding string
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedEncoding = r.Header.Get("Content-Encoding")
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
		},
		AuthSnapshot:             &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"},
		EnableRequestCompression: true,
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedEncoding != "" {
		t.Fatalf("Content-Encoding = %q, want empty", recordedEncoding)
	}
	if recordedBody["model"] != "gpt-test" {
		t.Fatalf("body = %#v", recordedBody)
	}
}

func TestResponsesAgentRunnerAddsHostedImageGenerationForCodexBackend(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"access_token": "token"},
		},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:            "gpt-test",
			InputModalities: []string{"text", "image"},
		}}}),
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "draw"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v", recordedBody["tools"])
	}
	if !hasResponseToolType(tools, "image_generation") {
		t.Fatalf("tools missing image_generation: %#v", tools)
	}
}

func TestResponsesAgentRunnerDoesNotAddHostedImageGenerationForFreePlan(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"access_token": "token",
				"plan_type":    "free",
			},
		},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:            "gpt-test",
			InputModalities: []string{"text", "image"},
		}}}),
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "draw"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	tools, _ := recordedBody["tools"].([]any)
	if hasResponseToolType(tools, "image_generation") {
		t.Fatalf("free-plan tools should not include image_generation: %#v", tools)
	}
}

func TestResponsesAgentRunnerAddsHostedImageGenerationForOpenAIAPIKey(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:     &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:            "gpt-test",
			InputModalities: []string{"text", "image"},
		}}}),
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "draw"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	tools, _ := recordedBody["tools"].([]any)
	if !hasResponseToolType(tools, "image_generation") {
		t.Fatalf("api key OpenAI tools missing image_generation: %#v", tools)
	}
}

func TestResponsesAgentRunnerDoesNotAddHostedImageGenerationForResponsesLite(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-lite","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"access_token": "token"},
		},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:             "gpt-lite",
			InputModalities:  []string{"text", "image"},
			UseResponsesLite: true,
		}}}),
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{
		Model:  "gpt-lite",
		Prompt: "draw",
		Tools:  []any{map[string]any{"type": "function", "name": "echo"}},
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if _, ok := recordedBody["tools"]; ok {
		t.Fatalf("responses lite should not include top-level tools: %#v", recordedBody["tools"])
	}
	inputs, ok := recordedBody["input"].([]any)
	if !ok || len(inputs) == 0 {
		t.Fatalf("input = %#v", recordedBody["input"])
	}
	additionalTools, ok := inputs[0].(map[string]any)
	if !ok || additionalTools["type"] != "additional_tools" {
		t.Fatalf("additional_tools = %#v", inputs[0])
	}
	tools, ok := additionalTools["tools"].([]any)
	if !ok {
		t.Fatalf("additional_tools.tools = %#v", additionalTools["tools"])
	}
	if hasResponseToolType(tools, "image_generation") {
		t.Fatalf("responses lite additional_tools should not include hosted image_generation: %#v", tools)
	}
}

func TestResponsesAgentRunnerDoesNotAddHostedImageGenerationWhenStandaloneNamespacePresent(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"access_token": "token"},
		},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:            "gpt-test",
			InputModalities: []string{"text", "image"},
		}}}),
	})

	standalone := map[string]any{
		"type":        "namespace",
		"name":        "image_gen",
		"description": "Tools in the image_gen namespace.",
		"tools": []any{map[string]any{
			"type": "function",
			"name": "imagegen",
		}},
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Model:  "gpt-test",
		Prompt: "draw",
		Tools:  []any{standalone},
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	tools, ok := recordedBody["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v", recordedBody["tools"])
	}
	if hasResponseToolType(tools, "image_generation") {
		t.Fatalf("standalone image_gen should suppress hosted image_generation: %#v", tools)
	}
	if !hasResponseNamespaceFunction(tools, "image_gen", "imagegen") {
		t.Fatalf("standalone namespace missing: %#v", tools)
	}
}

func TestResponsesAgentRunnerParsesImageGenerationCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"resp-image",
			"model":"gpt-test",
			"output":[{"id":"ig_123","type":"image_generation_call","status":"generating","revised_prompt":"A small blue square","result":"Zm9v"}]
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "draw"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "image_generation_call" || item.ID != "ig_123" || item.Status != "completed" || item.Text != "Zm9v" {
		t.Fatalf("image item = %#v", item)
	}
	if item.Data["status"] != "completed" || item.Data["revised_prompt"] != "A small blue square" || item.Data["result"] != "Zm9v" {
		t.Fatalf("image data = %#v", item.Data)
	}
}

func TestParseResponsesStreamNormalizesImageGenerationCallWithResult(t *testing.T) {
	var events []ResponsesStreamEvent
	response, err := parseResponsesStream(context.Background(), strings.NewReader(responsesSSE(
		`{"type":"response.created","response":{"id":"resp-image"}}`,
		`{"type":"response.output_item.added","item":{"id":"ig_123","type":"image_generation_call","status":"generating"}}`,
		`{"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","status":"generating","revised_prompt":"A small blue square","result":"Zm9v"}}`,
		`{"type":"response.completed","response":{"id":"resp-image","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)), &AgentRequest{Model: "gpt-test"}, OpenAIProviderName, func(event *ResponsesStreamEvent) {
		events = append(events, *event)
	})
	if err != nil {
		t.Fatalf("parseResponsesStream error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "image_generation_call" || item.Status != "completed" || item.Text != "Zm9v" {
		t.Fatalf("image item = %#v", item)
	}
	var done *ResponsesStreamEvent
	for i := range events {
		if events[i].Kind == ResponsesStreamEventOutputDone {
			done = &events[i]
			break
		}
	}
	if done == nil || done.Item == nil || done.Item.Status != "completed" || done.Item.Data["status"] != "completed" {
		t.Fatalf("output done event = %#v", done)
	}
}

func TestParseResponsesStreamRecordsImageGenerationFromCompletedOutput(t *testing.T) {
	response, err := parseResponsesStream(context.Background(), strings.NewReader(responsesSSE(
		`{"type":"response.created","response":{"id":"resp-image"}}`,
		`{"type":"response.completed","response":{"id":"resp-image","output":[{"id":"ig_123","type":"image_generation_call","status":"generating","revised_prompt":"A small blue square","result":"Zm9v"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)), &AgentRequest{Model: "gpt-test"}, OpenAIProviderName, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "image_generation_call" || item.ID != "ig_123" || item.Status != "completed" || item.Text != "Zm9v" {
		t.Fatalf("image item = %#v", item)
	}
	if item.Data["status"] != "completed" || item.Data["revised_prompt"] != "A small blue square" {
		t.Fatalf("image data = %#v", item.Data)
	}
}

func TestParseResponsesStreamDeduplicatesCompletedAssistantMessageWithStableID(t *testing.T) {
	response, err := parseResponsesStream(context.Background(), strings.NewReader(responsesSSE(
		`{"type":"response.created","response":{"id":"resp-1"}}`,
		`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-1","output":[{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)), &AgentRequest{Model: "gpt-test"}, OpenAIProviderName, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.ID != "msg_123" || item.Type != "agent_message" || item.Text != "done" {
		t.Fatalf("deduped item = %#v", item)
	}
}

func TestParseResponsesStreamDeduplicatesCompletedAssistantMessageWhenSummaryOmitsID(t *testing.T) {
	response, err := parseResponsesStream(context.Background(), strings.NewReader(responsesSSE(
		`{"type":"response.created","response":{"id":"resp-1"}}`,
		`{"type":"response.output_item.done","item":{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)), &AgentRequest{Model: "gpt-test"}, OpenAIProviderName, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.ID != "msg_123" || item.Type != "agent_message" || item.Text != "done" {
		t.Fatalf("deduped item = %#v", item)
	}
}

func TestParseResponsesStreamMergesToolCallsByCallIDAndPreservesNamespace(t *testing.T) {
	response, err := parseResponsesStream(context.Background(), strings.NewReader(responsesSSE(
		`{"type":"response.created","response":{"id":"resp-mcp"}}`,
		`{"type":"response.output_item.done","item":{"id":"fc-complete","type":"function_call","call_id":"call-mcp","namespace":"mcp__geogebra","name":"geogebra_create_circle","arguments":"{\"radius\":3}"}}`,
		`{"type":"response.completed","response":{"id":"resp-mcp","output":[{"id":"fc-summary","type":"function_call","call_id":"call-mcp","name":"geogebra_create_circle","arguments":"{\"radius\":3}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)), &AgentRequest{Model: "gpt-test"}, OpenAIProviderName, nil)
	if err != nil {
		t.Fatalf("parseResponsesStream error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.ID != "fc-complete" || item.CallID != "call-mcp" || item.Namespace != "mcp__geogebra" || item.Name != "geogebra_create_circle" {
		t.Fatalf("merged item = %#v", item)
	}
}

func TestResponsesAgentRunnerReasoningUsesModelDefaultsAndLiteContext(t *testing.T) {
	var recordedBody map[string]any
	var recordedLiteHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedLiteHeader = r.Header.Get(responsesLiteHeader)
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-lite",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:                       "gpt-lite",
			SupportsReasoningSummaries: true,
			DefaultReasoningLevel:      "high",
			DefaultReasoningSummary:    "auto",
			UseResponsesLite:           true,
		}}}),
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:       "hello",
		Instructions: "test instructions",
		InputItems: []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type":      "input_image",
				"image_url": "data:",
				"detail":    "high",
			}},
		}},
		Tools: []any{map[string]any{
			"type": "function",
			"name": "echo",
		}},
		Model:             "gpt-lite",
		ParallelToolCalls: true,
		ReasoningSummary:  "none",
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedLiteHeader != "true" {
		t.Fatalf("%s = %q", responsesLiteHeader, recordedLiteHeader)
	}
	if _, ok := recordedBody["instructions"]; ok {
		t.Fatalf("instructions present = %#v", recordedBody["instructions"])
	}
	if _, ok := recordedBody["tools"]; ok {
		t.Fatalf("tools present = %#v", recordedBody["tools"])
	}
	if recordedBody["parallel_tool_calls"] != false {
		t.Fatalf("parallel_tool_calls = %#v", recordedBody["parallel_tool_calls"])
	}
	inputs, ok := recordedBody["input"].([]any)
	if !ok || len(inputs) != 4 {
		t.Fatalf("input = %#v", recordedBody["input"])
	}
	additionalTools, ok := inputs[0].(map[string]any)
	if !ok || additionalTools["type"] != "additional_tools" || additionalTools["role"] != "developer" {
		t.Fatalf("additional_tools = %#v", inputs[0])
	}
	if tools, ok := additionalTools["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("additional_tools.tools = %#v", additionalTools["tools"])
	}
	developerMessage, ok := inputs[1].(map[string]any)
	if !ok || developerMessage["role"] != "developer" {
		t.Fatalf("developer message = %#v", inputs[1])
	}
	imageMessage, ok := inputs[2].(map[string]any)
	if !ok {
		t.Fatalf("image message = %#v", inputs[2])
	}
	content, ok := imageMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("image content = %#v", imageMessage["content"])
	}
	image, ok := content[0].(map[string]any)
	if !ok || image["type"] != "input_image" || image["detail"] != nil {
		t.Fatalf("lite image content = %#v", content[0])
	}
	reasoning, ok := recordedBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" || reasoning["context"] != "all_turns" {
		t.Fatalf("reasoning = %#v", recordedBody["reasoning"])
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("reasoning summary present = %#v", reasoning)
	}
}

func TestResponsesAgentRunnerAppendsPromptAfterHistoryInputItems(t *testing.T) {
	items := responsesInputItems(&AgentRequest{
		Prompt: "new question",
		InputItems: []any{map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": "old answer",
			}},
		}},
	})

	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	history := items[0].(map[string]any)
	prompt := items[1].(responsesInputMessage)
	if history["role"] != "assistant" || prompt.Role != "user" || prompt.Content[0].Text != "new question" {
		t.Fatalf("items order = %#v", items)
	}
}

func TestResponsesAgentRunnerBootstrapsAgentIdentity(t *testing.T) {
	home := t.TempDir()
	accessToken := fakeJWTModel(map[string]any{
		"chatgpt_account_id": "account-123",
		"chatgpt_user_id":    "user-123",
		"chatgpt_plan_type":  "pro",
	})
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token": accessToken,
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var responsesAuth string
	var responsesAccountID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Fatalf("register Authorization = %q", got)
			}
			writeJSONModel(t, w, map[string]string{"agent_runtime_id": "agent-runtime-1"})
		case "/v1/agent/agent-runtime-1/task/register":
			writeJSONModel(t, w, map[string]string{"task_id": "task-1"})
		case "/v1/responses":
			responsesAuth = r.Header.Get("Authorization")
			responsesAccountID = r.Header.Get("ChatGPT-Account-ID")
			writeJSONModel(t, w, map[string]any{"id": "resp-1", "output_text": "ok", "output": []any{}})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()
	bearer := BearerAuthHeaders(accessToken, "account-123", false)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
		},
		Auth:         &bearer,
		HTTPClient:   server.Client(),
		CodexHome:    home,
		AuthSnapshot: &snapshot,
		AgentIdentity: &AgentIdentityOptions{
			Enabled:        true,
			AuthAPIBaseURL: server.URL,
			SessionSource:  "cli",
		},
	})

	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response.Message != "ok" {
		t.Fatalf("response = %#v", response)
	}
	if !strings.HasPrefix(responsesAuth, "AgentAssertion ") {
		t.Fatalf("responses Authorization = %q", responsesAuth)
	}
	if responsesAccountID != "account-123" {
		t.Fatalf("ChatGPT-Account-ID = %q", responsesAccountID)
	}
	if runner.AuthSnapshot == nil || runner.AuthSnapshot.Mode() != "agent-identity" {
		t.Fatalf("runner auth snapshot = %#v", runner.AuthSnapshot)
	}
	if runner.AgentIdentityTelemetry == nil || runner.AgentIdentityTelemetry.AgentID != "agent-runtime-1" || runner.AgentIdentityTelemetry.TaskID != "task-1" {
		t.Fatalf("AgentIdentityTelemetry = %#v", runner.AgentIdentityTelemetry)
	}
}

func TestResponsesAgentRunnerSendsSignerPreparedBody(t *testing.T) {
	var recordedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll request body error = %v", err)
		}
		recordedBody = string(body)
		_, _ = w.Write([]byte(`{"id":"resp-1","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	authHeaders := AuthHeaders{
		SignRequest: func(_ context.Context, request *http.Request, _ []byte) (*SignedRequest, error) {
			body := []byte(`{"model":"signed-body"}`)
			request.Header.Set("X-Signed-Body", "true")
			return &SignedRequest{Body: body}, nil
		},
	}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
		},
		Auth: &authHeaders,
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedBody != `{"model":"signed-body"}` {
		t.Fatalf("body = %q", recordedBody)
	}
}

func fakeJWTModel(payload map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + "."
}

func writeJSONModel(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func TestResponsesAgentRunnerSendsOriginatorHeader(t *testing.T) {
	var recordedOriginator string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedOriginator = r.Header.Get("originator")
		_, _ = w.Write([]byte(`{"id":"resp-1","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
		},
	})

	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello", Originator: "codex_vscode"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedOriginator != "codex_vscode" {
		t.Fatalf("originator = %q", recordedOriginator)
	}
}

func TestResponsesAgentRunnerRoutingHintUsesCodexBackendAuth(t *testing.T) {
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:     &APIProvider{Name: OpenAIProviderName, BaseURL: "https://example.test/v1"},
		AuthSnapshot: &auth.AuthDotJSON{AuthMode: "chatgpt"},
	})
	request, err := runner.newResponsesHTTPRequest(context.Background(), &AgentRequest{}, &responsesAgentRequest{
		Model: "gpt-test", ServiceTier: "fast",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get(codexapi.ClientCodexRoutingHintHeader); got != "model=gpt-test;tier=fast" {
		t.Fatalf("routing hint = %q", got)
	}

	runner.ProviderUsesOwnCredentials = true
	request, err = runner.newResponsesHTTPRequest(context.Background(), &AgentRequest{}, &responsesAgentRequest{Model: "gpt-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get(codexapi.ClientCodexRoutingHintHeader); got != "" {
		t.Fatalf("provider-credential routing hint = %q, want empty", got)
	}
	runner.ProviderUsesOwnCredentials = false
	runner.AuthSnapshot = &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}
	request, err = runner.newResponsesHTTPRequest(context.Background(), &AgentRequest{}, &responsesAgentRequest{Model: "gpt-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get(codexapi.ClientCodexRoutingHintHeader); got != "" {
		t.Fatalf("API-key routing hint = %q, want empty", got)
	}
}

func TestResponsesAgentRunnerSendsStoreAndMetadataFieldsWithoutHTTPPreviousResponseID(t *testing.T) {
	var recordedBody map[string]any
	var recordedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-next","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:         "gpt-test",
			ServiceTiers: []string{"priority"},
		}}}),
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:             "hello",
		Model:              "gpt-test",
		Store:              true,
		PreviousResponseID: "resp-prev",
		ServiceTier:        "priority",
		PromptCacheKey:     "cache-key",
		ClientMetadata: map[string]string{
			"thread_id":             "thread-1",
			"x-codex-window-id":     "thread-1:1",
			"x-codex-turn-metadata": `{"thread_id":"thread-1","workspace_kind":"git"}`,
			"workspace_kind":        "git",
			"x-openai-subagent":     "review",
		},
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedBody["store"] != true || recordedBody["service_tier"] != "priority" || recordedBody["prompt_cache_key"] != "cache-key" {
		t.Fatalf("body = %#v", recordedBody)
	}
	if _, ok := recordedBody["previous_response_id"]; ok {
		t.Fatalf("HTTP Responses request unexpectedly included previous_response_id: %#v", recordedBody)
	}
	metadata, ok := recordedBody["client_metadata"].(map[string]any)
	if !ok || metadata["thread_id"] != "thread-1" {
		t.Fatalf("client_metadata = %#v", recordedBody["client_metadata"])
	}
	if recordedHeaders.Get("x-codex-window-id") != "thread-1:1" ||
		recordedHeaders.Get("x-codex-turn-metadata") != `{"thread_id":"thread-1","workspace_kind":"git"}` ||
		recordedHeaders.Get("x-openai-subagent") != "review" {
		t.Fatalf("compatibility metadata headers = %#v", recordedHeaders)
	}
	if recordedHeaders.Get("workspace_kind") != "" {
		t.Fatalf("extra metadata should not be sent as compatibility header: %#v", recordedHeaders)
	}
}

func TestResponsesAgentRunnerKeepsCodeModeNamesInClientMetadataButOmitsThemFromHeader(t *testing.T) {
	var recordedBody map[string]any
	var recordedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedHeader = r.Header.Get(codexapi.ClientCodexTurnMetadataHeader)
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-next","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()
	turnMetadata := `{"thread_id":"thread-1","code_mode_tool_names":{"view_image":{"name":"view_image","namespace":null}}}`
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL + "/v1"}})
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Prompt: "hello", Model: "gpt-test",
		ClientMetadata: map[string]string{codexapi.ClientCodexTurnMetadataHeader: turnMetadata},
	}); err != nil {
		t.Fatal(err)
	}
	clientMetadata := recordedBody["client_metadata"].(map[string]any)
	if clientMetadata[codexapi.ClientCodexTurnMetadataHeader] != turnMetadata {
		t.Fatalf("canonical client metadata = %#v", clientMetadata)
	}
	if strings.Contains(recordedHeader, codexapi.CodeModeToolNamesKey) || !strings.Contains(recordedHeader, `"thread_id":"thread-1"`) {
		t.Fatalf("compatibility header = %q", recordedHeader)
	}
}

func TestResponsesAgentRunnerPreparesInputItemIDsLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		store          bool
		itemIDsEnabled bool
		inputID        string
		wantID         bool
	}{
		{name: "default strips legacy id", inputID: "legacy-id"},
		{name: "default keeps responses prefixed id", inputID: "msg_server", wantID: true},
		{name: "item ids enabled keeps legacy id", inputID: "legacy-id", itemIDsEnabled: true, wantID: true},
		{name: "store keeps legacy id", inputID: "legacy-id", store: true, wantID: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recordedBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
					t.Fatalf("Decode request body error = %v", err)
				}
				_, _ = w.Write([]byte(`{"id":"resp-next","model":"gpt-test","output_text":"ok"}`))
			}))
			defer server.Close()

			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
				Provider: &APIProvider{BaseURL: server.URL + "/v1"},
			})
			_, err := runner.Run(context.Background(), &AgentRequest{
				Prompt:         "hello",
				Model:          "gpt-test",
				Store:          tc.store,
				ItemIDsEnabled: tc.itemIDsEnabled,
				InputItems: []any{&AgentItem{
					ID:   tc.inputID,
					Type: "agent_message",
					Text: "old",
				}},
			})
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			inputs, ok := recordedBody["input"].([]any)
			if !ok || len(inputs) == 0 {
				t.Fatalf("input = %#v", recordedBody["input"])
			}
			first, ok := inputs[0].(map[string]any)
			if !ok {
				t.Fatalf("first input = %#v", inputs[0])
			}
			_, hasID := first["id"]
			if hasID != tc.wantID {
				t.Fatalf("input id present = %v, want %v; input=%#v", hasID, tc.wantID, first)
			}
		})
	}
}

func TestResponsesAgentRunnerUsesRequestInstructions(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-instructions","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:       "hello",
		Model:        "gpt-test",
		Instructions: "project instructions",
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedBody["instructions"] != "project instructions" {
		t.Fatalf("instructions = %#v", recordedBody["instructions"])
	}
}

func TestResponsesAgentRunnerSendsOutputSchemaTextFormat(t *testing.T) {
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-schema","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:       "hello",
		Model:        "gpt-test",
		OutputSchema: schema,
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	text, ok := recordedBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v", recordedBody["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format = %#v", text["format"])
	}
	if format["name"] != "codex_output_schema" || format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("format = %#v", format)
	}
	schemaBody, ok := format["schema"].(map[string]any)
	if !ok || schemaBody["type"] != "object" || schemaBody["additionalProperties"] != false {
		t.Fatalf("schema = %#v", format["schema"])
	}
}

func TestResponsesAgentRunnerUsesOutputTextFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"resp-fallback","model":"gpt-test","output_text":"plain text"}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if response.Message != "plain text" || len(response.Items) != 1 || response.Items[0].ID != "resp-fallback" {
		t.Fatalf("response = %#v", response)
	}
}

func TestResponsesAgentRunnerKeepsToolCallItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp-tools",
			"model":"gpt-test",
			"output":[
				{"id":"call-1","type":"function_call","name":"echo","call_id":"call-1","arguments":"{\"text\":\"hi\"}"},
				{"id":"search-1","type":"tool_search_call","call_id":"search-1","execution":"client","arguments":{"query":"calendar"}}
			],
			"usage":{"input_tokens":1}
		}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "call tools", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if response.Message != "" || len(response.Items) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if response.Items[0].Type != "function_call" || response.Items[0].Name != "echo" || response.Items[0].Arguments != `{"text":"hi"}` {
		t.Fatalf("function item = %#v", response.Items[0])
	}
	if response.Items[1].Type != "tool_search_call" || response.Items[1].Search["query"] != "calendar" {
		t.Fatalf("search item = %#v", response.Items[1])
	}
}

func TestResponsesAgentRunnerRetriesTransientHTTPError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`temporarily unavailable`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-retry","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", RequestMaxRetries: 1},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 2 || response.Message != "ok" {
		t.Fatalf("attempts = %d response = %#v", attempts, response)
	}
}

func TestResponsesAgentRunnerDoesNotRetryTooManyRequests(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", RequestMaxRetries: 3},
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil {
		t.Fatal("Run returned nil error, want 429 failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v", err)
	}
}

func TestResponsesAgentRunnerRefreshesCommandAuthAfterUnauthorized(t *testing.T) {
	authConfig := commandAuthForTest("second-token")
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer first-token" {
				t.Fatalf("first auth = %q", got)
			}
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer second-token" {
			t.Fatalf("second auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"resp-auth","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	initialAuth := BearerAuthHeaders("first-token", "", false)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", Auth: authConfig, RequestMaxRetries: 1},
		Auth:     &initialAuth,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 2 || response.Message != "ok" {
		t.Fatalf("attempts = %d response = %#v", attempts, response)
	}
}

func TestResponsesAgentRunnerRefreshesChatGPTAuthAfterUnauthorized(t *testing.T) {
	home := t.TempDir()
	initialSnapshot := &auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "refresh-token",
			"account_id":    "account-1",
		},
	}
	if err := auth.NewStore(home).Save(*initialSnapshot); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			attempts++
			if attempts == 1 {
				if got := r.Header.Get("Authorization"); got != "Bearer old-access-token" {
					t.Fatalf("first auth = %q", got)
				}
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer new-access-token" {
				t.Fatalf("second auth = %q", got)
			}
			if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-1" {
				t.Fatalf("account id = %q", got)
			}
			_, _ = w.Write([]byte(`{"id":"resp-auth","model":"gpt-test","output_text":"ok"}`))
		case "/oauth/token":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode refresh body error = %v", err)
			}
			if body["refresh_token"] != "refresh-token" {
				t.Fatalf("refresh body = %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"new-access-token","refresh_token":"new-refresh-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	initialAuth := BearerAuthHeaders("old-access-token", "account-1", false)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:     &APIProvider{BaseURL: server.URL + "/v1", RequestMaxRetries: 1},
		Auth:         &initialAuth,
		HTTPClient:   server.Client(),
		CodexHome:    home,
		AuthSnapshot: initialSnapshot,
		AuthIssuer:   server.URL,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 2 || response.Message != "ok" {
		t.Fatalf("attempts = %d response = %#v", attempts, response)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load auth returned error: %v", err)
	}
	if loaded == nil || loaded.Tokens["access_token"] != "new-access-token" || loaded.Tokens["refresh_token"] != "new-refresh-token" {
		t.Fatalf("loaded auth = %#v", loaded)
	}
}

func TestResponsesAgentRunnerRefreshesExternalChatGPTAuthAfterUnauthorized(t *testing.T) {
	plan := "pro"
	initialSnapshot := auth.FromChatGPTAuthTokens("old-external-token", "account-old", &plan)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer old-external-token" {
				t.Fatalf("first auth = %q", got)
			}
			if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-old" {
				t.Fatalf("first account id = %q", got)
			}
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer new-external-token" {
			t.Fatalf("second auth = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-new" {
			t.Fatalf("second account id = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"resp-auth","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	refreshCalls := 0
	initialAuth := BearerAuthHeaders("old-external-token", "account-old", false)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:     &APIProvider{BaseURL: server.URL + "/v1", RequestMaxRetries: 1},
		Auth:         &initialAuth,
		HTTPClient:   server.Client(),
		AuthSnapshot: &initialSnapshot,
		ExternalAuthRefresh: func(ctx context.Context, request *ExternalAuthRefreshRequest) (*ExternalAuthRefreshResponse, error) {
			refreshCalls++
			if request == nil || request.Reason != ExternalAuthRefreshUnauthorized || request.PreviousAccountID != "account-old" {
				t.Fatalf("refresh request = %#v", request)
			}
			return &ExternalAuthRefreshResponse{AccessToken: "new-external-token", ChatGPTAccountID: "account-new", ChatGPTPlanType: &plan}, nil
		},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 2 || refreshCalls != 1 || response.Message != "ok" {
		t.Fatalf("attempts=%d refreshCalls=%d response=%#v", attempts, refreshCalls, response)
	}
	if runner.AuthSnapshot == nil || runner.AuthSnapshot.Mode() != "chatgptAuthTokens" || runner.AuthSnapshot.Tokens["account_id"] != "account-new" {
		t.Fatalf("auth snapshot = %#v", runner.AuthSnapshot)
	}
}

func TestResponsesAgentRunnerCachesCommandAuthUntilRefreshInterval(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"id":"resp-command-auth","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	authConfig := commandAuthSequenceForTest(t, []string{"first-token", "second-token"})
	authConfig.RefreshIntervalMS = 60_000
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", Auth: authConfig},
	})
	for i := 0; i < 2; i++ {
		if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"}); err != nil {
			t.Fatalf("Run %d error = %v", i+1, err)
		}
	}
	if strings.Join(authorizations, ",") != "Bearer first-token,Bearer first-token" {
		t.Fatalf("authorizations = %#v", authorizations)
	}
}

func TestResponsesAgentRunnerRefreshesStaleCommandAuthBeforeRequest(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"id":"resp-command-auth","model":"gpt-test","output_text":"ok"}`))
	}))
	defer server.Close()

	authConfig := commandAuthSequenceForTest(t, []string{"first-token", "second-token"})
	authConfig.RefreshIntervalMS = 1
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", Auth: authConfig},
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"}); err != nil {
		t.Fatalf("first Run error = %v", err)
	}
	runner.providerAuthFetchedAt = time.Now().Add(-time.Second)
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"}); err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	if strings.Join(authorizations, ",") != "Bearer first-token,Bearer second-token" {
		t.Fatalf("authorizations = %#v", authorizations)
	}
}

func commandAuthForTest(token string) *ProviderAuthInfo {
	if runtime.GOOS == "windows" {
		return &ProviderAuthInfo{
			Command:   "cmd",
			Args:      []string{"/D", "/Q", "/C", "echo " + token},
			TimeoutMS: 5000,
		}
	}
	return &ProviderAuthInfo{
		Command:   "sh",
		Args:      []string{"-c", "printf '%s\n' " + shellSingleQuote(token)},
		TimeoutMS: 5000,
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func commandAuthSequenceForTest(t *testing.T, tokens []string) *ProviderAuthInfo {
	t.Helper()
	if len(tokens) == 0 {
		t.Fatal("tokens must not be empty")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte(strings.Join(tokens, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile tokens returned error: %v", err)
	}
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "print-token.ps1")
		body := `$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
$indexPath = Join-Path $dir 'index.txt'
$i = 0
if (Test-Path -LiteralPath $indexPath) {
  $raw = Get-Content -LiteralPath $indexPath -Raw
  if ($raw.Trim().Length -gt 0) { $i = [int]$raw.Trim() }
}
$tokens = @(Get-Content -LiteralPath (Join-Path $dir 'tokens.txt'))
if ($i -ge $tokens.Length) { $i = $tokens.Length - 1 }
Write-Output $tokens[$i]
Set-Content -LiteralPath $indexPath -Value ($i + 1)
`
		if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile script returned error: %v", err)
		}
		return &ProviderAuthInfo{
			Command:   "powershell",
			Args:      []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script},
			TimeoutMS: 5000,
		}
	}
	script := filepath.Join(dir, "print-token.sh")
	body := `#!/bin/sh
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
idx=$(cat "$dir/index.txt" 2>/dev/null || echo 0)
token=$(sed -n "$((idx + 1))p" "$dir/tokens.txt")
if [ -z "$token" ]; then
  token=$(tail -n 1 "$dir/tokens.txt")
fi
printf '%s\n' "$token"
printf '%s\n' "$((idx + 1))" > "$dir/index.txt"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile script returned error: %v", err)
	}
	return &ProviderAuthInfo{
		Command:   script,
		TimeoutMS: 5000,
	}
}

func TestResponsesAgentRunnerStreamsResponsesSSE(t *testing.T) {
	var recordedAccept string
	var recordedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req-1")
		w.Header().Set("openai-model", "gpt-stream-header")
		w.Header().Set("x-codex-turn-state", "turn-state-1")
		w.Header().Set("x-models-etag", `"models-etag-1"`)
		w.Header().Set("x-reasoning-included", "true")
		w.Header().Set("x-codex-primary-used-percent", "12.5")
		w.Header().Set("x-codex-primary-window-minutes", "60")
		w.Header().Set("x-codex-primary-reset-at", "1704069000")
		w.Header().Set("x-codex-secondary-primary-used-percent", "80")
		w.Header().Set("x-codex-secondary-limit-name", "gpt-secondary")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.metadata","headers":{"openai-model":"gpt-metadata"}}`,
			`{"type":"response.metadata","metadata":{"model_reroute":{"from_model":"gpt-old","to_model":"gpt-new","reason":"high_risk_cyber_activity"},"model_verification":{"verifications":["trusted_access_for_cyber"]},"turn_moderation_metadata":{"flagged":true}}}`,
			`{"type":"response.metadata","metadata":{"type":"safety_buffering","use_cases":["cyber"],"reasons":["review"],"show_buffering_ui":true,"faster_model":"gpt-fast"}}`,
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"responsesapi.websocket_timing","timing_metrics":{"responses_duration_excl_engine_and_client_tool_time_ms":120,"engine_iapi_ttft_total_ms":310,"engine_service_ttft_total_ms":340}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","delta":"hello "}`,
			`{"type":"response.output_text.delta","delta":"world"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-1","name":"echo","arguments":"{\"text\":\"hi\"}"}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":4,"output_tokens":2,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":1},"total_tokens":6}}}`,
		)))
	}))
	defer server.Close()

	var events []ResponsesStreamEvent
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
		StreamHandler: func(event *ResponsesStreamEvent) {
			events = append(events, *event)
		},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test", PreviousResponseID: "resp-prev"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if recordedAccept != "text/event-stream" || recordedBody["stream"] != true {
		t.Fatalf("stream request accept=%q body=%#v", recordedAccept, recordedBody)
	}
	if _, ok := recordedBody["previous_response_id"]; ok {
		t.Fatalf("SSE Responses request unexpectedly included previous_response_id: %#v", recordedBody)
	}
	if response.Message != "hello world" || len(response.Items) != 2 {
		t.Fatalf("response = %#v", response)
	}
	if response.ResponseID != "resp-1" {
		t.Fatalf("response id = %q", response.ResponseID)
	}
	if response.Items[1].Type != "function_call" || response.Items[1].CallID != "call-1" || response.Items[1].Arguments != `{"text":"hi"}` {
		t.Fatalf("function item = %#v", response.Items[1])
	}
	if response.Usage.InputTokens != 4 || response.Usage.CachedInputTokens != 1 || response.Usage.OutputTokens != 2 || response.Usage.ReasoningOutputTokens != 1 || response.Usage.TotalTokens != 6 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.TimingMetrics["engine_iapi_ttft_total_ms"] != float64(310) || response.TimingMetrics["engine_service_ttft_total_ms"] != float64(340) {
		t.Fatalf("timing metrics = %#v", response.TimingMetrics)
	}
	if len(events) != 18 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].Kind != ResponsesStreamEventHeaders || events[0].RequestID != "req-1" || events[0].Model != "gpt-stream-header" || events[0].TurnState != "turn-state-1" || events[0].ModelsETag != "" {
		t.Fatalf("headers event = %#v", events[0])
	}
	if events[0].Headers["x-request-id"] != "req-1" || events[0].Headers["openai-model"] != "gpt-stream-header" {
		t.Fatalf("headers map = %#v", events[0].Headers)
	}
	serverModels := eventsByKind(events, ResponsesStreamEventServerModel)
	if len(serverModels) != 2 || serverModels[0].Model != "gpt-stream-header" || serverModels[1].Model != "gpt-metadata" {
		t.Fatalf("server model events = %#v", serverModels)
	}
	if reroute := firstEventByKind(events, ResponsesStreamEventModelReroute); reroute == nil || reroute.Reroute == nil || reroute.Reroute.FromModel != "gpt-old" || reroute.Reroute.ToModel != "gpt-new" {
		t.Fatalf("reroute event = %#v", reroute)
	}
	if verification := firstEventByKind(events, ResponsesStreamEventModelVerify); verification == nil || verification.Verification == nil || len(verification.Verification.Verifications) != 1 || verification.Verification.Verifications[0] != "trusted_access_for_cyber" {
		t.Fatalf("verification event = %#v", verification)
	}
	if moderation := firstEventByKind(events, ResponsesStreamEventModeration); moderation == nil || moderation.ModerationMetadata == nil {
		t.Fatalf("moderation event = %#v", moderation)
	}
	if buffering := firstEventByKind(events, ResponsesStreamEventSafetyBuffer); buffering == nil || buffering.SafetyBuffering == nil || !buffering.SafetyBuffering.ShowBufferingUI || buffering.SafetyBuffering.FasterModel == nil || *buffering.SafetyBuffering.FasterModel != "gpt-fast" {
		t.Fatalf("safety buffering event = %#v", buffering)
	}
	rateLimits := eventsByKind(events, ResponsesStreamEventRateLimits)
	if len(rateLimits) != 2 {
		t.Fatalf("rate limit events = %#v", rateLimits)
	}
	if rateLimits[0].RateLimit == nil || rateLimits[0].RateLimit.LimitID != "codex" || rateLimits[0].RateLimit.Primary == nil || rateLimits[0].RateLimit.Primary.UsedPercent != 12.5 {
		t.Fatalf("default rate limit = %#v", rateLimits[0])
	}
	if rateLimits[1].RateLimit == nil || rateLimits[1].RateLimit.LimitID != "codex_secondary" || rateLimits[1].RateLimit.LimitName != "gpt-secondary" || rateLimits[1].RateLimit.Primary == nil || rateLimits[1].RateLimit.Primary.UsedPercent != 80 {
		t.Fatalf("secondary rate limit = %#v", rateLimits[1])
	}
	reasoning := firstEventByKind(events, ResponsesStreamEventReasoning)
	if reasoning == nil || reasoning.Reasoning == nil || !*reasoning.Reasoning {
		t.Fatalf("reasoning event = %#v", reasoning)
	}
	timing := firstEventByKind(events, ResponsesStreamEventTimingMetrics)
	if timing == nil || timing.TimingMetrics["responses_duration_excl_engine_and_client_tool_time_ms"] != float64(120) {
		t.Fatalf("timing event = %#v", timing)
	}
	firstDelta := firstEventByKind(events, ResponsesStreamEventOutputText)
	if firstDelta == nil || firstDelta.Delta != "hello " {
		t.Fatalf("first delta event = %#v", firstDelta)
	}
	outputAdded := firstEventByKind(events, ResponsesStreamEventOutputAdded)
	if outputAdded == nil || outputAdded.ResponseID != "resp-1" {
		t.Fatalf("output added event = %#v", outputAdded)
	}
	if events[len(events)-1].Kind != ResponsesStreamEventCompleted || events[len(events)-1].ResponseID != "resp-1" {
		t.Fatalf("completed event = %#v", events[len(events)-1])
	}
}

func TestParseResponsesStreamAccumulatesFunctionCallArgumentDeltas(t *testing.T) {
	var events []ResponsesStreamEvent
	response, err := parseResponsesStream(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.added","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"exec_command","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"{\"cmd\":\""}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc-1","call_id":"call-1","delta":"pwd\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"exec_command","arguments":""}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{Prompt: "inspect", Model: "gpt-test"},
		"openai",
		func(event *ResponsesStreamEvent) {
			events = append(events, *event)
		},
	)
	if err != nil {
		t.Fatalf("parseResponsesStream() error = %v", err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %#v", response.Items)
	}
	item := response.Items[0]
	if item.Type != "function_call" || item.Name != "exec_command" || item.CallID != "call-1" || item.Arguments != `{"cmd":"pwd"}` {
		t.Fatalf("function call item = %#v", item)
	}
	toolDeltas := eventsByKind(events, ResponsesStreamEventToolInputDelta)
	if len(toolDeltas) != 2 || toolDeltas[0].ItemID != "fc-1" || toolDeltas[0].CallID != "call-1" {
		t.Fatalf("tool delta events = %#v", toolDeltas)
	}
	done := firstEventByKind(events, ResponsesStreamEventOutputDone)
	if done == nil || done.Item == nil || done.Item.Arguments != `{"cmd":"pwd"}` {
		t.Fatalf("done event = %#v", done)
	}
}

func TestResponsesAgentRunnerStreamsRetryAndIdleTimeout(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 1,
			StreamMaxRetries:  1,
			StreamIdleTimeout: 5 * time.Millisecond,
		},
		Stream: true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want request retry plus one dropped-stream retry", attempts)
	}
}

func TestResponsesAgentRunnerRetriesDroppedSSEStreamLikeRust(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if attempt == 1 {
			_, _ = w.Write([]byte(responsesSSE(`{"type":"response.created","response":{"id":"resp-dropped"}}`)))
			return
		}
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-recovered","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			BaseURL:          server.URL + "/v1",
			StreamMaxRetries: 1,
		},
		Stream: true,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if response.ResponseID != "resp-recovered" || response.Message != "recovered" {
		t.Fatalf("response = %#v", response)
	}
}

func TestResponsesAgentRunnerRetriesTemporaryResponseFailedLikeRust(t *testing.T) {
	var attempts atomic.Int64
	var retryEvents []*ResponsesStreamEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if attempt == 1 {
			_, _ = w.Write([]byte(responsesSSE(
				`{"type":"response.failed","response":{"id":"resp-1","error":{"code":"upstream_unavailable","message":"Upstream service temporarily unavailable"}}}`,
			)))
			return
		}
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-2"}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			BaseURL:          server.URL + "/v1",
			StreamMaxRetries: 2,
		},
		Stream: true,
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event.Kind == ResponsesStreamEventRetrying {
				retryEvents = append(retryEvents, event)
			}
		},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil || response == nil || response.Message != "recovered" {
		t.Fatalf("response=%#v error=%v", response, err)
	}
	if attempts.Load() != 2 || len(retryEvents) != 1 || retryEvents[0].RetryAttempt != 1 {
		t.Fatalf("attempts=%d retryEvents=%#v", attempts.Load(), retryEvents)
	}
}

func TestResponsesAgentRunnerDoesNotRetryFatalResponseFailedLikeRust(t *testing.T) {
	for _, code := range []string{"context_length_exceeded", "insufficient_quota", "usage_not_included", "cyber_policy", "misalignment_policy_violation", "invalid_prompt", "bio_policy"} {
		t.Run(code, func(t *testing.T) {
			var attempts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(responsesSSE(fmt.Sprintf(
					`{"type":"response.failed","response":{"id":"resp-1","error":{"code":%q,"message":"fatal"}}}`,
					code,
				))))
			}))
			defer server.Close()
			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 2}, Stream: true})
			if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"}); err == nil {
				t.Fatal("Run() error = nil")
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

func TestMisalignmentPolicyViolationErrorsAreTypedAndNonRetryableLikeRust(t *testing.T) {
	err := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"misalignment_policy_violation","message":"This request violated the misalignment policy."}}}`))
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorMisalignmentPolicyViolation {
		t.Fatalf("streamed error = %#v", err)
	}
	if apiErr.Message != "This request violated the misalignment policy." {
		t.Fatalf("message = %q", apiErr.Message)
	}

	blank := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"misalignment_policy_violation","message":"   "}}}`))
	if !errors.As(blank, &apiErr) || apiErr.Message != "This request was blocked due to a misalignment policy violation." {
		t.Fatalf("blank streamed error = %#v", blank)
	}

	// HTTP 400 with the misalignment code is also typed and not retried.
	httpErr := responsesHTTPError("openai", http.StatusBadRequest, http.Header{}, []byte(`{"error":{"code":"misalignment_policy_violation","message":"blocked"}}`))
	if !errors.As(httpErr, &apiErr) || apiErr.Kind != codexapi.ErrorMisalignmentPolicyViolation || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("http error = %#v", httpErr)
	}
	if apiErr.Message != "blocked" {
		t.Fatalf("http message = %q", apiErr.Message)
	}
}

func TestResponseFailedRateLimitRetryDelayLikeRust(t *testing.T) {
	err := responseFailedError([]byte(`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Please try again in 11.054s."}}}`))
	if delay, ok := codexapi.RetryDelayInfo(err); !ok || delay != 11054*time.Millisecond {
		t.Fatalf("retry delay = %v, %t", delay, ok)
	}
}

func TestResponsesAgentRunnerReportsResponseIncompleteReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":{"reason":"max_output_tokens"}}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 0},
		Stream:   true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil || err.Error() != "Incomplete response returned, reason: max_output_tokens" {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestResponsesAgentRunnerReportsUnknownResponseIncompleteReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":null}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1", StreamMaxRetries: 0},
		Stream:   true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil || err.Error() != "Incomplete response returned, reason: unknown" {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestResponsesAgentRunnerStreamsResponseFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.failed","response":{"id":"resp-1","error":{"code":"context_length_exceeded","message":"too much context"}}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil || !strings.Contains(err.Error(), "context window exceeded") {
		t.Fatalf("Run error = %v", err)
	}
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error type = %T, want APIError", err)
	}
	if apiErr.Kind != codexapi.ErrorContextWindowExceeded || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("APIError = %#v, want context window exceeded bad request", apiErr)
	}
}

func TestResponsesAgentRunnerReturnsAPIErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-error")
		w.Header().Set("cf-ray", "ray-error")
		w.Header().Set("x-openai-authorization-error", "identity denied")
		w.Header().Set("x-error-json", base64.StdEncoding.EncodeToString([]byte(`{"error":{"code":"identity_denied"}}`)))
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad prompt","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL + "/v1"},
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err == nil || !strings.Contains(err.Error(), "bad prompt") {
		t.Fatalf("Run error = %v", err)
	}
	var apiErr *ResponsesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error type = %T", err)
	}
	if apiErr.RequestID != "req-error" || apiErr.CFRay != "ray-error" || apiErr.AuthorizationError != "identity denied" || apiErr.AuthorizationErrorCode != "identity_denied" {
		t.Fatalf("debug context = %#v", apiErr)
	}
	if !strings.Contains(err.Error(), "request_id: req-error") || !strings.Contains(err.Error(), "cf_ray: ray-error") {
		t.Fatalf("error string = %v", err)
	}
}

func TestResponsesAgentRunnerMapsBedrockExpiredSignature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-bedrock")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Signature expired: 20260609T133205Z is now earlier than 20260614T062525Z"))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: AmazonBedrockProviderName, BaseURL: server.URL + "/openai/v1"},
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: AmazonBedrockGPT55ModelID})
	if err == nil {
		t.Fatal("Run returned nil error, want API error")
	}
	var apiErr *ResponsesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error type = %T", err)
	}
	if apiErr.Message != bedrockExpiredSignatureMessage {
		t.Fatalf("message = %q", apiErr.Message)
	}
	if !strings.Contains(err.Error(), "Refresh your AWS credentials and retry") || !strings.Contains(err.Error(), "request_id: req-bedrock") {
		t.Fatalf("error string = %v", err)
	}
}

func TestResponsesAgentRunnerKeepsGenericBedrockUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("The security token included in the request is invalid"))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: AmazonBedrockProviderName, BaseURL: server.URL + "/openai/v1"},
	})
	_, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: AmazonBedrockGPT55ModelID})
	if err == nil || !strings.Contains(err.Error(), "The security token included in the request is invalid") {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(err.Error(), "Refresh your AWS credentials") {
		t.Fatalf("error string = %v", err)
	}
}

func hasResponseToolType(tools []any, toolType string) bool {
	for _, tool := range tools {
		item, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] == toolType {
			return true
		}
	}
	return false
}

func hasResponseNamespaceFunction(tools []any, namespace string, name string) bool {
	for _, tool := range tools {
		item, ok := tool.(map[string]any)
		if !ok || item["type"] != "namespace" || item["name"] != namespace {
			continue
		}
		switch children := item["tools"].(type) {
		case []map[string]any:
			for _, child := range children {
				if child["name"] == name {
					return true
				}
			}
		case []any:
			for _, childValue := range children {
				child, ok := childValue.(map[string]any)
				if ok && child["name"] == name {
					return true
				}
			}
		}
	}
	return false
}

func responsesSSE(payloads ...string) string {
	var builder strings.Builder
	for _, payload := range payloads {
		eventType := responseSSEType(payload)
		if eventType != "" {
			builder.WriteString("event: ")
			builder.WriteString(eventType)
			builder.WriteByte('\n')
		}
		builder.WriteString("data: ")
		builder.WriteString(payload)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func responseSSEType(payload string) string {
	var value map[string]any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return ""
	}
	eventType, _ := value["type"].(string)
	return eventType
}

func eventsByKind(events []ResponsesStreamEvent, kind ResponsesStreamEventKind) []ResponsesStreamEvent {
	out := []ResponsesStreamEvent{}
	for _, event := range events {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func firstEventByKind(events []ResponsesStreamEvent, kind ResponsesStreamEventKind) *ResponsesStreamEvent {
	for i := range events {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

func TestResponsesInputItemsStripEncryptedFunctionArgsForNonOpenAIProviders(t *testing.T) {
	marker := []string{}
	request := &AgentRequest{InputItems: []any{
		&AgentItem{Type: "function_call", Name: "spawn_agent", EncryptedFunctionArgs: &marker},
		map[string]any{"type": "function_call", "name": "send_message", "encrypted_function_args": []any{}},
	}}
	openAI := responsesInputItemsForProvider(request, OpenAIProviderName)
	if openAI[0].(*AgentItem).EncryptedFunctionArgs == nil {
		t.Fatal("OpenAI input lost encrypted function marker")
	}
	nonOpenAI := responsesInputItemsForProvider(request, "ollama")
	if nonOpenAI[0].(*AgentItem).EncryptedFunctionArgs != nil {
		t.Fatalf("non-OpenAI AgentItem marker = %#v", nonOpenAI[0])
	}
	if _, ok := nonOpenAI[1].(map[string]any)["encrypted_function_args"]; ok {
		t.Fatalf("non-OpenAI map marker = %#v", nonOpenAI[1])
	}
}

func TestAddMemoryGenerationHeaderForConsolidationAgentLikeRust(t *testing.T) {
	headers := http.Header{}
	addMemoryGenerationHeader(headers, map[string]string{
		codexapi.ClientOpenAISubagentHeader: "memory_consolidation",
	})
	if got := headers.Get(codexapi.ClientOpenAIMemgenRequestHeader); got != "true" {
		t.Fatalf("memory generation header = %q, want true", got)
	}

	ordinary := http.Header{}
	addMemoryGenerationHeader(ordinary, map[string]string{
		codexapi.ClientOpenAISubagentHeader: "review",
	})
	if got := ordinary.Get(codexapi.ClientOpenAIMemgenRequestHeader); got != "" {
		t.Fatalf("ordinary subagent memory generation header = %q, want empty", got)
	}
}

func TestResponsesAgentRunnerRefreshWorkloadIdentityAfterUnauthorizedLikeRust(t *testing.T) {
	// Mirrors Rust refresh_external_auth (login/src/auth/manager.rs): when the
	// process selected workload identity, a downstream 401 re-exchanges the
	// workload token preserving the previously authenticated account instead
	// of falling through to OAuth refresh.
	runner := &ResponsesAgentRunner{
		AuthSnapshot: &auth.AuthDotJSON{
			Tokens: map[string]any{"access_token": "old", "account_id": "account-one"},
		},
		WorkloadIdentityRefresh: func(ctx context.Context, previousAccountID string) (*auth.AuthDotJSON, error) {
			if previousAccountID != "account-one" {
				t.Fatalf("previous account id = %q, want account-one", previousAccountID)
			}
			return &auth.AuthDotJSON{
				Tokens: map[string]any{"access_token": "new-workload", "account_id": "account-one"},
			}, nil
		},
	}
	if err := runner.refreshWorkloadIdentityAuth(context.Background()); err != nil {
		t.Fatalf("refreshWorkloadIdentityAuth: %v", err)
	}
	if got := stringFromAny(runner.AuthSnapshot.Tokens, "access_token"); got != "new-workload" {
		t.Fatalf("access token after refresh = %q, want new-workload", got)
	}
	if runner.Auth == nil {
		t.Fatal("auth headers not updated after workload identity refresh")
	}
}

func TestResponsesAgentRunnerWorkloadIdentityRefreshFallsThroughWhenNotSelected(t *testing.T) {
	// Without a configured workload identity, the refresh falls through to the
	// next recovery step instead of erroring the whole recovery.
	runner := &ResponsesAgentRunner{AuthSnapshot: &auth.AuthDotJSON{}}
	err := runner.refreshWorkloadIdentityAuth(context.Background())
	if err == nil {
		t.Fatal("expected workload identity refresh to report unavailability")
	}
}

func TestResponsesAgentRunnerAppliesManagedResidencyHeaderLikeRust(t *testing.T) {
	provider := &APIProvider{
		Name:    OpenAIProviderName,
		BaseURL: "https://example.invalid/v1",
		// A provider-configured residency header must be overridden by the
		// managed enforce_residency requirement (#39645).
		Headers: http.Header{ResidencyHeaderName: []string{"provider-value"}},
	}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:      provider,
		ModelsManager: NewStaticModelsManager(ModelsResponse{}),
		ProviderID:    OpenAIProviderID,
	})
	runner.Residency = "us"
	req, err := runner.newResponsesHTTPRequest(context.Background(), &AgentRequest{Model: "gpt-test"}, &responsesAgentRequest{}, "application/json")
	if err != nil {
		t.Fatalf("newResponsesHTTPRequest() error = %v", err)
	}
	if got := req.Header.Get(ResidencyHeaderName); got != "us" {
		t.Fatalf("residency header = %q, want managed value us", got)
	}
}
