package telemetry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// rawRecordingHTTPDoer captures the undecoded OTLP/HTTP payload.
type rawRecordingHTTPDoer struct {
	mu          sync.Mutex
	bodies      [][]byte
	contentType []string
}

func (d *rawRecordingHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.bodies = append(d.bodies, body)
	d.contentType = append(d.contentType, request.Header.Get("Content-Type"))
	d.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
}

// The OTLP/HTTP binary protocol sends the protobuf ExportMetricsServiceRequest
// built from the authoritative proto definitions.
func TestOTLPExporterBinaryProtocolLikeRust(t *testing.T) {
	doer := &rawRecordingHTTPDoer{}
	client := NewMetricsClient(MetricsClientOptions{
		Endpoint:       "https://metrics.test/v1/metrics",
		Protocol:       OtelHTTPProtocolBinary,
		HTTPClient:     doer,
		ServiceName:    "codex",
		ServiceVersion: "1.2.3",
		Environment:    "test",
		ExportInterval: -1,
	})
	if !client.Enabled() {
		t.Fatal("the binary client is disabled")
	}
	client.Counter("codex.turn.tool.call", 2, map[string]string{"tool": "shell"})
	client.HistogramWithBounds("codex.turn.e2e_duration_ms", 7, []float64{5, 10}, nil)
	client.Gauge("codex.turn.unified_exec.running_processes", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	doer.mu.Lock()
	bodies := append([][]byte(nil), doer.bodies...)
	contentTypes := append([]string(nil), doer.contentType...)
	doer.mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("bodies = %d", len(bodies))
	}
	if contentTypes[0] != "application/x-protobuf" {
		t.Fatalf("content type = %q", contentTypes[0])
	}
	decoded := &collectormetricspb.ExportMetricsServiceRequest{}
	if err := proto.Unmarshal(bodies[0], decoded); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if len(decoded.ResourceMetrics) != 1 {
		t.Fatalf("resource metrics = %#v", decoded.ResourceMetrics)
	}
	resource := decoded.ResourceMetrics[0].GetResource()
	if got := protoAttributeValues(resource.GetAttributes()); got["service.name"] != "codex" ||
		got["service.version"] != "1.2.3" || got["env"] != "test" {
		t.Fatalf("resource attributes = %#v", got)
	}
	scopeMetrics := decoded.ResourceMetrics[0].GetScopeMetrics()
	if len(scopeMetrics) != 1 || scopeMetrics[0].GetScope().GetName() != MetricsMeterName {
		t.Fatalf("scope metrics = %#v", scopeMetrics)
	}
	metrics := map[string]*metricspb.Metric{}
	for _, metric := range scopeMetrics[0].GetMetrics() {
		metrics[metric.GetName()] = metric
	}
	counter := metrics["codex.turn.tool.call"]
	if counter.GetSum() == nil || counter.GetSum().GetIsMonotonic() != true ||
		counter.GetSum().GetAggregationTemporality().String() != "AGGREGATION_TEMPORALITY_DELTA" {
		t.Fatalf("counter = %#v", counter)
	}
	if point := counter.GetSum().GetDataPoints()[0]; point.GetAsInt() != 2 ||
		protoAttributeValues(point.GetAttributes())["tool"] != "shell" {
		t.Fatalf("counter point = %#v", point)
	}
	histogram := metrics["codex.turn.e2e_duration_ms"].GetHistogram()
	if histogram.GetAggregationTemporality().String() != "AGGREGATION_TEMPORALITY_DELTA" {
		t.Fatalf("histogram = %#v", histogram)
	}
	histogramPoint := histogram.GetDataPoints()[0]
	if histogramPoint.GetCount() != 1 || histogramPoint.GetSum() != 7 ||
		len(histogramPoint.GetExplicitBounds()) != 2 ||
		len(histogramPoint.GetBucketCounts()) != 3 || histogramPoint.GetBucketCounts()[1] != 1 {
		t.Fatalf("histogram point = %#v", histogramPoint)
	}
	if gauge := metrics["codex.turn.unified_exec.running_processes"].GetGauge(); gauge == nil ||
		gauge.GetDataPoints()[0].GetAsInt() != 1 {
		t.Fatalf("gauge = %#v", gauge)
	}
}

