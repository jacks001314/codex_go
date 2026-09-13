package telemetry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
