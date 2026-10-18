package model

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestProviderDiagnosticsRedactCredentialsLikeRust(t *testing.T) {
	const (
		bearer   = "synthetic-provider-bearer"
		header   = "synthetic-provider-header"
		query    = "synthetic-provider-query"
		authArg  = "synthetic-provider-auth-arg"
		refresh  = "synthetic-provider-refresh"
		export   = "synthetic-provider-export"
		response = "synthetic-refreshed-token"
	)
	provider := ProviderInfo{
		Name:                    "custom",
		BaseURL:                 "https://example.test/v1",
		ExperimentalBearerToken: bearer,
		Auth: &ProviderAuthInfo{
			Command: "token-helper",
			Args:    []string{"--token", authArg},
		},
		QueryParams: map[string]string{"api-key": query},
		HTTPHeaders: map[string]string{"X-Credential": header},
		AWS: &ProviderAWSAuthInfo{
			CredentialExport: &ProviderCredentialExportInfo{
				Command: "credential-helper",
				Args:    []string{"--credential", export},
			},
			AuthRefresh: &ProviderAuthRefreshInfo{
				Command: "refresh-helper",
				Args:    []string{"--token", refresh},
			},
		},
	}
	apiProvider := APIProvider{
		Name:        provider.Name,
		BaseURL:     provider.BaseURL,
		QueryParams: provider.QueryParams,
		Headers:     map[string][]string{"X-Credential": {header}},
		Auth:        provider.Auth,
	}
	refreshResponse := ExternalAuthRefreshResponse{
		AccessToken:      response,
		ChatGPTAccountID: "account-1",
	}

	secrets := []string{bearer, header, query, authArg, refresh, export, response}
	for _, value := range []any{provider, &provider, apiProvider, &apiProvider, provider.Auth, provider.AWS.AuthRefresh, refreshResponse} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			assertDiagnosticRedacted(t, fmt.Sprintf(format, value), secrets)
		}
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	slog.Warn("provider diagnostics", "provider", provider, "api_provider", apiProvider, "refresh", refreshResponse)
	assertDiagnosticRedacted(t, logs.String(), secrets)
}

func assertDiagnosticRedacted(t *testing.T, got string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic output leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("diagnostic output omitted redaction marker: %s", got)
	}
}
