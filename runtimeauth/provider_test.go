package runtimeauth

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"codex_go/auth"
)

func TestClassifyOpenAIDestinationLikeRust(t *testing.T) {
	unsupported := []string{
		"https://unknown.api.openai.com/v1",
		"https://api.openai.com../v1",
		"https://api.openai.com:8443/v1",
		"http://api.openai.com/v1",
		"https://user@api.openai.com/v1",
		"https://other.openai.org/v1",
	}
	for _, destination := range unsupported {
		if got := ClassifyOpenAIDestination(destination); got != DestinationUnsupported {
			t.Fatalf("ClassifyOpenAIDestination(%q) = %v, want unsupported", destination, got)
		}
	}
	other := []string{
		"https://api.openai.com.example.test/v1",
		"https://example.test/v1",
		"http://127.0.0.1:8080/v1",
	}
	for _, destination := range other {
		if got := ClassifyOpenAIDestination(destination); got != DestinationOther {
			t.Fatalf("ClassifyOpenAIDestination(%q) = %v, want other", destination, got)
		}
	}
	supported := []string{
		"https://api.openai.com/v1",
		"https://chatgpt.com/backend-api",
		"https://foo.chatgpt.com/backend-api",
		"https://api.openai.org/v1",
	}
	for _, destination := range supported {
		if got := ClassifyOpenAIDestination(destination); got != DestinationSupported {
			t.Fatalf("ClassifyOpenAIDestination(%q) = %v, want supported", destination, got)
		}
	}
}

func TestProviderSkipsCustomDestinationsLikeRust(t *testing.T) {
	fake := newFakeRegistrationServer(t)
	manager, err := NewManager(ManagerOptions{
		Credentials:      func(context.Context) (*auth.AuthDotJSON, error) { return chatgptSnapshot("token"), nil },
		SourceGeneration: func() uint64 { return 1 },
		InstallationID:   "88888888-8888-4888-8888-888888888888",
		RegistrationURL:  fake.server.URL,
		HTTPClient:       fake.server.Client(),
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	provider := &Provider{Manager: manager, Mode: ModeDualHeader}

	headers, err := provider.HeadersForDestination(context.Background(), "https://example.test/v1")
	if err != nil {
		t.Fatalf("custom destination error = %v", err)
	}
	if headers != nil {
		t.Fatalf("custom destination headers = %#v, want none", headers)
	}
	if fake.count() != 0 {
		t.Fatalf("custom destinations must not register a runtime")
	}

	headers, err = provider.HeadersForDestination(context.Background(), "https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("supported destination error = %v", err)
	}
	if !strings.HasPrefix(headers.Get("Authorization"), "Bearer token") || headers.Get(AssertionHeader) == "" {
		t.Fatalf("supported destination headers = %#v", headers)
	}
}

func TestProtectSensitiveHeadersStripsCrossOriginRuntimeCredentials(t *testing.T) {
	policy := ProtectSensitiveHeaders(nil)
	previous, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	crossOrigin, _ := http.NewRequest(http.MethodGet, "https://evil.test/steal", nil)
	crossOrigin.Header.Set("Authorization", "Bearer token")
	crossOrigin.Header.Set(AssertionHeader, "1.a.b.c")
	if err := policy(crossOrigin, []*http.Request{previous}); err != nil {
		t.Fatalf("policy error = %v", err)
	}
	if crossOrigin.Header.Get("Authorization") != "" || crossOrigin.Header.Get(AssertionHeader) != "" {
		t.Fatalf("cross-origin headers survived: %#v", crossOrigin.Header)
	}

	sameOrigin, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	sameOrigin.Header.Set("Authorization", "Bearer token")
	sameOrigin.Header.Set(AssertionHeader, "1.a.b.c")
	if err := policy(sameOrigin, []*http.Request{previous}); err != nil {
		t.Fatalf("policy error = %v", err)
	}
	if sameOrigin.Header.Get("Authorization") != "Bearer token" || sameOrigin.Header.Get(AssertionHeader) == "" {
		t.Fatalf("same-origin headers were dropped: %#v", sameOrigin.Header)
	}
}
