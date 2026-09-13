package telemetry

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Rust parity: codex-rs/otel/src/metrics/client.rs. The client records
// counters, gauges, and histograms keyed by their tags, and exports them with
// DELTA temporality: each export carries only the observations since the
// previous export (gauges keep their last value). Metric names and tag
// components are validated; an instrument that fails validation is dropped
// rather than exported.

// MetricsMeterName mirrors METER_NAME.
const MetricsMeterName = "codex"

// Duration histogram units/descriptions/boundaries mirror client.rs.
const (
	MillisecondDurationUnit        = "ms"
	MillisecondDurationDescription = "Duration in milliseconds."
	SecondDurationUnit             = "s"
)

var (
	// MillisecondDurationBoundaries mirrors MILLISECOND_DURATION_BOUNDARIES.
	MillisecondDurationBoundaries = []float64{
		0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 1250, 1500, 1750, 2000, 2250, 2500, 3000,
		3500, 4000, 4500, 5000, 6000, 7000, 7500, 8000, 9000, 10000, 12000, 15000, 20000, 30000,
		60000, 120000,
	}
	// SecondDurationBoundaries mirrors SECOND_DURATION_BOUNDARIES.
	SecondDurationBoundaries = []float64{
		0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10, 12, 15, 20,
		30, 60, 120,
	}
)

// StatsigDisabledMetrics mirrors STATSIG_DISABLED_METRICS: metrics intentionally
// not sent through Codex's built-in Statsig route. Keep it an exact-name list so
// custom OTLP exporters still receive them.
var StatsigDisabledMetrics = []string{
	APICallCountMetric,
	APICallDurationMetric,
	ConversationTurnCountMetric,
	// Caller-side executor volume belongs in configured observability collectors.
	ExecServerClientRequestCountMetric,
	ResponsesAPIEngineIAPITTFTDurationMetric,
	ResponsesAPIEngineServiceTBTDurationMetric,
	ResponsesAPIEngineServiceTTFTDurationMetric,
	ToolCallCountMetric,
	ToolCallDurationMetric,
	TurnCostMicroUSDMetric,
	TurnTokenUsageMetric,
}

