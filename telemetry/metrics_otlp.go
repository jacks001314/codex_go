package telemetry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-rs/otel/src/metrics (client.rs + config.rs) and
// codex-rs/otel/src/otlp.rs. Metrics are exported over OTLP/HTTP with JSON
// payloads and DELTA temporality, matching the Rust client's
// `with_temporality(Temporality::Delta)` reader and its `Protocol::HttpJson`
// Statsig default.

const (
	// OTLP aggregation temporality values (opentelemetry-proto).
	otlpTemporalityDelta = 1

	// MetricsExporterTimeoutEnv / MetricsExporterSignalTimeoutEnv mirror the OTLP
	// timeout environment variables Rust resolves per signal
	// (OTEL_EXPORTER_OTLP_TIMEOUT / OTEL_EXPORTER_OTLP_METRICS_TIMEOUT).
	MetricsExporterTimeoutEnv       = "OTEL_EXPORTER_OTLP_TIMEOUT"
	MetricsExporterSignalTimeoutEnv = "OTEL_EXPORTER_OTLP_METRICS_TIMEOUT"

	// DefaultMetricsExportTimeout mirrors opentelemetry-otlp's default.
	DefaultMetricsExportTimeout = 10 * time.Second

	// DefaultMetricsExportInterval mirrors the periodic reader's default.
	DefaultMetricsExportInterval = 60 * time.Second

	// StatsigMetricsEndpoint and StatsigMetricsAPIKeyHeader mirror
	// codex-rs/otel/src/config.rs's built-in metrics route.
	StatsigMetricsEndpoint     = "https://ab.chatgpt.com/otlp/v1/metrics"
	StatsigMetricsAPIKeyHeader = "statsig-api-key"
	StatsigMetricsAPIKey       = "client-MkRuleRQBd6qakfnDYqJVR9JuXcY57Ljly3vi5JVUIO"
)

// otlpAttribute is one OTLP key/value attribute.
type otlpAttribute struct {
	Key   string        `json:"key"`
	Value otlpAttrValue `json:"value"`
}

type otlpAttrValue struct {
	StringValue *string `json:"stringValue,omitempty"`
}

func otlpStringAttribute(key string, value string) otlpAttribute {
	text := value
	return otlpAttribute{Key: key, Value: otlpAttrValue{StringValue: &text}}
}

func otlpAttributes(tags []MetricTagValue) []otlpAttribute {
	if len(tags) == 0 {
		return nil
	}
	out := make([]otlpAttribute, 0, len(tags))
	for _, tag := range tags {
		out = append(out, otlpStringAttribute(tag.Key, tag.Value))
	}
	return out
}

type otlpNumberDataPoint struct {
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	StartTimeUnixNano string          `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string          `json:"timeUnixNano,omitempty"`
	AsInt             *string         `json:"asInt,omitempty"`
	AsDouble          *float64        `json:"asDouble,omitempty"`
}

type otlpHistogramDataPoint struct {
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	StartTimeUnixNano string          `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string          `json:"timeUnixNano,omitempty"`
	Count             string          `json:"count"`
	Sum               *float64        `json:"sum,omitempty"`
	BucketCounts      []string        `json:"bucketCounts,omitempty"`
	ExplicitBounds    []float64       `json:"explicitBounds,omitempty"`
}

type otlpSum struct {
	AggregationTemporality int                   `json:"aggregationTemporality,omitempty"`
	IsMonotonic            bool                  `json:"isMonotonic,omitempty"`
	DataPoints             []otlpNumberDataPoint `json:"dataPoints,omitempty"`
}

type otlpGauge struct {
	DataPoints []otlpNumberDataPoint `json:"dataPoints,omitempty"`
}

type otlpHistogram struct {
	AggregationTemporality int                      `json:"aggregationTemporality,omitempty"`
	DataPoints             []otlpHistogramDataPoint `json:"dataPoints,omitempty"`
}

type otlpMetric struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Unit        string         `json:"unit,omitempty"`
	Sum         *otlpSum       `json:"sum,omitempty"`
	Gauge       *otlpGauge     `json:"gauge,omitempty"`
	Histogram   *otlpHistogram `json:"histogram,omitempty"`
}

