package otelinit

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"codex_go/config"
)

func parseOtelRequirements(t *testing.T, contents string) *config.OtelRequirements {
	t.Helper()
	requirements, err := config.ParseRequirementsTOML([]byte(contents))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.Otel == nil {
		t.Fatal("ParseRequirementsTOML() produced no OTEL layer")
	}
	return requirements.Otel
}

// Rust #62110 resolves a managed helper from the requirements layer, which also
// decides which exporters may receive its credentials.
func TestManagedOtelHeadersHelperConfigFollowsRequirementsLikeRust(t *testing.T) {
	command := filepath.Join(t.TempDir(), "otel-headers")
	managed := parseOtelRequirements(t, "[otel]\nheaders_helper = "+strconv.Quote(command)+
		"\n[otel.exporter.otlp-http]\nendpoint = \"https://collector.example/v1/logs\"\nprotocol = \"binary\"\n")
	resolved := config.OtelConfig{HeadersHelper: command, HeadersHelperRefreshIntervalMS: 60000}

	got := otelHeadersHelperConfig(resolved, Options{CodexHome: t.TempDir()}, managed)
	if got == nil {
		t.Fatal("otelHeadersHelperConfig() = nil, want the managed helper")
	}
	if !got.Managed {
		t.Fatal("managed requirements did not mark the helper as managed")
	}
	if !got.ApplyToLogs || got.ApplyToTraces || got.ApplyToMetrics {
		t.Fatalf("apply flags = %+v, want logs only", got)
	}
	if got.Command != command {
		t.Fatalf("command = %q, want %q", got.Command, command)
	}
	if got.RefreshInterval != time.Minute {
		t.Fatalf("refresh interval = %v, want 1m", got.RefreshInterval)
	}

	// The same command from the user's own configuration serves every signal and
	// runs through the user shell.
	user := otelHeadersHelperConfig(resolved, Options{CodexHome: t.TempDir()}, nil)
	if user == nil {
		t.Fatal("otelHeadersHelperConfig() = nil, want the user helper")
	}
	if user.Managed {
		t.Fatal("a user helper must not be marked managed")
	}
	if !user.ApplyToLogs || !user.ApplyToTraces || !user.ApplyToMetrics {
		t.Fatalf("user apply flags = %+v, want every signal", user)
	}

	// A managed layer without a helper excludes the exporters it governs from the
	// user's own helper.
	managedExporterOnly := parseOtelRequirements(t,
		"[otel]\nlog_user_prompt = false\n[otel.exporter.otlp-http]\nendpoint = \"https://collector.example/v1/logs\"\nprotocol = \"binary\"\n")
	mixed := otelHeadersHelperConfig(resolved, Options{CodexHome: t.TempDir()}, managedExporterOnly)
	if mixed == nil || mixed.Managed {
		t.Fatalf("mixed helper = %+v, want an unmanaged helper", mixed)
	}
	if mixed.ApplyToLogs || !mixed.ApplyToTraces || !mixed.ApplyToMetrics {
		t.Fatalf("mixed apply flags = %+v, want the managed log exporter excluded", mixed)
	}

	if managedOtelRequirements(Options{}) != nil {
		t.Fatal("managedOtelRequirements() = non-nil for a nil config")
	}
	withRequirements := Options{Config: &config.Config{Requirements: &config.ConfigRequirements{Otel: managed}}}
	if got := managedOtelRequirements(withRequirements); got != managed {
		t.Fatalf("managedOtelRequirements() = %v, want the requirements OTEL layer", got)
	}
}
