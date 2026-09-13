package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Rust parity: codex-rs/otel/src/metrics/client.rs's OtlpGrpc branch and
// codex-rs/otel/src/otlp.rs::build_grpc_tls_config. tonic (Rust) only wraps the
// connection in TLS when the endpoint scheme is `https`; an `http` endpoint is
// plaintext even when a TLS config is present, and a configured CA certificate
// is added to the enabled roots rather than replacing them.

// OTLPGRPCMetricsExporterOptions configures the OTLP gRPC transport. An empty
// Endpoint disables the exporter.
type OTLPGRPCMetricsExporterOptions struct {
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
	// TLS carries the gRPC TLS settings used for an https endpoint.
	TLS *OTLPHTTPTLSConfig
}

// OTLPGRPCMetricsExporter posts encoded metric batches over OTLP gRPC.
type OTLPGRPCMetricsExporter struct {
	client  collectormetricspb.MetricsServiceClient
	conn    *grpc.ClientConn
	headers map[string]string
	timeout time.Duration
}

// NewOTLPGRPCMetricsExporter builds the gRPC exporter. It returns nil when the
// endpoint is empty or the TLS settings cannot be built.
func NewOTLPGRPCMetricsExporter(options OTLPGRPCMetricsExporterOptions) *OTLPGRPCMetricsExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		logMetricsExportFailure(fmt.Errorf("invalid OTLP gRPC endpoint %q", endpoint))
		return nil
	}
	credentialsOption, err := grpcCredentials(parsed, options.TLS)
	if err != nil {
		// Rust fails provider initialization on a TLS configuration error; the
		// Go client has no error channel yet, so it reports the problem and
		// stays disabled rather than exporting without the requested TLS.
		logMetricsExportFailure(err)
		return nil
	}
	conn, err := grpc.NewClient(parsed.Host, credentialsOption)
	if err != nil {
		logMetricsExportFailure(err)
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveMetricsExportTimeout()
	}
	return &OTLPGRPCMetricsExporter{
		client:  collectormetricspb.NewMetricsServiceClient(conn),
		conn:    conn,
		headers: cloneMetricHeaderMap(options.Headers),
		timeout: timeout,
	}
}

// grpcCredentials mirrors build_grpc_tls_config: an https endpoint uses TLS
// (the configured CA joined with the enabled roots, plus the optional mTLS
// identity), while every other scheme is plaintext.
func grpcCredentials(parsed *url.URL, tlsOptions *OTLPHTTPTLSConfig) (grpc.DialOption, error) {
	if parsed.Scheme != "https" {
		return grpc.WithTransportCredentials(insecure.NewCredentials()), nil
	}
	config := &tls.Config{ServerName: parsed.Hostname()}
	if tlsOptions != nil && tlsOptions.CACertificate != "" {
		pem, err := os.ReadFile(tlsOptions.CACertificate)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", tlsOptions.CACertificate, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("failed to parse certificate %s", tlsOptions.CACertificate)
		}
		config.RootCAs = roots
	}
	if tlsOptions != nil && tlsOptions.ClientCertificate != "" && tlsOptions.ClientPrivateKey != "" {
		certificatePEM, err := os.ReadFile(tlsOptions.ClientCertificate)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", tlsOptions.ClientCertificate, err)
		}
		privateKeyPEM, err := os.ReadFile(tlsOptions.ClientPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", tlsOptions.ClientPrivateKey, err)
		}
		identity, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse client identity using %s and %s: %w", tlsOptions.ClientCertificate, tlsOptions.ClientPrivateKey, err)
		}
		config.Certificates = []tls.Certificate{identity}
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(config)), nil
}

// Export sends one encoded batch.
func (e *OTLPGRPCMetricsExporter) Export(ctx context.Context, request OTLPExportMetricsRequest) error {
	if e == nil || e.client == nil {
		return nil
	}
	if len(request.ResourceMetrics) == 0 {
		return nil
	}
	encoded, err := request.protoRequest()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if len(e.headers) > 0 {
		requestCtx = metadata.NewOutgoingContext(requestCtx, metadata.New(e.headers))
	}
	_, err = e.client.Export(requestCtx, encoded)
	return err
}

// Close releases the gRPC connection.
func (e *OTLPGRPCMetricsExporter) Close() error {
	if e == nil || e.conn == nil {
		return nil
	}
	return e.conn.Close()
}
