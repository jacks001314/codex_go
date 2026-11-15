package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// Mirrors Rust #61562/#61587: each opened response stream pairs its own
// Pre/PostModelRequest hooks, the request id stays stable across the sampling
// retries, the attempt number increases per stream, and the terminal
// response.completed event waits for the attempt's post hook.
func TestModelRequestHooksPairRetriedAttemptsLikeRust(t *testing.T) {
	var (
		mu           sync.Mutex
		requests     int
		timeline     []string
		events       []ModelRequestHookEvent
		completedIDs []string
	)
	record := func(entry string) {
		mu.Lock()
		timeline = append(timeline, entry)
		mu.Unlock()
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		mu.Lock()
		requests++
		attempt := requests
		mu.Unlock()
		if attempt == 1 {
			writer.Header().Set("Retry-After", "0")
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(retryAdviceRateLimitBody))
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(responsesSSE(
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}}`,
			`{"type":"response.completed","response":{"id":"recovered"}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{
			Name:              OpenAIProviderName,
			BaseURL:           server.URL + "/v1",
			RequestMaxRetries: 0,
			StreamMaxRetries:  1,
		},
		Stream: true,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{
		Prompt: "hi",
		Model:  "gpt-5.5",
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event != nil && event.Kind == ResponsesStreamEventCompleted {
				mu.Lock()
				completedIDs = append(completedIDs, event.ResponseID)
				mu.Unlock()
				record("terminal")
			}
		},
		OnModelRequest: func(_ context.Context, event ModelRequestHookEvent) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			record(event.Phase + "-" + strconv.Itoa(event.AttemptNumber))
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || response.ResponseID != "recovered" {
		t.Fatalf("response = %#v, want the recovered response", response)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v, want pre/post per attempt", events)
	}
	requestID := events[0].RequestID
	if requestID == "" {
		t.Fatal("request id is empty")
	}
	wantPhases := []string{"pre", "post", "pre", "post"}
	for index, event := range events {
		if event.Phase != wantPhases[index] {
			t.Fatalf("event[%d].Phase = %q, want %q", index, event.Phase, wantPhases[index])
		}
		if event.RequestID != requestID {
			t.Fatalf("event[%d].RequestID = %q, want %q", index, event.RequestID, requestID)
		}
		if event.Kind != SamplingKindSampling {
			t.Fatalf("event[%d].Kind = %q, want sampling", index, event.Kind)
		}
	}
	if events[0].AttemptNumber != 1 || events[1].AttemptNumber != 1 || events[2].AttemptNumber != 2 || events[3].AttemptNumber != 2 {
		t.Fatalf("attempt numbers = %d,%d,%d,%d, want 1,1,2,2", events[0].AttemptNumber, events[1].AttemptNumber, events[2].AttemptNumber, events[3].AttemptNumber)
	}
	if events[1].Status != "failed" || events[1].Error == "" {
		t.Fatalf("failed attempt post = %#v", events[1])
	}
	if events[3].Status != "completed" || events[3].ResponseID != "recovered" {
		t.Fatalf("completed attempt post = %#v", events[3])
	}
	if events[3].Usage == nil {
		t.Fatal("completed attempt post carries no usage")
	}
	wantTimeline := []string{"pre-1", "post-1", "pre-2", "post-2", "terminal"}
	if len(timeline) != len(wantTimeline) {
		t.Fatalf("timeline = %#v, want %#v", timeline, wantTimeline)
	}
	for index, entry := range timeline {
		if entry != wantTimeline[index] {
			t.Fatalf("timeline = %#v, want %#v", timeline, wantTimeline)
		}
	}
	if len(completedIDs) != 1 || completedIDs[0] != "recovered" {
		t.Fatalf("terminal response ids = %#v", completedIDs)
	}
}

// Mirrors Rust #61562: compaction requests report their own kind, and their
// request id is distinct from an ordinary sampling generation.
func TestModelRequestHooksReportSamplingKindLikeRust(t *testing.T) {
	var (
		kinds      []SamplingKind
		requestIDs []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(responsesSSE(
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"compacted"}]}}`,
			`{"type":"response.completed","response":{"id":"kind-response"}}`,
		)))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		Stream:   true,
	})
	run := func(kind SamplingKind) {
		t.Helper()
		_, err := runner.Run(context.Background(), &AgentRequest{
			Prompt: "hi", Model: "gpt-5.5", SamplingKind: kind,
			OnModelRequest: func(_ context.Context, event ModelRequestHookEvent) {
				if event.Phase != "post" {
					return
				}
				kinds = append(kinds, event.Kind)
				requestIDs = append(requestIDs, event.RequestID)
			},
		})
		if err != nil {
			t.Fatalf("Run(%q) error = %v", kind, err)
		}
	}
	run(SamplingKindBackgroundCompaction)
	run("")
	if len(kinds) != 2 || kinds[0] != SamplingKindBackgroundCompaction || kinds[1] != SamplingKindSampling {
		t.Fatalf("kinds = %#v", kinds)
	}
	if requestIDs[0] == "" || requestIDs[1] == "" || requestIDs[0] == requestIDs[1] {
		t.Fatalf("request ids = %#v, want distinct non-empty ids", requestIDs)
	}
}

// Mirrors Rust #61562: a non-streaming Responses request still pairs exactly
// one Pre/PostModelRequest lifecycle.
func TestModelRequestHooksPairNonStreamingRequestLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp-nonstream",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":12}
		}`))
	}))
	defer server.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		Stream:   false,
	})
	var events []ModelRequestHookEvent
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Prompt: "hi", Model: "gpt-5.5",
		OnModelRequest: func(_ context.Context, event ModelRequestHookEvent) {
			events = append(events, event)
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(events) != 2 || events[0].Phase != "pre" || events[1].Phase != "post" {
		t.Fatalf("events = %#v, want one pre/post pair", events)
	}
	if events[1].Status != "completed" || events[1].ResponseID != "resp-nonstream" || events[1].AttemptNumber != 1 {
		t.Fatalf("post event = %#v", events[1])
	}
	if events[1].Usage == nil || events[1].Usage.TotalTokens != 12 {
		t.Fatalf("post usage = %#v, want 12 total tokens", events[1].Usage)
	}
}