// MetricsClientOptions configures the metrics client. An empty Endpoint (or a
// nil exporter) leaves the client disabled.
type MetricsClientOptions struct {
	Environment    string
	ServiceName    string
	ServiceVersion string
	Endpoint       string
	Headers        map[string]string
	HTTPClient     HTTPDoer
	// TLS configures the OTLP HTTP transport when no HTTPClient is supplied.
	TLS *OTLPHTTPTLSConfig
	// Statsig marks the built-in metrics route: it resolves the Statsig endpoint
	// when that build enables it and applies the Statsig-disabled metric list.
	Statsig bool
	// StatsigEnabled mirrors Rust's `cfg!(debug_assertions)` guard for the
	// built-in route: the Statsig default resolves to the OTLP endpoint only in
	// builds that opt in.
	StatsigEnabled bool
	// ExportInterval is the periodic export cadence. Zero uses Rust's periodic
	// reader default; a negative value disables the background loop so the
	// caller drives Flush/Export directly.
	ExportInterval time.Duration
	Timeout        time.Duration
	DefaultTags    map[string]string
	// DisabledMetrics overrides the Statsig-disabled list.
	DisabledMetrics []string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// MetricsClient records metrics and exports them through an OTLP/HTTP exporter.
type MetricsClient struct {
	exporter *OTLPMetricsExporter
	scope    OTLPScope

	hostName       string
	environment    string
	serviceVersion string
	serviceName    string
	defaultTags    map[string]string
	disabled       map[string]bool

	mu          sync.Mutex
	counters    map[instrumentKey]map[string]*counterSeries
	gauges      map[instrumentKey]map[string]*gaugeSeries
	histograms  map[instrumentKey]map[string]*histogramSeries
	windowStart time.Time
	now         func() time.Time

	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

type instrumentKey struct {
	name        string
	unit        string
	description string
}

type counterSeries struct {
	tags  []MetricTagValue
	value int64
}

type gaugeSeries struct {
	tags  []MetricTagValue
	value int64
}

type histogramSeries struct {
	tags         []MetricTagValue
	boundaries   []float64
	count        int64
	sum          float64
	bucketCounts []int64
}

// NewMetricsClient builds the client. It returns a disabled client when no
// exporter is configured, so callers can install it unconditionally.
func NewMetricsClient(options MetricsClientOptions) *MetricsClient {
	client := &MetricsClient{
		counters:    map[instrumentKey]map[string]*counterSeries{},
		gauges:      map[instrumentKey]map[string]*gaugeSeries{},
		histograms:  map[instrumentKey]map[string]*histogramSeries{},
		defaultTags: cloneMetricTagMap(options.DefaultTags),
		scope:       OTLPScope{Name: MetricsMeterName},
		now:         options.Now,
	}
	if client.now == nil {
		client.now = time.Now
	}
	client.windowStart = client.now().UTC()

	endpoint := options.Endpoint
	headers := options.Headers
	disabled := options.DisabledMetrics
	if options.Statsig {
		if !statsigMetricsRouteEnabled() {
			// Rust resolves the Statsig default to `None` unless the build opts
			// in, so a development build never exports the built-in metrics.
			return client
		}
		if endpoint == "" {
			endpoint = StatsigMetricsEndpoint
		}
		if headers == nil {
			headers = map[string]string{StatsigMetricsAPIKeyHeader: StatsigMetricsAPIKey}
		}
		if disabled == nil {
			disabled = StatsigDisabledMetrics
		}
	}
	if len(disabled) > 0 {
		client.disabled = map[string]bool{}
		for _, name := range disabled {
			client.disabled[name] = true
		}
	}
	if options.ServiceName != "" {
		client.serviceName = options.ServiceName
	}
	client.serviceVersion = options.ServiceVersion
	client.environment = options.Environment
	client.exporter = NewOTLPMetricsExporter(OTLPMetricsExporterOptions{
		Endpoint:   endpoint,
		Headers:    headers,
		HTTPClient: options.HTTPClient,
		Timeout:    options.Timeout,
		TLS:        options.TLS,
	})
	if client.exporter == nil {
		return client
	}
	// Rust always installs a periodic reader; a zero interval therefore uses the
	// reader's default cadence. A negative interval disables the background loop
	// so a caller can drive Flush directly (there is no Rust equivalent).
	exportInterval := options.ExportInterval
	if exportInterval == 0 {
		exportInterval = DefaultMetricsExportInterval
	}
	if exportInterval > 0 {
		client.startExportLoop(exportInterval)
	}
	return client
}

// NewStatsigMetricsClient builds the built-in metrics client for a build that
// opts into the Statsig route.
func NewStatsigMetricsClient(options MetricsClientOptions) *MetricsClient {
	options.Statsig = true
	return NewMetricsClient(options)
}

// StatsigMetricsRouteEnabled mirrors Rust's `cfg!(debug_assertions)` guard for
// the built-in Statsig metrics route. Development builds leave it off so local
// runs never emit best-effort OTEL traffic; release builds set it via
// `-ldflags -X codex_go/telemetry.StatsigMetricsRouteEnabled=true`.
var StatsigMetricsRouteEnabled = "false"

// statsigMetricsRouteEnabled reports whether this build enables the built-in
// Statsig route.
func statsigMetricsRouteEnabled() bool {
	return StatsigMetricsRouteEnabled == "true"
}

// Enabled reports whether the client will export metrics.
func (c *MetricsClient) Enabled() bool {
	return c != nil && c.exporter != nil
}

// Counter records one counter increment.
func (c *MetricsClient) Counter(name string, inc int, tags map[string]string) {
	c.CounterWithDescription(name, "", inc, tags)
}

// CounterWithDescription records one counter increment with an instrument
// description.
func (c *MetricsClient) CounterWithDescription(name string, description string, inc int, tags map[string]string) {
	if c == nil || !c.Enabled() {
		return
	}
	if err := validateMetricName(name); err != nil {
		return
	}
	if inc < 0 {
		return
	}
	if c.disabled[name] {
		return
	}
	attributes, err := mergeMetricTags(c.defaultTags, tags)
	if err != nil {
		return
	}
	key := instrumentKey{name: name, description: description}
	c.mu.Lock()
	defer c.mu.Unlock()
	series := c.counters[key]
	if series == nil {
		series = map[string]*counterSeries{}
		c.counters[key] = series
	}
	id := metricTagSetID(attributes)
	entry := series[id]
	if entry == nil {
		entry = &counterSeries{tags: attributes}
		series[id] = entry
	}
	entry.value += int64(inc)
}

// Histogram records one histogram observation with the default boundaries.
func (c *MetricsClient) Histogram(name string, value int, tags map[string]string) {
	c.HistogramWithBounds(name, value, nil, tags)
}

// HistogramWithBounds records one histogram observation with explicit buckets.
func (c *MetricsClient) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	c.recordHistogram(name, float64(value), boundaries, "", "", tags)
}

// RecordDuration records one duration observation in milliseconds (Rust's
// millisecond duration histogram).
func (c *MetricsClient) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	c.recordHistogram(
		name,
		float64(duration)/float64(time.Millisecond),
		MillisecondDurationBoundaries,
		MillisecondDurationUnit,
		MillisecondDurationDescription,
		tags,
	)
}

