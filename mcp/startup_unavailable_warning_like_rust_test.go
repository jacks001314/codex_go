package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPStartupUnavailableReasonMatchesRustLikeRust(t *testing.T) {
	tests := []struct {
		name                   string
		err                    error
		authenticationRequired bool
		want                   string
	}{
		{"auth", errors.New("401 unauthorized"), true, "authentication required; sign in to the MCP server"},
		{"forbidden", &mcpHTTPStatusError{StatusCode: 403, Status: "403 Forbidden"}, false, "HTTP 403 Forbidden"},
		{"not found", &mcpHTTPStatusError{StatusCode: 404, Status: "404 Not Found"}, false, "HTTP 404 Not Found"},
		{"other status", &mcpHTTPStatusError{StatusCode: 500, Status: "500 Internal Server Error"}, false, "HTTP response error"},
		{"certificate", errors.New("x509: certificate signed by unknown authority"), false, "TLS certificate error"},
		{"dns", errors.New("dial tcp: lookup mcp.example on 10.0.0.1:53: no such host"), false, "DNS lookup failed"},
		{"refused", errors.New("Post \"https://mcp.example/mcp\": dial tcp: connect: connection refused"), false, "connection refused"},
		{"request", errors.New("http/request failed: EOF"), false, "HTTP request failed"},
		{"timeout", errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)"), false, "startup timed out"},
		{"unknown", errors.New("something unexpected"), false, "initialization failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpStartupUnavailableReason(tc.err, tc.authenticationRequired); got != tc.want {
				t.Fatalf("mcpStartupUnavailableReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHTTPMCPStartupFailureWarnsOnceWithSanitizedReasonLikeRust(t *testing.T) {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"remote": {
			Name: "remote",
			Config: ServerConfig{
				// Rust #62906 keeps the endpoint URL out of the warning, so a
				// credential embedded there must never reach stderr.
				URL:            server.URL + "/mcp?api_key=SECRET-TOKEN",
				Enabled:        true,
				Required:       true,
				StartupTimeout: 5 * time.Second,
			},
		},
	}})
	defer service.Close()

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.ListStatusChecked(&MCPListServerStatusParams{
			Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		}); err != nil {
			t.Fatalf("ListStatusChecked() attempt %d error = %v", attempt, err)
		}
	}
	output := logs.String()
	if got := strings.Count(output, "MCP server unavailable"); got != 1 {
		t.Fatalf("startup warnings = %d, want one per connection:\n%s", got, output)
	}
	for _, want := range []string{"server_name=remote", "reason=\"HTTP 403 Forbidden\""} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup warning is missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "SECRET-TOKEN") {
		t.Fatalf("startup warning leaked the endpoint credential:\n%s", output)
	}
}

func TestOptionalHTTPMCPStartupPendingOmissionWarnsOnceLikeRust(t *testing.T) {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(200 * time.Millisecond):
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defer close(release)

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"slow-remote": {
			Name: "slow-remote",
			Config: ServerConfig{
				URL:            server.URL + "/mcp",
				Enabled:        true,
				StartupTimeout: 300 * time.Millisecond,
			},
		},
	}})
	defer service.Close()

	started := time.Now()
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 20 * time.Millisecond,
	})
	if err != nil || response == nil {
		t.Fatalf("ListStatusChecked() response=%#v err=%v", response, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("optional startup was not omitted after the grace window: %v", elapsed)
	}
	if len(response.Data) != 1 || len(response.Data[0].Tools) != 0 {
		t.Fatalf("response = %#v, want the pending server omitted from this turn", response)
	}
	output := logs.String()
	if got := strings.Count(output, "MCP server unavailable"); got != 1 {
		t.Fatalf("startup warnings = %d, want one:\n%s", got, output)
	}
	for _, want := range []string{"server_name=slow-remote", "reason=\"startup still pending; omitted from this turn\""} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup warning is missing %q:\n%s", want, output)
		}
	}
}
