package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Rust #62110 allows the system requirements file to install an OTel headers
// helper, but only an absolute executable path: the client runs it from its
// installation directory instead of through a shell.
func TestManagedOtelHeadersHelperRequiresAbsolutePathLikeRust(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "requirements.toml")
	if err := os.WriteFile(path, []byte("[otel]\nheaders_helper = \"otel-headers\"\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	_, err := LoadRequirementsFile(path)
	if err == nil || !strings.Contains(err.Error(), "otel.headers_helper must be an absolute executable path") {
		t.Fatalf("LoadRequirementsFile() error = %v, want the absolute-path rejection", err)
	}

	absolute := filepath.Join(home, "bin", "otel-headers")
	contents := "[otel]\nheaders_helper = " + strconv.Quote(absolute) + "\nheaders_helper_refresh_interval_ms = 60000\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
	if requirements == nil || requirements.Otel == nil {
		t.Fatal("requirements did not carry the otel layer")
	}
	if got := requirements.Otel.HeadersHelper; got == nil || *got != absolute {
		t.Fatalf("headers_helper = %v, want %q", got, absolute)
	}
	if got := requirements.Otel.HeadersHelperRefreshIntervalMS; got == nil || *got != 60000 {
		t.Fatalf("headers_helper_refresh_interval_ms = %v, want 60000", got)
	}
	if !requirements.Otel.ManagedHeadersHelper() {
		t.Fatal("managed requirements did not mark the helper as managed")
	}

	// The requirements API exposes both fields (Rust #62110).
	wire := otelRequirementsForWire(requirements.Otel)
	if wire == nil || wire.HeadersHelper == nil || *wire.HeadersHelper != absolute {
		t.Fatalf("wire headers_helper = %#v", wire)
	}
	if wire.HeadersHelperRefreshIntervalMS == nil || *wire.HeadersHelperRefreshIntervalMS != 60000 {
		t.Fatalf("wire headers_helper_refresh_interval_ms = %v", wire.HeadersHelperRefreshIntervalMS)
	}
}

// A zero refresh interval is not a duration, mirroring Rust's NonZeroU64 field.
func TestManagedOtelHeadersHelperRejectsZeroRefreshIntervalLikeRust(t *testing.T) {
	_, err := ParseRequirementsTOML([]byte("[otel]\nheaders_helper = \"/opt/company/otel-headers\"\nheaders_helper_refresh_interval_ms = 0\n"))
	if err == nil || !strings.Contains(err.Error(), "must be a positive integer") {
		t.Fatalf("ParseRequirementsTOML() error = %v, want the positive-interval rejection", err)
	}
}

func mustParseOtelRequirements(t *testing.T, contents string) *OtelRequirements {
	t.Helper()
	requirements, err := ParseRequirementsTOML([]byte(contents))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML(%q) error = %v", contents, err)
	}
	if requirements == nil || requirements.Otel == nil {
		t.Fatalf("ParseRequirementsTOML(%q) did not produce an otel layer", contents)
	}
	return requirements.Otel
}