// RecordDurationSecondsWithDescription records one duration observation in
// seconds (Rust's `record_duration_seconds_with_description`, which requires the
// instrument description).
func (c *MetricsClient) RecordDurationSecondsWithDescription(name string, description string, duration time.Duration, tags map[string]string) {
	c.recordHistogram(
		name,
		float64(duration)/float64(time.Second),
		SecondDurationBoundaries,
		SecondDurationUnit,
		description,
		tags,
	)
}

// Gauge records one gauge observation.
func (c *MetricsClient) Gauge(name string, value int, tags map[string]string) {
	c.GaugeWithDescription(name, "", value, tags)
}

// GaugeWithDescription records one gauge observation with an instrument
// description.
func (c *MetricsClient) GaugeWithDescription(name string, description string, value int, tags map[string]string) {
	if c == nil || !c.Enabled() {
		return
	}
	if err := validateMetricName(name); err != nil {
		return
	}
	if c.disabled[name] {
		return
	}
	attributes, err := mergeMetricTags(c.defaultTags, tags)
	if err != nil {
		return
	}
	key := instrumentKey{name: name, description: description}
	c.mu.Lock()
	defer c.mu.Unlock()
	series := c.gauges[key]
	if series == nil {
		series = map[string]*gaugeSeries{}
		c.gauges[key] = series
	}
	id := metricTagSetID(attributes)
	entry := series[id]
	if entry == nil {
		entry = &gaugeSeries{tags: attributes}
		series[id] = entry
	}
	entry.value = int64(value)
}

func (c *MetricsClient) recordHistogram(name string, value float64, boundaries []float64, unit string, description string, tags map[string]string) {
	if c == nil || !c.Enabled() {
		return
	}
	if err := validateMetricName(name); err != nil {
		return
	}
	if c.disabled[name] {
		return
	}
	attributes, err := mergeMetricTags(c.defaultTags, tags)
	if err != nil {
		return
	}
	key := instrumentKey{name: name, unit: unit, description: description}
	c.mu.Lock()
	defer c.mu.Unlock()
	series := c.histograms[key]
	if series == nil {
		series = map[string]*histogramSeries{}
		c.histograms[key] = series
	}
	id := metricTagSetID(attributes)
	entry := series[id]
	if entry == nil {
		entry = &histogramSeries{tags: attributes, boundaries: append([]float64(nil), boundaries...)}
		if len(boundaries) > 0 {
			entry.bucketCounts = make([]int64, len(boundaries)+1)
		}
		series[id] = entry
	}
	entry.count++
	entry.sum += value
	if len(entry.boundaries) > 0 {
		index := sort.SearchFloat64s(entry.boundaries, value)
		if index < len(entry.bucketCounts) {
			entry.bucketCounts[index]++
		}
	}
}

// Flush exports the accumulated delta and resets the delta instruments.
func (c *MetricsClient) Flush(ctx context.Context) error {
	if c == nil || !c.Enabled() {
		return nil
	}
	request := c.snapshot()
	if len(request.ResourceMetrics) == 0 {
		return nil
	}
	if err := c.exporter.Export(ctx, request); err != nil {
		logMetricsExportFailure(err)
		return err
	}
	return nil
}

// Shutdown stops the export loop and flushes.
func (c *MetricsClient) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	done := c.done
	c.done = nil
	closed := c.closed
	c.closed = true
	c.mu.Unlock()
	if done != nil && !closed {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	return c.Flush(ctx)
}

func (c *MetricsClient) startExportLoop(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	c.interval = interval
	c.cancel = cancel
	c.done = done
	c.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.Flush(ctx)
			}
		}
	}()
}

