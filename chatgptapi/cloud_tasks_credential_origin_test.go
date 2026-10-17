package chatgptapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestValidateChatGPTBaseURLLikeRust pins Rust #41403: saved ChatGPT
// credentials may only go to trusted HTTPS origins on port 443 without user
// information, a query, or a fragment.
func TestValidateChatGPTBaseURLLikeRust(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{"https://chatgpt.com/backend-api", "https://chatgpt.com/backend-api"},
		{"https://chatgpt.com:443/backend-api/", "https://chatgpt.com:443/backend-api"},
		{"https://chat.openai.com/", "https://chat.openai.com/backend-api"},
		{"https://chatgpt-staging.com/backend-api", "https://chatgpt-staging.com/backend-api"},
		{"  https://ChatGPT.com/backend-api  ", "https://chatgpt.com/backend-api"},
	}
	for _, test := range valid {
		got, err := ValidateChatGPTBaseURL(test.input)
		if err != nil {
			t.Fatalf("ValidateChatGPTBaseURL(%q) error = %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("ValidateChatGPTBaseURL(%q) = %q, want %q", test.input, got, test.want)
		}
	}

	invalid := []string{
		"",
		"not a url",
		"http://chatgpt.com/backend-api",
		"https://example.com/backend-api",
		"https://chatgpt.com:8443/backend-api",
		"https://user:pass@chatgpt.com/backend-api",
		"https://chatgpt.com/backend-api?limit=1",
		"https://chatgpt.com/backend-api#fragment",
		"https://chatgpt.com.evil.example/backend-api",
	}
	for _, input := range invalid {
		if _, err := ValidateChatGPTBaseURL(input); err == nil {
			t.Fatalf("ValidateChatGPTBaseURL(%q) = nil error, want rejection", input)
		} else if !strings.Contains(err.Error(), "trusted HTTPS origin on port 443") {
			t.Fatalf("ValidateChatGPTBaseURL(%q) error = %v", input, err)
		}
	}
}

// TestCloudClientWithoutRedirectsDoesNotForwardCredentialsLikeRust pins the
// second half of Rust #41403: a 3xx response must not be followed with the
// Authorization header.
func TestCloudClientWithoutRedirectsDoesNotForwardCredentialsLikeRust(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-secret" {
			t.Errorf("Authorization = %q", got)
		}
		http.Redirect(w, r, target.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewCloudClientWithoutRedirects(&CloudClientOptions{
		BaseURL: source.URL,
		Headers: http.Header{"Authorization": []string{"Bearer synthetic-secret"}},
	})
	if _, err := client.ListTasks(context.Background(), &CloudListTasksParams{Limit: 1}); err == nil {
		t.Fatal("ListTasks() followed a redirect, want the 3xx reported as an error")
	}
	if got := redirected.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
}
