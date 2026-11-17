package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var otlpCompressionEnvKeys = []string{
	MetricsExporterSignalCompressionEnv,
	MetricsExporterCompressionEnv,
	MetricsExporterSignalHeadersEnv,
	MetricsExporterHeadersEnv,
}

func unsetOTLPEnv(t *testing.T) {
	t.Helper()
	for _, key := range otlpCompressionEnvKeys {
		key := key
		previous, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, previous)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}

// TestResolveOTLPHTTPCompressionLikeRust pins Rust #61911: the built-in Statsig
// route defaults to Zstandard, an explicit compression environment variable
// wins, and header configuration leaves the choice to the operator.
func TestResolveOTLPHTTPCompressionLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		statsig bool
		want    string
	}{
		{name: "statsig defaults to zstd", statsig: true, want: OtlpCompressionZstd},
		{name: "custom exporter stays uncompressed", want: OtlpCompressionNone},
		{name: "general compression override", env: map[string]string{MetricsExporterCompressionEnv: "gzip"}, statsig: true, want: OtlpCompressionGzip},
		{name: "signal compression wins", env: map[string]string{MetricsExporterCompressionEnv: "gzip", MetricsExporterSignalCompressionEnv: "zstd"}, statsig: true, want: OtlpCompressionZstd},
		{name: "explicit none", env: map[string]string{MetricsExporterSignalCompressionEnv: "none"}, statsig: true, want: OtlpCompressionNone},
		{name: "unknown value", env: map[string]string{MetricsExporterCompressionEnv: "brotli"}, statsig: true, want: OtlpCompressionNone},
		{name: "headers disable the default", env: map[string]string{MetricsExporterHeadersEnv: "a=b"}, statsig: true, want: OtlpCompressionNone},
		{name: "signal headers disable the default", env: map[string]string{MetricsExporterSignalHeadersEnv: "a=b"}, statsig: true, want: OtlpCompressionNone},
		{name: "compression beats headers", env: map[string]string{MetricsExporterHeadersEnv: "a=b", MetricsExporterCompressionEnv: "zstd"}, statsig: true, want: OtlpCompressionZstd},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			unsetOTLPEnv(t)
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}
			if got := resolveOTLPHTTPCompression(testCase.statsig); got != testCase.want {
				t.Fatalf("compression = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestMetricsClientCompressesStatsigExportsLikeRust verifies the wire format:
// the built-in route sets Content-Encoding: zstd while a configured exporter
// stays uncompressed.
func TestMetricsClientCompressesStatsigExportsLikeRust(t *testing.T) {
	unsetOTLPEnv(t)
	previous := StatsigMetricsRouteEnabled
	StatsigMetricsRouteEnabled = "true"
	defer func() { StatsigMetricsRouteEnabled = previous }()

	doer := &recordingHTTPDoer{}
	client := NewStatsigMetricsClient(MetricsClientOptions{
		HTTPClient:     doer,
		ServiceName:    "codex",
		ServiceVersion: "1.2.3",
		Environment:    "test",
		ExportInterval: -1,
	})
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if got := requests[0].headers.Get("Content-Encoding"); got != OtlpCompressionZstd {
		t.Fatalf("Content-Encoding = %q, want %q", got, OtlpCompressionZstd)
	}
	if _, ok := metricsByName(t, requests[0])["codex.thread.started"]; !ok {
		t.Fatal("compressed Statsig export lost its metric")
	}

	customDoer := &recordingHTTPDoer{}
	custom := newTestMetricsClient(t, customDoer, MetricsClientOptions{})
	custom.Counter("codex.thread.started", 1, nil)
	if err := custom.Flush(context.Background()); err != nil {
		t.Fatalf("custom Flush() error = %v", err)
	}
	if got := customDoer.snapshot()[0].headers.Get("Content-Encoding"); got != "" {
		t.Fatalf("custom Content-Encoding = %q, want none", got)
	}
}

// TestMetricsClientHonorsCompressionEnvLikeRust pins the operator override:
// OTEL_EXPORTER_OTLP_METRICS_COMPRESSION selects the encoder for the Statsig
// route as well.
func TestMetricsClientHonorsCompressionEnvLikeRust(t *testing.T) {
	unsetOTLPEnv(t)
	t.Setenv(MetricsExporterSignalCompressionEnv, "gzip")
	previous := StatsigMetricsRouteEnabled
	StatsigMetricsRouteEnabled = "true"
	defer func() { StatsigMetricsRouteEnabled = previous }()

	doer := &recordingHTTPDoer{}
	client := NewStatsigMetricsClient(MetricsClientOptions{
		HTTPClient:     doer,
		ServiceName:    "codex",
		ServiceVersion: "1.2.3",
		Environment:    "test",
		ExportInterval: -1,
	})
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if got := requests[0].headers.Get("Content-Encoding"); got != OtlpCompressionGzip {
		t.Fatalf("Content-Encoding = %q, want %q", got, OtlpCompressionGzip)
	}
}

// TestCompressMetricsPayloadRoundTripLikeRust checks both encoders produce a
// body the matching decoder restores byte for byte.
func TestCompressMetricsPayloadRoundTripLikeRust(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[]}`)
	compressed, err := compressMetricsPayload(payload, OtlpCompressionZstd)
	if err != nil {
		t.Fatalf("zstd compress: %v", err)
	}
	decoder, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	restored, err := io.ReadAll(decoder)
	decoder.Close()
	if err != nil || !bytes.Equal(restored, payload) {
		t.Fatalf("zstd round trip = %q, %v", restored, err)
	}
	compressed, err = compressMetricsPayload(payload, OtlpCompressionGzip)
	if err != nil {
		t.Fatalf("gzip compress: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	restored, err = io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(restored, payload) {
		t.Fatalf("gzip round trip = %q, %v", restored, err)
	}
	unchanged, err := compressMetricsPayload(payload, OtlpCompressionNone)
	if err != nil || !bytes.Equal(unchanged, payload) {
		t.Fatalf("uncompressed payload = %q, %v", unchanged, err)
	}
}