// snapshot builds the OTLP batch for the current window and resets the delta
// instruments (counters and histograms) while keeping gauge values.
func (c *MetricsClient) snapshot() OTLPExportMetricsRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	start := c.windowStart
	if start.IsZero() || start.After(now) {
		start = now
	}
	metrics := make([]OTLPMetric, 0, len(c.counters)+len(c.histograms)+len(c.gauges))
	for _, key := range sortedInstrumentKeys(c.counters) {
		series := c.counters[key]
		dataPoints := make([]OTLPNumberDataPoint, 0, len(series))
		for _, id := range sortedSeriesIDs(series) {
			entry := series[id]
			if entry.value == 0 {
				continue
			}
			value := entry.value
			dataPoints = append(dataPoints, OTLPNumberDataPoint{
				Attributes:        entry.tags,
				StartTimeUnixNano: unixNanoString(start),
				TimeUnixNano:      unixNanoString(now),
				AsInt:             &value,
			})
		}
		if len(dataPoints) == 0 {
			continue
		}
		metrics = append(metrics, OTLPMetric{
			Name:        key.name,
			Description: key.description,
			Unit:        key.unit,
			Sum:         dataPoints,
			Monotonic:   true,
		})
	}
	for _, key := range sortedInstrumentKeys(c.histograms) {
		series := c.histograms[key]
		dataPoints := make([]OTLPHistogramDataPoint, 0, len(series))
		for _, id := range sortedSeriesIDs(series) {
			entry := series[id]
			if entry.count == 0 {
				continue
			}
			sum := entry.sum
			dataPoints = append(dataPoints, OTLPHistogramDataPoint{
				Attributes:        entry.tags,
				StartTimeUnixNano: unixNanoString(start),
				TimeUnixNano:      unixNanoString(now),
				Count:             entry.count,
				Sum:               &sum,
				BucketCounts:      append([]int64(nil), entry.bucketCounts...),
				ExplicitBounds:    append([]float64(nil), entry.boundaries...),
			})
		}
		if len(dataPoints) == 0 {
			continue
		}
		metrics = append(metrics, OTLPMetric{
			Name:        key.name,
			Description: key.description,
			Unit:        key.unit,
			Histogram:   dataPoints,
		})
	}
	for _, key := range sortedInstrumentKeys(c.gauges) {
		series := c.gauges[key]
		dataPoints := make([]OTLPNumberDataPoint, 0, len(series))
		for _, id := range sortedSeriesIDs(series) {
			entry := series[id]
			value := entry.value
			dataPoints = append(dataPoints, OTLPNumberDataPoint{
				Attributes:        entry.tags,
				StartTimeUnixNano: unixNanoString(start),
				TimeUnixNano:      unixNanoString(now),
				AsInt:             &value,
			})
		}
		if len(dataPoints) == 0 {
			continue
		}
		metrics = append(metrics, OTLPMetric{
			Name:        key.name,
			Description: key.description,
			Unit:        key.unit,
			Gauge:       dataPoints,
		})
	}
	c.windowStart = now
	c.counters = map[instrumentKey]map[string]*counterSeries{}
	c.histograms = map[instrumentKey]map[string]*histogramSeries{}
	if len(metrics) == 0 {
		return OTLPExportMetricsRequest{}
	}
	return OTLPExportMetricsRequest{
		ResourceMetrics: []OTLPResourceMetrics{{
			Resource: OTLPResource{Attributes: c.resourceAttributes()},
			ScopeMetrics: []OTLPScopeMetrics{{
				Scope:   c.scope,
				Metrics: metrics,
			}},
		}},
	}
}

// resourceAttributes mirrors the Rust metrics resource: service.name,
// service.version, and the environment.
func (c *MetricsClient) resourceAttributes() []MetricTagValue {
	attributes := []MetricTagValue{}
	if c == nil {
		return attributes
	}
	if name := c.serviceName; name != "" {
		attributes = append(attributes, MetricTagValue{Key: "service.name", Value: name})
	}
	if version := c.serviceVersion; version != "" {
		attributes = append(attributes, MetricTagValue{Key: "service.version", Value: version})
	}
	if environment := c.environment; environment != "" {
		attributes = append(attributes, MetricTagValue{Key: "env", Value: environment})
	}
	return attributes
}

func metricTagSetID(tags []MetricTagValue) string {
	if len(tags) == 0 {
		return ""
	}
	builder := make([]byte, 0, len(tags)*16)
	for _, tag := range tags {
		builder = append(builder, tag.Key...)
		builder = append(builder, '=')
		builder = append(builder, tag.Value...)
		builder = append(builder, 0)
	}
	return string(builder)
}

func unixNanoString(value time.Time) string {
	return formatInt64(value.UnixNano())
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func sortedInstrumentKeys[V any](values map[instrumentKey]V) []instrumentKey {
	keys := make([]instrumentKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i int, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		if keys[i].unit != keys[j].unit {
			return keys[i].unit < keys[j].unit
		}
		return keys[i].description < keys[j].description
	})
	return keys
}

func sortedSeriesIDs[V any](values map[string]V) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func cloneMetricTagMap(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}
