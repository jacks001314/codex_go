package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	managedOtelHelperEnv     = "CODEX_GO_TEST_MANAGED_OTEL_HELPER"
	managedOtelHelperModeEnv = "CODEX_GO_TEST_MANAGED_OTEL_HELPER_MODE"
)

// The managed helper test runs the test binary itself as the helper executable:
// the child reports its own working directory so the caller can prove the
// managed command runs from its installation directory (Rust #62110).
func init() {
	if os.Getenv(managedOtelHelperEnv) != "1" {
		return
	}
	mode, _ := os.ReadFile(os.Getenv(managedOtelHelperModeEnv))
	if strings.TrimSpace(string(mode)) != "ok" {
		fmt.Fprintln(os.Stderr, "managed otel helper is not ready")
		os.Exit(3)
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(4)
	}
	payload, err := json.Marshal(map[string]string{
		"Authorization": "Bearer managed",
		"X-Helper-CWD":  cwd,
	})
	if err != nil {
		os.Exit(5)
	}
	os.Stdout.Write(payload)
	os.Exit(0)
}

// Rust #62110 requires an absolute executable for a managed helper.
func TestManagedOtelHeadersHelperRejectsRelativeCommandLikeRust(t *testing.T) {
	helper, err := startOTelHeadersHelper(&OTelHeadersHelperConfig{
		Command:     "otel-headers",
		Managed:     true,
		ApplyToLogs: true,
	})
	if err == nil || !strings.Contains(err.Error(), "managed OTel helper requires an absolute executable path") {
		t.Fatalf("startOTelHeadersHelper() error = %v, want the absolute-path rejection", err)
	}
	if helper != nil {
		t.Fatal("a rejected managed helper must not start")
	}
}

// A missing managed executable suspends exports with a diagnostic naming the
// command instead of silently exporting unauthenticated.
func TestManagedOtelHeadersHelperReportsMissingExecutableLikeRust(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-otel-headers")
	helper, err := startOTelHeadersHelper(&OTelHeadersHelperConfig{
		Command:     missing,
		Managed:     true,
		ApplyToLogs: true,
	})
	if err != nil {
		t.Fatalf("startOTelHeadersHelper() error = %v", err)
	}
	defer helper.Close()
	_, err = helper.headersForExport(context.Background())
	if err == nil || !strings.Contains(err.Error(), "managed OTel helper "+missing) {
		t.Fatalf("headersForExport() error = %v, want the missing-executable diagnostic", err)
	}
}

// The managed helper runs from its installation directory, suspends exports
// while it fails, and resumes once it recovers.
func TestManagedOtelHeadersHelperRunsFromInstallDirectoryAndRecoversLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	installDir := filepath.Dir(executable)
	modePath := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(modePath, []byte("fail"), 0o600); err != nil {
		t.Fatalf("write mode: %v", err)
	}
	t.Setenv(managedOtelHelperEnv, "1")
	t.Setenv(managedOtelHelperModeEnv, modePath)

	helper, err := startOTelHeadersHelper(&OTelHeadersHelperConfig{
		Command:         executable,
		CWD:             t.TempDir(),
		RefreshInterval: 10 * time.Millisecond,
		Managed:         true,
		ApplyToLogs:     true,
	})
	if err != nil {
		t.Fatalf("startOTelHeadersHelper() error = %v", err)
	}
	defer helper.Close()
	if _, err := helper.headersForExport(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "managed OTel helper "+executable) {
		t.Fatalf("failing refresh error = %v, want the managed-helper diagnostic", err)
	}

	if err := os.WriteFile(modePath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write mode: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var headers map[string]string
	for time.Now().Before(deadline) {
		current, err := helper.headersForExport(context.Background())
		if err == nil && current["Authorization"] == "Bearer managed" {
			headers = current
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if headers == nil {
		t.Fatal("managed helper did not recover after the executable started succeeding")
	}
	if got := headers["X-Helper-Cwd"]; got != installDir {
		t.Fatalf("helper working directory = %q, want its installation directory %q", got, installDir)
	}
}

// A signal the requirements layer did not authorize keeps its static headers.
func TestOtelSettingsGateHelperPerSignalLikeRust(t *testing.T) {
	helper := &otelHeadersHelper{}
	settings := OtelSettings{
		HeadersHelper: &OTelHeadersHelperConfig{ApplyToLogs: true},
		headersHelper: helper,
	}
	if got := settings.helperForExport(settings.HeadersHelper.appliesToLogs()); got != helper {
		t.Fatalf("logs helper = %v, want the shared helper", got)
	}
	if got := settings.helperForExport(settings.HeadersHelper.appliesToTraces()); got != nil {
		t.Fatalf("traces helper = %v, want no helper", got)
	}
	if got := settings.helperForExport(settings.HeadersHelper.appliesToMetrics()); got != nil {
		t.Fatalf("metrics helper = %v, want no helper", got)
	}
	unconfigured := OtelSettings{}
	if got := unconfigured.helperForExport(true); got != nil {
		t.Fatalf("helper = %v, want none without a helper config", got)
	}
}

// The provider wires each explicit exporter separately, so a managed helper
// bound to logs never reaches the trace or metric collectors (Rust #62110).
func TestOtelProviderAuthorizesHelperPerExporterLikeRust(t *testing.T) {
	endpoint := "http://127.0.0.1:4318"
	provider, err := NewOtelProvider(OtelSettings{
		Exporter:        OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: endpoint, Protocol: OtelHTTPProtocolBinary},
		TraceExporter:   OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: endpoint, Protocol: OtelHTTPProtocolBinary},
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: endpoint, Protocol: OtelHTTPProtocolBinary},
		HeadersHelper:   &OTelHeadersHelperConfig{Command: "otel-headers", ApplyToLogs: true},
	})
	if err != nil {
		t.Fatalf("NewOtelProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatal("NewOtelProvider() returned no provider")
	}
	defer provider.Shutdown(context.Background())
	logsExporter, ok := provider.logs.exporter.(*OTLPLogsExporter)
	if !ok || logsExporter.headerHelper == nil {
		t.Fatalf("the authorized log exporter did not receive the helper: %T", provider.logs.exporter)
	}
	if tracesExporter, ok := provider.traces.exporter.(*OTLPTracesExporter); ok && tracesExporter.headerHelper != nil {
		t.Fatal("the unauthorized trace exporter received the helper")
	}
	if metricsExporter, ok := provider.metrics.exporter.(*OTLPMetricsExporter); ok && metricsExporter.headerHelper != nil {
		t.Fatal("the unauthorized metric exporter received the helper")
	}
}
