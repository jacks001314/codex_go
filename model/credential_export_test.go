package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProviderConfigParsesCredentialExportLikeRust(t *testing.T) {
	// Rust #44028: aws.credential_export carries a command, args, and timeout.
	providers, err := ProvidersFromConfig(map[string]any{
		"model_providers": map[string]any{
			"amazon-bedrock": map[string]any{
				"aws": map[string]any{
					"region": "us-west-2",
					"credential_export": map[string]any{
						"command":    "aws-credential-exporter",
						"args":       []any{"--format", "json"},
						"timeout_ms": 45000,
					},
				},
			},
		},
	}, "")
	if err != nil {
		t.Fatalf("ProvidersFromConfig returned error: %v", err)
	}
	bedrock := providers[AmazonBedrockProviderID]
	if bedrock.AWS == nil || bedrock.AWS.CredentialExport == nil {
		t.Fatalf("bedrock aws credential_export = %#v", bedrock.AWS)
	}
	export := bedrock.AWS.CredentialExport
	if export.Command != "aws-credential-exporter" || strings.Join(export.Args, " ") != "--format json" || export.TimeoutMS != 45000 {
		t.Fatalf("credential_export = %#v", export)
	}
}

func TestProviderConfigDefaultCredentialExportTimeout(t *testing.T) {
	normalized, err := providerInfoFromConfig(map[string]any{
		"aws": map[string]any{
			"credential_export": map[string]any{"command": "aws-credential-exporter"},
		},
	}, true)
	if err != nil {
		t.Fatalf("providerInfoFromConfig returned error: %v", err)
	}
	if normalized.AWS == nil || normalized.AWS.CredentialExport == nil {
		t.Fatalf("credential_export = %#v", normalized.AWS)
	}
	if normalized.AWS.CredentialExport.TimeoutMS != DefaultAWSCredentialExportTimeoutMS {
		t.Fatalf("default timeout = %d", normalized.AWS.CredentialExport.TimeoutMS)
	}
}

func TestProviderConfigRejectsCredentialExportLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		aws    map[string]any
		errMsg string
	}{
		{
			name: "profile conflict",
			aws: map[string]any{
				"profile":           "codex-bedrock",
				"credential_export": map[string]any{"command": "exporter"},
			},
			errMsg: "provider aws.credential_export cannot be combined with aws.profile",
		},
		{
			name:   "empty command",
			aws:    map[string]any{"credential_export": map[string]any{"command": "  "}},
			errMsg: "provider aws.credential_export.command must not be empty",
		},
		{
			name:   "relative command",
			aws:    map[string]any{"credential_export": map[string]any{"command": "./exporter"}},
			errMsg: "provider aws.credential_export.command must be an absolute path or a bare executable name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := providerInfoFromConfig(map[string]any{"aws": test.aws}, true)
			if err == nil || !strings.Contains(err.Error(), test.errMsg) {
				t.Fatalf("error = %v, want %q", err, test.errMsg)
			}
		})
	}
}

func TestParseAWSCredentialOutputFormats(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
		key  string
	}{
		{
			name: "flat credential process output",
			body: `{"Version":1,"AccessKeyId":"flat-access","SecretAccessKey":"flat-secret","SessionToken":"flat-session","Expiration":"2099-01-01T00:00:00Z"}`,
			key:  "flat-access",
		},
		{
			name: "nested STS output",
			body: `{"Credentials":{"AccessKeyId":"nested-access","SecretAccessKey":"nested-secret","SessionToken":"nested-session","Expiration":"2099-01-01T00:00:00Z"}}`,
			key:  "nested-access",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cached, err := parseAWSCredentialOutput([]byte(test.body), now)
			if err != nil {
				t.Fatalf("parseAWSCredentialOutput returned error: %v", err)
			}
			if cached.keys.AccessKeyID != test.key {
				t.Fatalf("access key = %q", cached.keys.AccessKeyID)
			}
			// 5 minutes before a 2099 expiration.
			wantRefresh := time.Date(2098, 12, 31, 23, 55, 0, 0, time.UTC)
			if !cached.refreshAt.Equal(wantRefresh) {
				t.Fatalf("refreshAt = %s, want %s", cached.refreshAt, wantRefresh)
			}
		})
	}
}