func protoAttributeValues(attributes []*commonpb.KeyValue) map[string]string {
	values := map[string]string{}
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		values[attribute.GetKey()] = attribute.GetValue().GetStringValue()
	}
	return values
}

func sampleMetricsRequest() OTLPExportMetricsRequest {
	value := int64(1)
	return OTLPExportMetricsRequest{
		ResourceMetrics: []OTLPResourceMetrics{{
			ScopeMetrics: []OTLPScopeMetrics{{
				Scope: OTLPScope{Name: MetricsMeterName},
				Metrics: []OTLPMetric{{
					Name: ThreadStartedMetric,
					Sum: []OTLPNumberDataPoint{{
						AsInt:             &value,
						StartTimeUnixNano: "1",
						TimeUnixNano:      "2",
					}},
					Monotonic: true,
				}},
			}},
		}},
	}
}

// The configured CA certificate is trusted (and, as in Rust, the built-in roots
// are disabled), so a self-signed OTLP HTTPS endpoint works.
func TestOTLPExporterTrustsConfiguredCACertificate(t *testing.T) {
	received := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	caPath := writePEMFile(t, "ca.pem", pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}))
	exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: server.URL + "/v1/metrics",
		TLS:      &OTLPHTTPTLSConfig{CACertificate: caPath},
	})
	if exporter == nil {
		t.Fatal("exporter is disabled")
	}
	if err := exporter.Export(context.Background(), sampleMetricsRequest()); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("the TLS server did not receive the export")
	}
}

// With a configured CA the built-in roots are not used, so an endpoint whose
// certificate is signed by anything else fails verification.
func TestOTLPExporterDisablesBuiltInRootsWithConfiguredCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	certificatePEM, _ := selfSignedCertificate(t)
	caPath := writePEMFile(t, "other-ca.pem", certificatePEM)
	exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: server.URL + "/v1/metrics",
		TLS:      &OTLPHTTPTLSConfig{CACertificate: caPath},
	})
	if exporter == nil {
		t.Fatal("exporter is disabled")
	}
	if err := exporter.Export(context.Background(), sampleMetricsRequest()); err == nil {
		t.Fatal("Export() trusted a certificate that is not signed by the configured CA")
	}
}

// mTLS requires both halves of the client identity, and an mTLS exporter refuses
// a plain-HTTP endpoint.
func TestOTLPExporterMTLSRequiresBothHalvesAndHTTPS(t *testing.T) {
	certificatePEM, privateKeyPEM := selfSignedCertificate(t)
	certPath := writePEMFile(t, "client.pem", certificatePEM)
	keyPath := writePEMFile(t, "client.key", privateKeyPEM)

	if exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: "https://metrics.test/v1/metrics",
		TLS:      &OTLPHTTPTLSConfig{ClientCertificate: certPath},
	}); exporter != nil {
		t.Fatal("a client certificate without a private key built an exporter")
	}
	if exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: "https://metrics.test/v1/metrics",
		TLS:      &OTLPHTTPTLSConfig{ClientPrivateKey: keyPath},
	}); exporter != nil {
		t.Fatal("a private key without a client certificate built an exporter")
	}

	exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: "http://metrics.test/v1/metrics",
		TLS: &OTLPHTTPTLSConfig{
			ClientCertificate: certPath,
			ClientPrivateKey:  keyPath,
		},
	})
	if exporter == nil {
		t.Fatal("a complete client identity did not build an exporter")
	}
	if err := exporter.Export(context.Background(), sampleMetricsRequest()); err == nil {
		t.Fatal("an mTLS exporter accepted a plain-HTTP endpoint")
	}
}

// A missing CA file disables the exporter instead of exporting without the
// requested trust configuration.
func TestOTLPExporterMissingCACertificateIsDisabled(t *testing.T) {
	exporter := NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint: "https://metrics.test/v1/metrics",
		TLS:      &OTLPHTTPTLSConfig{CACertificate: filepath.Join(t.TempDir(), "missing.pem")},
	})
	if exporter != nil {
		t.Fatal("a missing CA certificate built an exporter")
	}
}

func writePEMFile(t *testing.T, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
	return path
}

func selfSignedCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "codex-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certificatePEM, privateKeyPEM
}
