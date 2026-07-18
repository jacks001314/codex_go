package codexapi

import (
	"net/http"
	"testing"
	"time"
)

func TestProviderURLAndWebsocket(t *testing.T) {
	provider := &Provider{
		Name:        "openai",
		BaseURL:     "https://api.openai.com/v1/",
		QueryParams: map[string]string{"api-version": "1"},
		Headers:     http.Header{"X-Test": []string{"present"}},
	}
	if got := provider.URLForPath("/responses"); got != "https://api.openai.com/v1/responses?api-version=1" {
		t.Fatalf("URLForPath() = %q", got)
	}
	ws, err := provider.WebsocketURLForPath("/realtime")
	if err != nil || ws != "wss://api.openai.com/v1/realtime?api-version=1" {
		t.Fatalf("WebsocketURLForPath() = %q, %v", ws, err)
	}
	request := provider.BuildRequest(http.MethodPost, "responses")
	if request.Headers.Get("X-Test") != "present" {
		t.Fatalf("headers not cloned: %+v", request.Headers)
	}
}

func TestAzureDetectionAndRetryPolicy(t *testing.T) {
	if !IsAzureResponsesProvider("test", "https://foo.openai.azure.com/openai") {
		t.Fatalf("expected azure URL")
	}
	if IsAzureResponsesProvider("test", "https://api.openai.com/v1") {
		t.Fatalf("unexpected azure URL")
	}
	config := RetryConfig{MaxAttempts: 3, BaseDelay: time.Second, Retry429: true, Retry5xx: true}
	policy := config.ToPolicy()
	if policy.MaxAttempts != 3 || !policy.RetryOn.Retry429 || policy.RetryOn.RetryTransport {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestAPIErrorStringsAndAuthTelemetry(t *testing.T) {
	if got := NewAPIError(429, "slow down").Error(); got != "api error 429: slow down" {
		t.Fatalf("Error() = %q", got)
	}
	auth := &HeaderAuthProvider{Headers: http.Header{"Authorization": []string{"Bearer token"}}}
	telemetry := AuthHeaderTelemetryFor(auth)
	if !telemetry.Attached || telemetry.Name != "authorization" {
		t.Fatalf("telemetry = %+v", telemetry)
	}
}