type otlpScope struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type otlpScopeMetrics struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics,omitempty"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes,omitempty"`
}

type otlpResourceMetrics struct {
	Resource     otlpResource       `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics,omitempty"`
}

type otlpExportMetricsRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

// OTLPMetricsExporter posts encoded metric batches to one OTLP/HTTP endpoint.
type OTLPMetricsExporter struct {
	endpoint     string
	headers      map[string]string
	httpClient   HTTPDoer
	timeout      time.Duration
	requireHTTPS bool
}

// OTLPMetricsExporterOptions configures the transport. An empty Endpoint
// disables the exporter.
type OTLPMetricsExporterOptions struct {
	Endpoint   string
	Headers    map[string]string
	HTTPClient HTTPDoer
	Timeout    time.Duration
	// TLS mirrors Rust's OtelTlsConfig for the OTLP HTTP exporter. A nil value
	// uses the platform roots through the default transport.
	TLS *OTLPHTTPTLSConfig
}

// OTLPHTTPTLSConfig mirrors codex-otel's OtelTlsConfig for OTLP HTTP: the CA
// certificate replaces the built-in roots, and a client identity enables mTLS
// over HTTPS.
type OTLPHTTPTLSConfig struct {
	CACertificate     string
	ClientCertificate string
	ClientPrivateKey  string
}

// NewOTLPMetricsExporter builds the exporter, resolving the OTLP timeout from
// the environment like Rust's resolve_otlp_timeout.
func NewOTLPMetricsExporter(options OTLPMetricsExporterOptions) *OTLPMetricsExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveMetricsExportTimeout()
	}
	client := options.HTTPClient
	var requireHTTPS bool
	if client == nil {
		var err error
		client, requireHTTPS, err = buildOTLPHTTPClient(options.TLS, timeout)
		if err != nil {
			// Rust fails provider initialization on a TLS configuration error;
			// the Go client has no error channel yet, so it reports the problem
			// and stays disabled rather than exporting without the requested TLS.
			logMetricsExportFailure(err)
			return nil
		}
	}
	return &OTLPMetricsExporter{
		endpoint:     endpoint,
		headers:      cloneMetricHeaderMap(options.Headers),
		httpClient:   client,
		timeout:      timeout,
		requireHTTPS: requireHTTPS,
	}
}

// buildOTLPHTTPClient mirrors codex-otel's build_http_client_inner for the OTLP
// HTTP exporter: the CA certificate disables the built-in roots, and a client
// certificate/private key pair is required together and forces HTTPS.
func buildOTLPHTTPClient(tlsOptions *OTLPHTTPTLSConfig, timeout time.Duration) (HTTPDoer, bool, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: transport, Timeout: timeout}
	if tlsOptions == nil {
		return client, false, nil
	}
	tlsConfig := &tls.Config{}
	if tlsOptions.CACertificate != "" {
		pem, err := os.ReadFile(tlsOptions.CACertificate)
		if err != nil {
			return nil, false, fmt.Errorf("failed to read %s: %w", tlsOptions.CACertificate, err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, false, fmt.Errorf("failed to parse certificate %s", tlsOptions.CACertificate)
		}
		tlsConfig.RootCAs = roots
	}
	haveCertificate := tlsOptions.ClientCertificate != ""
	havePrivateKey := tlsOptions.ClientPrivateKey != ""
	if haveCertificate != havePrivateKey {
		return nil, false, fmt.Errorf("client_certificate and client_private_key must both be provided for mTLS")
	}
	if haveCertificate {
		certificatePEM, err := os.ReadFile(tlsOptions.ClientCertificate)
		if err != nil {
			return nil, false, fmt.Errorf("failed to read %s: %w", tlsOptions.ClientCertificate, err)
		}
		privateKeyPEM, err := os.ReadFile(tlsOptions.ClientPrivateKey)
		if err != nil {
			return nil, false, fmt.Errorf("failed to read %s: %w", tlsOptions.ClientPrivateKey, err)
		}
		identity, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse client identity using %s and %s: %w", tlsOptions.ClientCertificate, tlsOptions.ClientPrivateKey, err)
		}
		tlsConfig.Certificates = []tls.Certificate{identity}
	}
	transport.TLSClientConfig = tlsConfig
	return client, haveCertificate, nil
}

