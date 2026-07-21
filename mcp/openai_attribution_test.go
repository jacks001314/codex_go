package mcp

import (
	"net/http"
	"net/url"
	"testing"
)

func TestMaybeWithOpenAIDocsSourceAttributionMatchesURL(t *testing.T) {
	modifier := MaybeWithOpenAIDocsSourceAttribution("https://developers.openai.com/mcp")
	if modifier == nil {
		t.Fatal("should return modifier for OpenAI docs MCP URL")
	}
}

func TestMaybeWithOpenAIDocsSourceAttributionSkipsOtherURL(t *testing.T) {
	modifier := MaybeWithOpenAIDocsSourceAttribution("https://api.example.com/mcp")
	if modifier != nil {
		t.Fatal("should not return modifier for non-OpenAI URL")
	}
}

func TestMaybeWithOpenAIDocsSourceAttributionSkipsEmptyURL(t *testing.T) {
	modifier := MaybeWithOpenAIDocsSourceAttribution("")
	if modifier != nil {
		t.Fatal("should not return modifier for empty URL")
	}
}

func TestOpenAIAttributionAddsSourceParam(t *testing.T) {
	req, err := http.NewRequest("GET", openAIDeveloperDocsMCPURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	modifier := openAIAttributionRequestModifier
	modifier(req)
	if req.URL.Query().Get("source") != "codex" {
		t.Fatalf("source param not added: url=%s", req.URL.String())
	}
}

func TestOpenAIAttributionPreservesExistingQuery(t *testing.T) {
	req, err := http.NewRequest("GET", openAIDeveloperDocsMCPURL+"?foo=bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	modifier := openAIAttributionRequestModifier
	modifier(req)
	if req.URL.Query().Get("foo") != "bar" {
		t.Fatalf("existing foo param lost: url=%s", req.URL.String())
	}
	if req.URL.Query().Get("source") != "codex" {
		t.Fatalf("source param not added: url=%s", req.URL.String())
	}
}

func TestOpenAIAttributionSkipsNonDocsURL(t *testing.T) {
	req, err := http.NewRequest("GET", "https://api.example.com/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	u := req.URL.String()
	modifier := openAIAttributionRequestModifier
	modifier(req)
	if req.URL.String() != u {
		t.Fatalf("non-docs URL should not be modified: %s", req.URL.String())
	}
}

func TestOpenAIAttributionNilSafe(t *testing.T) {
	modifier := openAIAttributionRequestModifier
	modifier(nil) // should not panic
	var req *http.Request
	modifier(req) // should not panic
}

func TestOpenAIDeveloperDocsConstants(t *testing.T) {
	parsed, err := url.Parse(openAIDeveloperDocsMCPCodexURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("source") != "codex" {
		t.Fatalf("codex URL should have source=codex")
	}
}
