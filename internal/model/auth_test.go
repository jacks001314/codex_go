package model

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"codex_go/internal/agent"
	"codex_go/internal/auth"
)

func TestResolveProviderAuthUsesProviderEnvKeyBeforeCodexAuth(t *testing.T) {
	t.Setenv("CUSTOM_API_KEY", "provider-token")
	headers, err := ResolveProviderAuth(&auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "openai-token",
	}, ProviderInfo{EnvKey: "CUSTOM_API_KEY"})
	if err != nil {
		t.Fatalf("ResolveProviderAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer provider-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveProviderAuthUsesExperimentalBearerToken(t *testing.T) {
	headers, err := ResolveProviderAuth(nil, ProviderInfo{ExperimentalBearerToken: "static-token"})
	if err != nil {
		t.Fatalf("ResolveProviderAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer static-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveProviderAuthUsesCommandAuth(t *testing.T) {
	info := &ProviderAuthInfo{TimeoutMS: 5000}
	if runtime.GOOS == "windows" {
		info.Command = "cmd"
		info.Args = []string{"/D", "/Q", "/C", "echo command-token"}
	} else {
		info.Command = "sh"
		info.Args = []string{"-c", "printf '%s\n' command-token"}
	}
	headers, err := ResolveProviderCommandAuth(context.Background(), info)
	if err != nil {
		t.Fatalf("ResolveProviderCommandAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer command-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveProviderAuthUsesCommandAuthJSON(t *testing.T) {
	info := &ProviderAuthInfo{TimeoutMS: 5000}
	if runtime.GOOS == "windows" {
		info.Command = "cmd"
		info.Args = []string{"/D", "/Q", "/C", "echo {\"authorization\":\"Bearer json-token\"}"}
	} else {
		info.Command = "sh"
		info.Args = []string{"-c", "printf '%s\n' '{\"authorization\":\"Bearer json-token\"}'"}
	}
	headers, err := ResolveProviderCommandAuth(context.Background(), info)
	if err != nil {
		t.Fatalf("ResolveProviderCommandAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer json-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveProviderAuthCommandErrorIncludesStderr(t *testing.T) {
	info := &ProviderAuthInfo{TimeoutMS: 5000}
	if runtime.GOOS == "windows" {
		info.Command = "cmd"
		info.Args = []string{"/D", "/Q", "/C", "echo auth failed 1>&2 & exit /b 3"}
	} else {
		info.Command = "sh"
		info.Args = []string{"-c", "printf '%s\n' 'auth failed' >&2; exit 3"}
	}
	_, err := ResolveProviderCommandAuth(context.Background(), info)
	if err == nil {
		t.Fatal("ResolveProviderCommandAuth returned nil error, want stderr failure")
	}
	if !strings.Contains(err.Error(), "provider auth command failed") || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveProviderAuthFallsBackToCodexAuth(t *testing.T) {
	headers, err := ResolveProviderAuth(&auth.AuthDotJSON{
		AuthMode:     "api-key",
		OpenAIAPIKey: "openai-token",
	}, ProviderInfo{})
	if err != nil {
		t.Fatalf("ResolveProviderAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer openai-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveProviderAuthRejectsBedrockAPIKeyForConfiguredProvider(t *testing.T) {
	_, err := ResolveProviderAuth(&auth.AuthDotJSON{
		AuthMode:      "bedrock-api-key",
		BedrockAPIKey: map[string]string{"api_key": "bedrock"},
	}, ProviderInfo{})
	if err == nil {
		t.Fatal("ResolveProviderAuth returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "Bedrock API key") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthHeadersFromChatGPTTokens(t *testing.T) {
	headers, err := AuthHeadersFromAuth(auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"access_token":       "chatgpt-token",
			"account_id":         "acct-123",
			"is_fedramp_account": true,
		},
	})
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer chatgpt-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Headers.Get("ChatGPT-Account-ID"); got != "acct-123" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	if got := headers.Headers.Get("X-OpenAI-Fedramp"); got != "true" {
		t.Fatalf("X-OpenAI-Fedramp = %q", got)
	}
}

func TestAuthHeadersFromPersonalAccessTokenUsesAccountRouting(t *testing.T) {
	headers, err := AuthHeadersFromAuth(auth.AuthDotJSON{
		PersonalAccessToken: "at-test-token",
		Tokens: map[string]any{
			"chatgpt_account_id":         "account-pat",
			"chatgpt_account_is_fedramp": true,
		},
	})
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer at-test-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Headers.Get("ChatGPT-Account-ID"); got != "account-pat" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	if got := headers.Headers.Get("X-OpenAI-Fedramp"); got != "true" {
		t.Fatalf("X-OpenAI-Fedramp = %q", got)
	}
}

func TestAuthHeadersFromExternalChatGPTTokens(t *testing.T) {
	headers, err := AuthHeadersFromAuth(auth.AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens: map[string]any{
			"access_token":       "external-token",
			"chatgpt_account_id": "acct-456",
		},
	})
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth returned error: %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer external-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Headers.Get("ChatGPT-Account-ID"); got != "acct-456" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
}

func TestAuthHeadersFromAgentIdentityDoesNotExposeBearer(t *testing.T) {
	_, err := AuthHeadersFromAuth(auth.AuthDotJSON{
		AuthMode:      "agent-identity",
		AgentIdentity: "jwt-token",
	})
	if err == nil || !strings.Contains(err.Error(), "agent identity auth does not expose a bearer token") {
		t.Fatalf("AuthHeadersFromAuth error = %v", err)
	}
}

func TestAuthHeadersFromAgentIdentityRecordUsesAgentAssertion(t *testing.T) {
	keyMaterial, err := agent.GenerateAgentKeyMaterial()
	if err != nil {
		t.Fatalf("GenerateAgentKeyMaterial() error = %v", err)
	}
	taskID := "task-1"
	headers, err := AuthHeadersFromAuth(auth.AuthDotJSON{
		AuthMode: "agent-identity",
		AgentIdentity: &auth.AgentIdentityAuthRecord{
			AgentRuntimeID:        "agent-1",
			AgentPrivateKey:       keyMaterial.PrivateKeyPKCS8Base64,
			AccountID:             "account-1",
			ChatGPTUserID:         "user-1",
			PlanType:              auth.PlanPro,
			ChatGPTAccountFedRAMP: true,
			TaskID:                &taskID,
		},
	})
	if err != nil {
		t.Fatalf("AuthHeadersFromAuth returned error: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://example.test/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	if err := headers.Apply(context.Background(), request, []byte("{}")); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "AgentAssertion ") {
		t.Fatalf("Authorization = %q", got)
	}
	if got := request.Header.Get("ChatGPT-Account-ID"); got != "account-1" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	if got := request.Header.Get("X-OpenAI-Fedramp"); got != "true" {
		t.Fatalf("X-OpenAI-Fedramp = %q", got)
	}
	if headers.AgentIdentityTelemetry == nil || headers.AgentIdentityTelemetry.AgentID != "agent-1" || headers.AgentIdentityTelemetry.TaskID != "task-1" {
		t.Fatalf("AgentIdentityTelemetry = %#v", headers.AgentIdentityTelemetry)
	}
}