// Export posts one encoded batch.
func (e *OTLPMetricsExporter) Export(ctx context.Context, request OTLPExportMetricsRequest) error {
	if e == nil {
		return nil
	}
	if len(request.ResourceMetrics) == 0 {
		return nil
	}
	payload, err := request.MarshalJSON()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.requireHTTPS && !strings.HasPrefix(e.endpoint, "https://") {
		return fmt.Errorf("OTLP metrics endpoint %q must use HTTPS for mTLS", e.endpoint)
	}
	requestCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	for key, value := range e.headers {
		httpRequest.Header.Set(key, value)
	}
	response, err := e.httpClient.Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OTLP metrics export returned HTTP %d", response.StatusCode)
	}
	return nil
}

// OTLPExportMetricsRequest is one encoded export batch.
type OTLPExportMetricsRequest struct {
	ResourceMetrics []OTLPResourceMetrics
}

// OTLPResourceMetrics groups the resource, scope, and metrics of one batch.
type OTLPResourceMetrics struct {
	Resource     OTLPResource
	ScopeMetrics []OTLPScopeMetrics
}

type OTLPResource struct {
	Attributes []MetricTagValue
}

type OTLPScopeMetrics struct {
	Scope   OTLPScope
	Metrics []OTLPMetric
}

type OTLPScope struct {
	Name    string
	Version string
}

// OTLPMetric is one exported instrument.
type OTLPMetric struct {
	Name        string
	Description string
	Unit        string
	Sum         []OTLPNumberDataPoint
	Gauge       []OTLPNumberDataPoint
	Histogram   []OTLPHistogramDataPoint
	Monotonic   bool
	Temporality int
}

type OTLPNumberDataPoint struct {
	Attributes        []MetricTagValue
	StartTimeUnixNano string
	TimeUnixNano      string
	AsInt             *int64
	AsDouble          *float64
}

type OTLPHistogramDataPoint struct {
	Attributes        []MetricTagValue
	StartTimeUnixNano string
	TimeUnixNano      string
	Count             int64
	Sum               *float64
	BucketCounts      []int64
	ExplicitBounds    []float64
}

// MarshalJSON encodes the request into the OTLP JSON shape (int64 fields are
// strings, as the protobuf JSON mapping requires).
func (r OTLPExportMetricsRequest) MarshalJSON() ([]byte, error) {
	resourceMetrics := make([]otlpResourceMetrics, 0, len(r.ResourceMetrics))
	for _, resource := range r.ResourceMetrics {
		encodedResource := otlpResource{Attributes: otlpAttributes(resource.Resource.Attributes)}
		scopeMetrics := make([]otlpScopeMetrics, 0, len(resource.ScopeMetrics))
		for _, scoped := range resource.ScopeMetrics {
			metrics := make([]otlpMetric, 0, len(scoped.Metrics))
			for _, metric := range scoped.Metrics {
				encoded, err := metric.otlpJSON()
				if err != nil {
					return nil, err
				}
				metrics = append(metrics, encoded)
			}
			scopeMetrics = append(scopeMetrics, otlpScopeMetrics{
				Scope:   otlpScope{Name: scoped.Scope.Name, Version: scoped.Scope.Version},
				Metrics: metrics,
			})
		}
		resourceMetrics = append(resourceMetrics, otlpResourceMetrics{
			Resource:     encodedResource,
			ScopeMetrics: scopeMetrics,
		})
	}
	return json.Marshal(struct {
		ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
	}{ResourceMetrics: resourceMetrics})
}

