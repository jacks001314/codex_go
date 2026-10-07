package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	contextfrag "codex_go/context"
)

// Rust #49119 (upstream 8bd5a136ff) recognizes a content-filter stop as its own
// error while keeping the public copy. Rust tests:
// codex-api/src/api_bridge_tests.rs
// `map_api_error_preserves_content_filter_retry_and_public_error`,
// protocol/src/error_tests.rs `retryability_preserves_error_details_distinctions`.
func TestResponseIncompleteErrorContentFilterLikeRust(t *testing.T) {
	blocked := []byte(`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":{"reason":"content_filter"}}}`)
	err := responseIncompleteError(blocked)
	if err == nil || !isContentFilterStreamError(err) {
		t.Fatalf("error = %v, want the content-filter error", err)
	}
	want := "stream disconnected before completion: Incomplete response returned, reason: content_filter"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if !isRetryableResponsesStreamError(err) {
		t.Fatal("the content-filter error must keep the stream retry budget")
	}

	other := responseIncompleteError([]byte(`{"type":"response.incomplete","response":{"id":"resp-1","incomplete_details":{"reason":"max_output_tokens"}}}`))
	if isContentFilterStreamError(other) {
		t.Fatal("another incomplete reason must not be the content-filter error")
	}
	if got := other.Error(); got != "Incomplete response returned, reason: max_output_tokens" {
		t.Fatalf("other = %q", got)
	}
}

// Rust #49119 (core/tests/suite/scenarios_content_filter.rs
// `content_filter_guidance_is_appended_after_each_block`): every content-filter
// block appends the resolved developer guidance, so the retried request already
// carries it, and a later unblocked attempt completes.
func TestResponsesStreamingAppendsContentFilterGuidanceLikeRust(t *testing.T) {
	custom := "Your previous response was blocked. Offer a permitted alternative."
	runner := contentFilterTestRunner(t, &custom)

	bodies := runContentFilterScenario(t, runner)

	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(bodies))
	}
	// encoding/json escapes the markers on the wire (\u003c = '<').
	if strings.Contains(bodies[0], `\u003ccontent_filter_guidance\u003e`) {
		t.Fatalf("the first request must not carry guidance:\n%s", bodies[0])
	}
	for _, index := range []int{1, 2} {
		if !strings.Contains(bodies[index], `\u003ccontent_filter_guidance\u003e`) ||
			!strings.Contains(bodies[index], custom) ||
			!strings.Contains(bodies[index], `\u003c/content_filter_guidance\u003e`) {
			t.Fatalf("request %d is missing the developer guidance:\n%s", index+1, bodies[index])
		}
		if !strings.Contains(bodies[index], `"role":"developer"`) {
			t.Fatalf("request %d guidance is not a developer message:\n%s", index+1, bodies[index])
		}
		if !strings.Contains(bodies[index], "generic.content_filter_guidance") {
			t.Fatalf("request %d is missing the content kind:\n%s", index+1, bodies[index])
		}
	}
}

// The bundled guidance is used when the catalog carries no usable override.
func TestResponsesStreamingUsesBundledContentFilterGuidanceLikeRust(t *testing.T) {
	runner := contentFilterTestRunner(t, nil)
	bodies := runContentFilterScenario(t, runner)
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(bodies))
	}
	if !strings.Contains(bodies[1], contextfrag.ContentFilterGuidanceDefault) {
		t.Fatalf("request 2 is missing the bundled guidance:\n%s", bodies[1])
	}
}

func contentFilterTestRunner(t *testing.T, guidance *string) *ResponsesAgentRunner {
	t.Helper()
	manager := NewStaticModelsManager(BundledModelsResponse())
	if guidance != nil {
		catalog := BundledModelsResponse()
		if len(catalog.Models) > 0 {
			catalog.Models[0].Slug = "gpt-test"
			if catalog.Models[0].ModelMessages == nil {
				catalog.Models[0].ModelMessages = &ModelMessages{}
			}
			catalog.Models[0].ModelMessages.ContentFilterGuidance = guidance
			manager = NewStaticModelsManager(catalog)
		}
	}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:      &APIProvider{Name: OpenAIProviderName, BaseURL: "http://127.0.0.1:1/v1", StreamMaxRetries: 2},
		ModelsManager: manager,
		Stream:        true,
	})
	// The static catalog is addressed by slug, so pin the scenario's model.
	runner.Provider.BaseURL = ""
	return runner
}

func runContentFilterScenario(t *testing.T, runner *ResponsesAgentRunner) []string {
	t.Helper()
	block := `{"type":"response.incomplete","response":{"id":"blocked","incomplete_details":{"reason":"content_filter"}}}`
	recovered := `{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"I can help with a permitted alternative."}]}}`
	done := `{"type":"response.completed","response":{"id":"recovered"}}`
	var (
		mu     sync.Mutex
		bodies []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		attempt := len(bodies)
		mu.Unlock()
		final := []string{recovered, done}
		writer.Header().Set("Content-Type", "text/event-stream")
		if attempt >= 3 {
			_, _ = writer.Write([]byte(responsesSSE(final...)))
			return
		}
		_, _ = writer.Write([]byte(responsesSSE(block)))
	}))
	defer server.Close()
	runner.Provider.BaseURL = server.URL + "/v1"
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "help me", Model: "gpt-test"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), bodies...)
}
