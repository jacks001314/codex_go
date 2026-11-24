package model

import (
	"context"
	"net/http"
	"testing"

	"codex_go/codexapi"
)

// Rust #63461/#63479: every Responses request asks the server to omit the
// preliminary reasoning ciphertext and the delta sequence numbers the client
// never reads. A provider-configured value wins, and an empty value opts out.
func TestResponsesStreamFieldExclusionsLikeRust(t *testing.T) {
	newRequest := func(t *testing.T, headers http.Header) *http.Request {
		t.Helper()
		runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
			Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: "https://example.test/v1", Headers: headers},
		})
		request, err := runner.newResponsesHTTPRequest(context.Background(), &AgentRequest{}, &responsesAgentRequest{Model: "gpt-test"}, "")
		if err != nil {
			t.Fatal(err)
		}
		return request
	}

	if got := newRequest(t, nil).Header.Get(codexapi.ClientCodexExcludeStreamFieldsHeader); got != "delta.sequence_number,reasoning.added.encrypted_content" {
		t.Fatalf("default stream-field exclusions = %q", got)
	}

	custom := http.Header{}
	custom.Set(codexapi.ClientCodexExcludeStreamFieldsHeader, "reasoning.added.encrypted_content")
	if got := newRequest(t, custom).Header.Get(codexapi.ClientCodexExcludeStreamFieldsHeader); got != "reasoning.added.encrypted_content" {
		t.Fatalf("provider stream-field exclusions = %q", got)
	}

	optOut := http.Header{}
	optOut.Set(codexapi.ClientCodexExcludeStreamFieldsHeader, "")
	request := newRequest(t, optOut)
	if values, ok := request.Header[http.CanonicalHeaderKey(codexapi.ClientCodexExcludeStreamFieldsHeader)]; !ok || len(values) != 1 || values[0] != "" {
		t.Fatalf("empty provider opt-out = %#v (present=%v)", values, ok)
	}
}