func (m OTLPMetric) otlpJSON() (otlpMetric, error) {
	switch {
	case m.Sum != nil && m.Histogram == nil && m.Gauge == nil:
		dataPoints, err := otlpNumberDataPoints(m.Sum)
		if err != nil {
			return otlpMetric{}, err
		}
		temporality := m.Temporality
		if temporality == 0 {
			temporality = otlpTemporalityDelta
		}
		return otlpMetric{
			Name:        m.Name,
			Description: m.Description,
			Unit:        m.Unit,
			Sum:         &otlpSum{AggregationTemporality: temporality, IsMonotonic: m.Monotonic, DataPoints: dataPoints},
		}, nil
	case m.Gauge != nil && m.Sum == nil && m.Histogram == nil:
		dataPoints, err := otlpNumberDataPoints(m.Gauge)
		if err != nil {
			return otlpMetric{}, err
		}
		return otlpMetric{Name: m.Name, Description: m.Description, Unit: m.Unit, Gauge: &otlpGauge{DataPoints: dataPoints}}, nil
	case m.Histogram != nil && m.Sum == nil && m.Gauge == nil:
		temporality := m.Temporality
		if temporality == 0 {
			temporality = otlpTemporalityDelta
		}
		dataPoints := make([]otlpHistogramDataPoint, 0, len(m.Histogram))
		for _, dataPoint := range m.Histogram {
			bucketCounts := make([]string, 0, len(dataPoint.BucketCounts))
			for _, count := range dataPoint.BucketCounts {
				bucketCounts = append(bucketCounts, strconv.FormatInt(count, 10))
			}
			dataPoints = append(dataPoints, otlpHistogramDataPoint{
				Attributes:        otlpAttributes(dataPoint.Attributes),
				StartTimeUnixNano: dataPoint.StartTimeUnixNano,
				TimeUnixNano:      dataPoint.TimeUnixNano,
				Count:             strconv.FormatInt(dataPoint.Count, 10),
				Sum:               dataPoint.Sum,
				BucketCounts:      bucketCounts,
				ExplicitBounds:    dataPoint.ExplicitBounds,
			})
		}
		return otlpMetric{
			Name:        m.Name,
			Description: m.Description,
			Unit:        m.Unit,
			Histogram:   &otlpHistogram{AggregationTemporality: temporality, DataPoints: dataPoints},
		}, nil
	default:
		return otlpMetric{}, fmt.Errorf("OTLP metric %q must carry exactly one instrument", m.Name)
	}
}

func otlpNumberDataPoints(dataPoints []OTLPNumberDataPoint) ([]otlpNumberDataPoint, error) {
	out := make([]otlpNumberDataPoint, 0, len(dataPoints))
	for _, dataPoint := range dataPoints {
		encoded := otlpNumberDataPoint{
			Attributes:        otlpAttributes(dataPoint.Attributes),
			StartTimeUnixNano: dataPoint.StartTimeUnixNano,
			TimeUnixNano:      dataPoint.TimeUnixNano,
			AsDouble:          dataPoint.AsDouble,
		}
		if dataPoint.AsInt != nil {
			text := strconv.FormatInt(*dataPoint.AsInt, 10)
			encoded.AsInt = &text
		}
		out = append(out, encoded)
	}
	return out, nil
}

// resolveMetricsExportTimeout mirrors otlp::resolve_otlp_timeout for the
// metrics signal.
func resolveMetricsExportTimeout() time.Duration {
	if timeout, ok := readMetricsTimeoutEnv(MetricsExporterSignalTimeoutEnv); ok {
		return timeout
	}
	if timeout, ok := readMetricsTimeoutEnv(MetricsExporterTimeoutEnv); ok {
		return timeout
	}
	return DefaultMetricsExportTimeout
}

func readMetricsTimeoutEnv(name string) (time.Duration, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, false
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds < 0 {
		return 0, false
	}
	return time.Duration(milliseconds) * time.Millisecond, true
}

func cloneMetricHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

// logMetricsExportFailure records a dropped batch (the OTLP SDK drops a payload
// whose export fails rather than blocking the recorder).
func logMetricsExportFailure(err error) {
	if err == nil {
		return
	}
	slog.Warn("failed to export OTEL metrics", "error", err)
}
