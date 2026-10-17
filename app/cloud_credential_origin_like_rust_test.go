package app

import (
	"strings"
	"testing"

	"codex_go/cli"
)

// TestNewCloudTaskClientRejectsUntrustedBaseURLLikeRust pins Rust #41403: the
// cloud backend destination is validated before saved ChatGPT credentials are
// loaded, so a custom backend never receives them.
func TestNewCloudTaskClientRejectsUntrustedBaseURLLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, baseURL := range []string{
		"http://127.0.0.1:9/backend-api",
		"https://example.com/backend-api",
		"https://chatgpt.com/backend-api?query=1",
	} {
		t.Setenv("CODEX_CLOUD_TASKS_BASE_URL", baseURL)
		client, err := newCloudTaskClient(&cli.CloudOptions{})
		if err == nil {
			t.Fatalf("newCloudTaskClient(%q) = %#v, want rejection", baseURL, client)
		}
		if !strings.Contains(err.Error(), "trusted HTTPS origin on port 443") {
			t.Fatalf("newCloudTaskClient(%q) error = %v", baseURL, err)
		}
	}
}

func TestNewCloudTaskClientDisablesRedirectsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_CLOUD_TASKS_BASE_URL", "https://chatgpt.com/backend-api")
	client, err := newCloudTaskClient(&cli.CloudOptions{})
	if err != nil {
		t.Fatalf("newCloudTaskClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("newCloudTaskClient() = nil")
	}
	if got := client.BaseURL(); got != "https://chatgpt.com/backend-api" {
		t.Fatalf("BaseURL() = %q", got)
	}
}
