package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Rust #61958: a steer that arrives after the stream observed its
// `response.completed` event keeps that completion. Its output and usage are
// settled instead of being abandoned with the request.
func TestResponsesRunnerPreemptionKeepsObservedCompletionLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(responsesSSE(
			`{"type":"response.output_item.done","item":{"id":"msg-final","type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-billed","output":[{"id":"msg-final","type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		)))
	}))
	defer server.Close()

	preempt := make(chan struct{})
	var once sync.Once
	// The runner-level handler sees the terminal event before the attempt's
	// held consumer handler releases it, which is the point the steer races.
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1", RequestMaxRetries: 1},
		Stream:   true,
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event != nil && event.Kind == ResponsesStreamEventCompleted {
				once.Do(func() { close(preempt) })
			}
		},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:  "hello",
		Model:   "gpt-test",
		Preempt: preempt,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || !response.Preempted || !response.Drained {
		t.Fatalf("response = %#v, want a drained preemption", response)
	}
	if response.Usage.TotalTokens != 8 || response.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v, want the completed response's usage", response.Usage)
	}
	if len(response.Items) != 1 || response.Items[0].Text != "final answer" {
		t.Fatalf("items = %#v, want the completed answer preserved", response.Items)
	}
}
