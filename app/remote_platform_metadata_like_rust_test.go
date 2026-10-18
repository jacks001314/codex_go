package app

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/auth"
)

func TestRemoteInitializeRetainsPlatformMetadataLikeRust(t *testing.T) {
	tests := []struct {
		name           string
		platformFamily string
		platformOS     string
	}{
		{name: "windows", platformFamily: "windows", platformOS: "windows"},
		{name: "unix linux", platformFamily: "unix", platformOS: "linux"},
		{name: "unknown values", platformFamily: "future-family", platformOS: "future-os"},
		{name: "missing os", platformFamily: "unix"},
		{name: "missing family", platformOS: "linux"},
		{name: "missing both"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := map[string]any{
				"userAgent": "codex_cli_rs/9.8.7-test (Test OS; x86_64) rust",
				"codexHome": "/server/.codex",
			}
			if tc.platformFamily != "" {
				result["platformFamily"] = tc.platformFamily
			}
			if tc.platformOS != "" {
				result["platformOs"] = tc.platformOS
			}
			payload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  result,
			})
			if err != nil {
				t.Fatalf("marshal initialize response: %v", err)
			}
			client := &remoteAppServerTUIClient{
				transport: &queuedRemoteTUITransport{messages: [][]byte{payload}},
			}
			if err := client.initialize(context.Background()); err != nil {
				t.Fatalf("initialize: %v", err)
			}
			if client.platformFamily != tc.platformFamily || client.platformOS != tc.platformOS {
				t.Fatalf("platform metadata = %q/%q, want %q/%q", client.platformFamily, client.platformOS, tc.platformFamily, tc.platformOS)
			}
		})
	}
}

func TestRemoteWindowsSandboxReadinessUsesServerPlatformLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		platformOS string
		want       bool
	}{
		{name: "windows", platformOS: "windows", want: true},
		{name: "linux", platformOS: "linux"},
		{name: "unknown", platformOS: "future-os"},
		{name: "missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &remoteAppServerTUIClient{platformOS: tc.platformOS}
			got := remoteAppServerRunsOnWindows(client)
			if got != tc.want {
				t.Fatalf("remoteAppServerRunsOnWindows(%q) = %v, want %v", tc.platformOS, got, tc.want)
			}
		})
	}
}

func TestRemoteStatusAccountRetainsServerAuthRequirementLikeRust(t *testing.T) {
	response := &auth.GetAccountResponse{
		Account:            &auth.Account{Type: auth.AccountAPIKey},
		RequiresOpenAIAuth: false,
	}
	display, hasChatGPTAccount, requiresOpenAIAuth := remoteStatusAccountValues(response)
	if display != "API key configured (run codex login to use ChatGPT)" || hasChatGPTAccount {
		t.Fatalf("account values = %q/%v", display, hasChatGPTAccount)
	}
	if requiresOpenAIAuth == nil || *requiresOpenAIAuth {
		t.Fatalf("requiresOpenAIAuth = %v, want false", requiresOpenAIAuth)
	}
}