// Rust #62110: a managed helper only serves OTLP HTTP exporters with nonempty
// endpoints configured in the same requirements layer.
func TestManagedOtelHeadersHelperTargetsFollowLayerLikeRust(t *testing.T) {
	managed := mustParseOtelRequirements(t, `[otel]
headers_helper = "/opt/company/otel-headers"
[otel.exporter.otlp-http]
endpoint = "https://collector.example/v1/logs"
protocol = "binary"
`)
	targets := managed.HeadersHelperTargets()
	if !targets.Logs || targets.Traces || targets.Metrics {
		t.Fatalf("targets = %+v, want logs only", targets)
	}

	// An empty HTTP endpoint cannot carry the helper's credentials.
	empty := mustParseOtelRequirements(t, `[otel]
headers_helper = "/opt/company/otel-headers"
[otel.exporter.otlp-http]
endpoint = ""
protocol = "binary"
`)
	if got := empty.HeadersHelperTargets(); got.Logs || got.Traces || got.Metrics {
		t.Fatalf("empty-endpoint targets = %+v, want no authorized signal", got)
	}

	// A gRPC collector never receives the helper.
	grpc := mustParseOtelRequirements(t, `[otel]
headers_helper = "/opt/company/otel-headers"
[otel.exporter.otlp-grpc]
endpoint = "https://collector.example:4317"
`)
	if got := grpc.HeadersHelperTargets(); got.Logs || got.Traces || got.Metrics {
		t.Fatalf("grpc targets = %+v, want no authorized signal", got)
	}

	// Replacing the helper revokes the previous binding.
	replacedHelper := mergeOtelRequirements(managed, &OtelRequirements{HeadersHelper: otelStringPtr("/opt/other/otel-headers")})
	if got := replacedHelper.HeadersHelperTargets(); got.Logs || got.Traces || got.Metrics {
		t.Fatalf("replaced-helper targets = %+v, want the binding revoked", got)
	}

	// Replacing the exporter revokes it too, and re-binding both restores it.
	replacedExporter := mergeOtelRequirements(managed, &OtelRequirements{
		Exporter: &OtelExporter{Kind: OtelExporterKindOtlpGRPC, Endpoint: "https://collector.example:4317"},
	})
	if got := replacedExporter.HeadersHelperTargets(); got.Logs || got.Traces || got.Metrics {
		t.Fatalf("replaced-exporter targets = %+v, want the binding revoked", got)
	}
	rebound := mergeOtelRequirements(managed, &OtelRequirements{
		HeadersHelper: otelStringPtr("/opt/company/otel-headers"),
		Exporter: &OtelExporter{
			Kind:     OtelExporterKindOtlpHTTP,
			Endpoint: "https://collector.example/v1/logs",
			Protocol: OtelHTTPProtocolBinary,
		},
	})
	if got := rebound.HeadersHelperTargets(); !got.Logs || got.Traces || got.Metrics {
		t.Fatalf("rebound targets = %+v, want logs only", got)
	}
}

// A user helper serves the signals no managed exporter claims.
func TestUserOtelHeadersHelperTargetsSkipManagedExportersLikeRust(t *testing.T) {
	managedWithoutHelper := mustParseOtelRequirements(t, `[otel]
log_user_prompt = false
`)
	if got := managedWithoutHelper.HeadersHelperTargets(); !got.Logs || !got.Traces || !got.Metrics {
		t.Fatalf("targets = %+v, want every signal", got)
	}
	if managedWithoutHelper.ManagedHeadersHelper() {
		t.Fatal("a layer without headers_helper must not report a managed helper")
	}

	managedExporter := mustParseOtelRequirements(t, `[otel]
log_user_prompt = false
[otel.exporter.otlp-http]
endpoint = "https://collector.example/v1/logs"
protocol = "binary"
`)
	if got := managedExporter.HeadersHelperTargets(); got.Logs || !got.Traces || !got.Metrics {
		t.Fatalf("targets = %+v, want the managed log exporter excluded", got)
	}
}

// Rust #62110 rejects helper settings from fetched policy: only the machine's
// own requirements may name an executable.
func TestCloudRequirementsRejectOtelHeadersHelperLikeRust(t *testing.T) {
	for _, contents := range []string{
		"[otel]\nheaders_helper = \"/opt/company/otel-headers\"\n",
		"[otel]\nheaders_helper_refresh_interval_ms = 60000\n",
	} {
		bundle := CloudConfigBundle{
			RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
				ID:       "remote",
				Name:     "remote",
				Contents: contents,
			}}},
		}
		if _, err := applyCloudConfigBundle(map[string]any{}, nil, bundle, t.TempDir()); err == nil ||
			!errors.Is(err, ErrInvalidCloudConfig) ||
			!strings.Contains(err.Error(), "OTel headers helpers may only be configured in local managed requirements") {
			t.Fatalf("applyCloudConfigBundle(%q) error = %v, want the cloud rejection", contents, err)
		}
	}
}

func otelStringPtr(value string) *string { return &value }
