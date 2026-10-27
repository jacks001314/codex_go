package codexapi

import (
	"errors"
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
	telemetry, err := AuthHeaderTelemetryFor(auth)
	if err != nil {
		t.Fatalf("AuthHeaderTelemetryFor() error = %v", err)
	}
	if !telemetry.Attached || telemetry.Name != "authorization" {
		t.Fatalf("telemetry = %+v", telemetry)
	}
}

// rejectedPartialAuth attaches a header and then fails, like Rust #55607's
// RejectedHeaderAuth: callers must observe the error instead of sending the
// partially authenticated request.
type rejectedPartialAuth struct{}

func (rejectedPartialAuth) AddAuthHeaders(headers http.Header) error {
	headers.Set("Authorization", "Bearer partial-auth")
	return ErrAuthBuild
}

func (rejectedPartialAuth) ApplyAuth(request Request) (Request, error) {
	if request.Headers == nil {
		request.Headers = http.Header{}
	}
	return request, rejectedPartialAuth{}.AddAuthHeaders(request.Headers)
}

// asyncOnlyAuth requires async resolution, mirroring Rust #55607's
// AsyncHeaderAuth: the synchronous path reports a build error while the async
// path can still authenticate.
type asyncOnlyAuth struct{}

func (asyncOnlyAuth) AddAuthHeaders(http.Header) error { return ErrAuthBuild }

func (asyncOnlyAuth) ApplyAuth(request Request) (Request, error) {
	request.Headers.Set("Authorization", "Bearer async-auth")
	return request, nil
}

func TestAuthHeaderFailuresPropagateLikeRust(t *testing.T) {
	headers, err := ToAuthHeaders(rejectedPartialAuth{})
	if !errors.Is(err, ErrAuthBuild) {
		t.Fatalf("ToAuthHeaders() error = %v, want ErrAuthBuild", err)
	}
	if headers.Get("Authorization") != "Bearer partial-auth" {
		t.Fatalf("partial header = %q, want the rejected provider value retained for inspection", headers.Get("Authorization"))
	}
	if _, err := AuthHeaderTelemetryFor(rejectedPartialAuth{}); !errors.Is(err, ErrAuthBuild) {
		t.Fatalf("AuthHeaderTelemetryFor() error = %v, want ErrAuthBuild", err)
	}
	if _, err := ToAuthHeaders(asyncOnlyAuth{}); !errors.Is(err, ErrAuthBuild) {
		t.Fatalf("sync async-only auth error = %v, want ErrAuthBuild", err)
	}
	request, err := asyncOnlyAuth{}.ApplyAuth(Request{Headers: http.Header{}})
	if err != nil {
		t.Fatalf("ApplyAuth() error = %v", err)
	}
	if request.Headers.Get("Authorization") != "Bearer async-auth" {
		t.Fatalf("async header = %q", request.Headers.Get("Authorization"))
	}
}