func TestParseAWSCredentialOutputDefaultsLifetime(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	cached, err := parseAWSCredentialOutput([]byte(`{"AccessKeyId":"a","SecretAccessKey":"b"}`), now)
	if err != nil {
		t.Fatalf("parseAWSCredentialOutput returned error: %v", err)
	}
	if !cached.refreshAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("refreshAt = %s", cached.refreshAt)
	}
}

func TestParseAWSCredentialOutputRejectsInvalid(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		body   string
		errMsg string
	}{
		{name: "not json", body: "not-json", errMsg: "invalid credentials JSON"},
		{name: "empty keys", body: `{"AccessKeyId":"","SecretAccessKey":""}`, errMsg: "empty access keys"},
		{name: "bad expiration", body: `{"AccessKeyId":"a","SecretAccessKey":"b","Expiration":"soon"}`, errMsg: "RFC 3339"},
		{name: "expired", body: `{"AccessKeyId":"a","SecretAccessKey":"b","Expiration":"2020-01-01T00:00:00Z"}`, errMsg: "expired credentials"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAWSCredentialOutput([]byte(test.body), now)
			if err == nil || !strings.Contains(err.Error(), test.errMsg) {
				t.Fatalf("error = %v, want %q", err, test.errMsg)
			}
		})
	}
}

func TestAWSCredentialExportProviderCachesAndRefreshes(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "counter")
	t.Setenv("GO_WANT_AWS_CREDENTIAL_EXPORT_HELPER", "1")
	t.Setenv("AWS_CREDENTIAL_EXPORT_HELPER_COUNTER", counterPath)

	provider := &AWSCredentialExportProvider{
		command: os.Args[0],
		args:    []string{"-test.run=TestAWSCredentialExportHelper"},
		timeout: 30 * time.Second,
	}
	first, err := provider.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials returned error: %v", err)
	}
	if first.AccessKeyID != "export-access-1" {
		t.Fatalf("first access key = %q", first.AccessKeyID)
	}
	// Second call is served from cache and must not run the command again.
	second, err := provider.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials returned error: %v", err)
	}
	if second.AccessKeyID != "export-access-1" {
		t.Fatalf("cached access key = %q", second.AccessKeyID)
	}
	if got := readCredentialExportCounter(t, counterPath); got != 1 {
		t.Fatalf("export invocations = %d, want 1", got)
	}
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	third, err := provider.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials returned error: %v", err)
	}
	if third.AccessKeyID != "export-access-2" {
		t.Fatalf("refreshed access key = %q", third.AccessKeyID)
	}
}

func TestAWSCredentialExportProviderRejectsInvalidCommand(t *testing.T) {
	provider := &AWSCredentialExportProvider{command: "relative/dir/exporter", timeout: time.Second}
	_, err := provider.Credentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path or a bare executable name") {
		t.Fatalf("error = %v", err)
	}
}

func TestAWSCredentialExportHelper(t *testing.T) {
	if os.Getenv("GO_WANT_AWS_CREDENTIAL_EXPORT_HELPER") != "1" {
		return
	}
	count := readCredentialExportCounter(t, os.Getenv("AWS_CREDENTIAL_EXPORT_HELPER_COUNTER")) + 1
	if counterPath := os.Getenv("AWS_CREDENTIAL_EXPORT_HELPER_COUNTER"); counterPath != "" {
		_ = os.WriteFile(counterPath, []byte(strconv.Itoa(count)), 0o600)
	}
	fmt.Fprintf(os.Stdout, `{"AccessKeyId":"export-access-%d","SecretAccessKey":"export-secret","SessionToken":"export-session"}`, count)
	os.Exit(0)
}

func readCredentialExportCounter(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return value
}
