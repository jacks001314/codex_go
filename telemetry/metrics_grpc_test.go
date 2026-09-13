package telemetry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"path/filepath"
	"testing"
	"time"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// captureMetricsService records the export requests an OTLP gRPC collector
// receives.
type captureMetricsService struct {
	collectormetricspb.UnimplementedMetricsServiceServer
	requests chan *collectormetricspb.ExportMetricsServiceRequest
	metadata chan metadata.MD
}

func newCaptureMetricsService() *captureMetricsService {
	return &captureMetricsService{
		requests: make(chan *collectormetricspb.ExportMetricsServiceRequest, 4),
		metadata: make(chan metadata.MD, 4),
	}
}

func (s *captureMetricsService) Export(ctx context.Context, request *collectormetricspb.ExportMetricsServiceRequest) (*collectormetricspb.ExportMetricsServiceResponse, error) {
	incoming, _ := metadata.FromIncomingContext(ctx)
	s.metadata <- incoming
	s.requests <- request
	return &collectormetricspb.ExportMetricsServiceResponse{}, nil
}

// The OTLP gRPC transport posts the protobuf batch over the metrics service and
// forwards the configured headers as request metadata.
func TestOTLPGRPCExporterExportsLikeRust(t *testing.T) {
	service := newCaptureMetricsService()
	address, stop := startMetricsGRPCServer(t, service)
	defer stop()

	client := NewMetricsClient(MetricsClientOptions{
		Transport:      MetricsTransportGRPC,
		Endpoint:       "http://" + address,
		Headers:        map[string]string{"authorization": "Bearer token"},
		ServiceName:    "codex",
		ServiceVersion: "1.2.3",
		Environment:    "test",
		ExportInterval: -1,
	})
	if !client.Enabled() {
		t.Fatal("the gRPC client is disabled")
	}
	client.Counter("codex.turn.tool.call", 2, map[string]string{"tool": "shell"})
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	select {
	case request := <-service.requests:
		if len(request.GetResourceMetrics()) != 1 {
			t.Fatalf("request = %#v", request)
		}
		resource := request.GetResourceMetrics()[0].GetResource()
		if got := protoAttributeValues(resource.GetAttributes()); got["service.name"] != "codex" ||
			got["service.version"] != "1.2.3" || got["env"] != "test" {
			t.Fatalf("resource attributes = %#v", got)
		}
		metrics := request.GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()
		if len(metrics) != 1 || metrics[0].GetName() != "codex.turn.tool.call" ||
			metrics[0].GetSum().GetDataPoints()[0].GetAsInt() != 2 {
			t.Fatalf("metrics = %#v", metrics)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the gRPC collector did not receive an export")
	}
	select {
	case incoming := <-service.metadata:
		if values := incoming.Get("authorization"); len(values) != 1 || values[0] != "Bearer token" {
			t.Fatalf("metadata = %#v", incoming)
		}
	default:
		t.Fatal("the configured header was not forwarded")
	}
}

// An https endpoint uses TLS with the configured CA certificate.
func TestOTLPGRPCExporterTLS(t *testing.T) {
	certificatePEM, privateKeyPEM := selfSignedServerCertificate(t)
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	service := newCaptureMetricsService()
	address, stop := startMetricsGRPCServer(t, service, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{certificate}})))
	defer stop()

	caPath := writePEMFile(t, "grpc-ca.pem", certificatePEM)
	client := NewMetricsClient(MetricsClientOptions{
		Transport:      MetricsTransportGRPC,
		Endpoint:       "https://" + address,
		TLS:            &OTLPHTTPTLSConfig{CACertificate: caPath},
		ServiceName:    "codex",
		ExportInterval: -1,
	})
	if !client.Enabled() {
		t.Fatal("the TLS gRPC client is disabled")
	}
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case request := <-service.requests:
		if name := request.GetResourceMetrics()[0].GetScopeMetrics()[0].GetMetrics()[0].GetName(); name != "codex.thread.started" {
			t.Fatalf("metric = %q", name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the TLS gRPC collector did not receive an export")
	}
}

// A missing CA certificate disables the exporter instead of exporting without
// the requested trust configuration.
func TestOTLPGRPCExporterMissingCAIsDisabled(t *testing.T) {
	if exporter := NewOTLPGRPCMetricsExporter(OTLPGRPCMetricsExporterOptions{
		Endpoint: "https://metrics.test:4317",
		TLS:      &OTLPHTTPTLSConfig{CACertificate: filepath.Join(t.TempDir(), "missing.pem")},
	}); exporter != nil {
		t.Fatal("a missing CA certificate built a gRPC exporter")
	}
}

func startMetricsGRPCServer(t *testing.T, service collectormetricspb.MetricsServiceServer, options ...grpc.ServerOption) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server := grpc.NewServer(options...)
	collectormetricspb.RegisterMetricsServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), server.Stop
}

func selfSignedServerCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "codex-grpc-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
