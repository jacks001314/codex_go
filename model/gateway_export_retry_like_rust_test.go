package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #59816: a rejected gateway credential export is dropped from the cache
// so the retry runs the command again.
func TestConfiguredProviderRecoverFromUnauthorizedInvalidatesCredentialExportLikeRust(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "exports.log")
	// A .bat keeps the redirection inside one interpreter invocation so the
	// token still reaches stdout while each run is recorded.
	scriptPath := filepath.Join(t.TempDir(), "export.bat")
	script := "@echo off\r\necho gw-token\r\necho gw-token>>\"" + logPath + "\"\r\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write export script: %v", err)
	}
	provider := &ConfiguredProvider{
		gatewayExport: newGatewayCredentialExport(&GatewayCredentialExportConfig{
			Command: "cmd",
			Args:    []string{"/c", scriptPath},
		}),
	}
	ctx := context.Background()
	first, err := provider.gatewayExport.ResolveAccessToken(ctx)
	if err != nil || first != "gw-token" {
		t.Fatalf("first export = %q err = %v", first, err)
	}
	if _, err := provider.gatewayExport.ResolveAccessToken(ctx); err != nil {
		t.Fatalf("cached export error = %v", err)
	}
	if lines := exportedTokenLines(t, logPath); lines != 1 {
		t.Fatalf("export runs before invalidation = %d, want 1", lines)
	}
	recovered, err := provider.RecoverFromUnauthorized(ctx)
	if err != nil || !recovered {
		t.Fatalf("RecoverFromUnauthorized() = %t, %v; want true", recovered, err)
	}
	again, err := provider.gatewayExport.ResolveAccessToken(ctx)
	if err != nil || again != "gw-token" {
		t.Fatalf("export after invalidation = %q err = %v", again, err)
	}
	if lines := exportedTokenLines(t, logPath); lines != 2 {
		t.Fatalf("export runs after invalidation = %d, want 2", lines)
	}
	if recovered, err := (&ConfiguredProvider{}).RecoverFromUnauthorized(ctx); recovered || err != nil {
		t.Fatalf("provider without an export recovered = %t, %v", recovered, err)
	}
}

func exportedTokenLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export log: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// Rust #59816: the provider recovery runs once per request, before the shared
// auth plan, and the retry is the only extra attempt.
func TestResponsesAgentRunnerRetriesOnceWithProviderRecoveryLikeRust(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"http-after-provider-recovery","output_text":"ok","output":[]}`))
	}))
	defer server.Close()

	recovered := 0
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL},
		ProviderUnauthorizedRecovery: func(context.Context) (bool, error) {
			recovered++
			return true, nil
		},
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"})
	if err != nil || response == nil || response.ResponseID != "http-after-provider-recovery" {
		t.Fatalf("response = %#v err = %v", response, err)
	}
	if attempts != 2 || recovered != 1 {
		t.Fatalf("attempts = %d provider recoveries = %d, want 2/1", attempts, recovered)
	}
}

// A second unauthorized response stops after the single provider retry.
func TestResponsesAgentRunnerStopsAfterSecondUnauthorizedLikeRust(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	recovered := 0
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{BaseURL: server.URL},
		ProviderUnauthorizedRecovery: func(context.Context) (bool, error) {
			recovered++
			return true, nil
		},
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hello"}); err == nil {
		t.Fatal("Run() returned no error for a repeated 401")
	}
	if attempts != 2 || recovered != 1 {
		t.Fatalf("attempts = %d provider recoveries = %d, want 2/1", attempts, recovered)
	}
}
