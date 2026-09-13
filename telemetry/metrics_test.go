package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mirrors codex-utils-string's sanitize_metric_tag_value tests.
func TestSanitizeMetricTagValueLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  string
	}{
		{"///", "unspecified"},
		{"bad value!", "bad_value"},
		{"gpt-5.1", "gpt-5.1"},
		{"_leading", "leading"},
		{"trailing_", "trailing"},
		{"codex_cli_rs", "codex_cli_rs"},
		{strings.Repeat("a", 300), strings.Repeat("a", 256)},
		{"", "unspecified"},
	} {
		if got := SanitizeMetricTagValue(testCase.value); got != testCase.want {
			t.Fatalf("SanitizeMetricTagValue(%q) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}

func TestBoundedOriginatorTagValueLikeRust(t *testing.T) {
	if got := BoundedOriginatorTagValue("codex-tui"); got != "codex-tui" {
		t.Fatalf("known originator = %q", got)
	}
	if got := BoundedOriginatorTagValue("secret user agent!"); got != "other" {
		t.Fatalf("unknown originator = %q", got)
	}
}

// Mirrors Rust's session_metric_tags tests: the known tags appear in order and
// missing optional tags are skipped.
func TestSessionMetricTagsLikeRust(t *testing.T) {
	tags, err := SessionMetricTags("api_key", "cli", "codex_cli", "desktop_app", "gpt-5.1", "1.2.3")
	if err != nil {
		t.Fatalf("SessionMetricTags() error = %v", err)
	}
	want := []MetricTagValue{
		{Key: AuthModeTag, Value: "api_key"},
		{Key: SessionSourceTag, Value: "cli"},
		{Key: OriginatorTag, Value: "codex_cli"},
		{Key: ServiceNameTag, Value: "desktop_app"},
		{Key: ModelTag, Value: "gpt-5.1"},
		{Key: AppVersionTag, Value: "1.2.3"},
	}
	if len(tags) != len(want) {
		t.Fatalf("tags = %#v", tags)
	}
	for index := range want {
		if tags[index] != want[index] {
			t.Fatalf("tags[%d] = %#v, want %#v", index, tags[index], want[index])
		}
	}

	skipped, err := SessionMetricTags("", "exec", "codex_exec", "", "gpt-5.1", "1.2.3")
	if err != nil {
		t.Fatalf("SessionMetricTags() error = %v", err)
	}
	if len(skipped) != 4 || skipped[0].Key != SessionSourceTag {
		t.Fatalf("skipped tags = %#v", skipped)
	}
}

func TestMetricValidationRejectsInvalidComponents(t *testing.T) {
	if err := validateMetricName("codex.turn.token_usage"); err != nil {
		t.Fatalf("valid name error = %v", err)
	}
	if !errors.Is(validateMetricName(""), ErrEmptyMetricName) {
		t.Fatal("empty name was accepted")
	}
	for _, name := range []string{"bad name", "codex/name", "codex:name"} {
		if err := validateMetricName(name); !errors.Is(err, ErrInvalidMetricName) {
			t.Fatalf("name %q error = %v", name, err)
		}
	}
	if err := validateMetricTagValue("exec_server_client_requests_total"); err != nil {
		t.Fatalf("valid tag error = %v", err)
	}
	for _, value := range []string{"", "bad value"} {
		if err := validateMetricTagValue(value); err == nil {
			t.Fatalf("tag value %q was accepted", value)
		}
	}
}

// recordingHTTPDoer captures OTLP exports for assertions.
type recordingHTTPDoer struct {
	mu       sync.Mutex
	requests []recordedMetricsRequest
	status   int
	err      error
}

type recordedMetricsRequest struct {
	url     string
	headers http.Header
	body    map[string]any
}

func (d *recordingHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	if d.err != nil {
		return nil, d.err
	}
	payload := map[string]any{}
	if request.Body != nil {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	d.requests = append(d.requests, recordedMetricsRequest{url: request.URL.String(), headers: request.Header.Clone(), body: payload})
	d.mu.Unlock()
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}, nil
}

func (d *recordingHTTPDoer) snapshot() []recordedMetricsRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]recordedMetricsRequest(nil), d.requests...)
}

func newTestMetricsClient(t *testing.T, doer *recordingHTTPDoer, options MetricsClientOptions) *MetricsClient {
	t.Helper()
	options.Endpoint = "https://metrics.test/otlp/v1/metrics"
	options.HTTPClient = doer
	options.ServiceName = "codex"
	options.ServiceVersion = "1.2.3"
	options.Environment = "test"
	// Tests drive Flush directly, so keep the periodic export loop off.
	options.ExportInterval = -1
	return NewMetricsClient(options)
}

// Tests the OTLP/HTTP JSON shape: resource, scope, sum (delta, monotonic),
// histogram buckets, and gauge data points.
func TestMetricsClientExportsOTLPJSONLikeRust(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{
		Headers: map[string]string{"statsig-api-key": "key"},
	})
	if !client.Enabled() {
		t.Fatal("client is disabled")
	}
	client.Counter("codex.turn.tool.call", 2, map[string]string{"tool": "shell"})
	client.HistogramWithBounds("codex.turn.e2e_duration_ms", 7, []float64{5, 10}, nil)
	client.Gauge("codex.turn.unified_exec.running_processes", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	request := requests[0]
	if request.url != "https://metrics.test/otlp/v1/metrics" {
		t.Fatalf("url = %q", request.url)
	}
	if got := request.headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	if got := request.headers.Get("statsig-api-key"); got != "key" {
		t.Fatalf("statsig header = %q", got)
	}

	resourceMetrics := request.body["resourceMetrics"].([]any)
	resource := resourceMetrics[0].(map[string]any)["resource"].(map[string]any)
	if attributes := attributeMap(resource["attributes"]); attributes["service.name"] != "codex" ||
		attributes["service.version"] != "1.2.3" || attributes["env"] != "test" {
		t.Fatalf("resource attributes = %#v", attributes)
	}
	scopeMetrics := resourceMetrics[0].(map[string]any)["scopeMetrics"].([]any)
	scoped := scopeMetrics[0].(map[string]any)
	if scope := scoped["scope"].(map[string]any); scope["name"] != "codex" {
		t.Fatalf("scope = %#v", scope)
	}
	metrics := map[string]map[string]any{}
	for _, raw := range scoped["metrics"].([]any) {
		metric := raw.(map[string]any)
		metrics[metric["name"].(string)] = metric
	}

	counter := metrics["codex.turn.tool.call"]["sum"].(map[string]any)
	if counter["aggregationTemporality"].(float64) != 1 || counter["isMonotonic"] != true {
		t.Fatalf("counter = %#v", counter)
	}
	counterPoint := counter["dataPoints"].([]any)[0].(map[string]any)
	if counterPoint["asInt"] != "2" {
		t.Fatalf("counter point = %#v", counterPoint)
	}
	if attributes := attributeMap(counterPoint["attributes"]); attributes["tool"] != "shell" {
		t.Fatalf("counter attributes = %#v", attributes)
	}
	if counterPoint["startTimeUnixNano"] == "" || counterPoint["timeUnixNano"] == "" {
		t.Fatalf("counter timestamps = %#v", counterPoint)
	}

	histogram := metrics["codex.turn.e2e_duration_ms"]["histogram"].(map[string]any)
	if histogram["aggregationTemporality"].(float64) != 1 {
		t.Fatalf("histogram = %#v", histogram)
	}
	histogramPoint := histogram["dataPoints"].([]any)[0].(map[string]any)
	if histogramPoint["count"] != "1" || histogramPoint["sum"].(float64) != 7 {
		t.Fatalf("histogram point = %#v", histogramPoint)
	}
	if bounds := histogramPoint["explicitBounds"].([]any); len(bounds) != 2 || bounds[0].(float64) != 5 {
		t.Fatalf("histogram bounds = %#v", bounds)
	}
	// 7 falls into the (5, 10] bucket, so the first bucket is empty.
	if counts := histogramPoint["bucketCounts"].([]any); len(counts) != 3 || counts[0] != "0" || counts[1] != "1" {
		t.Fatalf("histogram buckets = %#v", counts)
	}

	gauge := metrics["codex.turn.unified_exec.running_processes"]["gauge"].(map[string]any)
	gaugePoint := gauge["dataPoints"].([]any)[0].(map[string]any)
	if gaugePoint["asInt"] != "1" {
		t.Fatalf("gauge point = %#v", gaugePoint)
	}
}

// DELTA temporality: each export carries only the observations since the last
// export, and gauges keep their last value.
func TestMetricsClientExportsDeltasOnly(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.Counter("codex.turn.tool.call", 1, nil)
	client.Gauge("codex.gauge", 5, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	requests := doer.snapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	second := metricsByName(t, requests[1])
	if _, ok := second["codex.turn.tool.call"]; ok {
		t.Fatalf("counter delta was re-exported: %#v", second)
	}
	if _, ok := second["codex.gauge"]; !ok {
		t.Fatalf("gauge was not re-exported: %#v", second)
	}

	client.Counter("codex.turn.tool.call", 3, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("third flush: %v", err)
	}
	third := metricsByName(t, doer.snapshot()[2])
	point := third["codex.turn.tool.call"]["sum"].(map[string]any)["dataPoints"].([]any)[0].(map[string]any)
	if point["asInt"] != "3" {
		t.Fatalf("counter delta = %#v", point)
	}
}

// The built-in Statsig route filters its disabled metrics (Rust
// STATSIG_DISABLED_METRICS) while a custom OTLP exporter still receives them.
func TestMetricsClientStatsigDisabledMetricsLikeRust(t *testing.T) {
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
	client.Counter("codex.turn.token_usage", 1, nil)
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].url != StatsigMetricsEndpoint {
		t.Fatalf("url = %q, want the Statsig endpoint", requests[0].url)
	}
	if got := requests[0].headers.Get(StatsigMetricsAPIKeyHeader); got != StatsigMetricsAPIKey {
		t.Fatalf("statsig key header = %q", got)
	}
	metrics := metricsByName(t, requests[0])
	if _, ok := metrics["codex.turn.token_usage"]; ok {
		t.Fatalf("Statsig-disabled metric was exported: %#v", metrics)
	}
	if _, ok := metrics["codex.thread.started"]; !ok {
		t.Fatalf("allowed metric was not exported: %#v", metrics)
	}

	customDoer := &recordingHTTPDoer{}
	custom := newTestMetricsClient(t, customDoer, MetricsClientOptions{})
	custom.Counter("codex.turn.token_usage", 1, nil)
	if err := custom.Flush(context.Background()); err != nil {
		t.Fatalf("custom Flush() error = %v", err)
	}
	if _, ok := metricsByName(t, customDoer.snapshot()[0])["codex.turn.token_usage"]; !ok {
		t.Fatal("a custom exporter did not receive the metric")
	}
}

// A development build never resolves the built-in Statsig route (Rust's
// cfg!(debug_assertions) guard).
func TestMetricsClientStatsigRouteRequiresOptInBuild(t *testing.T) {
	previous := StatsigMetricsRouteEnabled
	StatsigMetricsRouteEnabled = "false"
	defer func() { StatsigMetricsRouteEnabled = previous }()

	client := NewStatsigMetricsClient(MetricsClientOptions{HTTPClient: &recordingHTTPDoer{}, ServiceName: "codex"})
	if client.Enabled() {
		t.Fatal("the Statsig route was enabled without the build opt-in")
	}
}

func TestMetricsClientDropsInvalidInstruments(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.Counter("invalid name", 1, nil)
	client.Counter("codex.turn.tool.call", -1, nil)
	client.Counter("codex.turn.tool.call", 1, map[string]string{"bad value": "x"})
	client.Counter("codex.turn.tool.call", 1, map[string]string{"tool": "bad value"})
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if requests := doer.snapshot(); len(requests) != 0 {
		t.Fatalf("invalid instruments were exported: %#v", requests)
	}
}

// Duration histograms use Rust's millisecond unit/boundaries, and the second
// variant uses the second boundaries.
func TestMetricsClientDurationHistogramsLikeRust(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.RecordDuration("codex.turn.e2e_duration_ms", 250*time.Millisecond, nil)
	client.RecordDurationSecondsWithDescription("codex.goal.duration_s", "Goal duration in seconds.", 3*time.Second, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	millisecond := metrics["codex.turn.e2e_duration_ms"]["histogram"].(map[string]any)
	millisecondPoint := millisecond["dataPoints"].([]any)[0].(map[string]any)
	if millisecondPoint["sum"].(float64) != 250 {
		t.Fatalf("millisecond histogram = %#v", millisecondPoint)
	}
	if bounds := millisecondPoint["explicitBounds"].([]any); len(bounds) != len(MillisecondDurationBoundaries) {
		t.Fatalf("millisecond bounds = %d", len(bounds))
	}
	second := metrics["codex.goal.duration_s"]["histogram"].(map[string]any)
	secondPoint := second["dataPoints"].([]any)[0].(map[string]any)
	if secondPoint["sum"].(float64) != 3 {
		t.Fatalf("second histogram = %#v", secondPoint)
	}
	if bounds := secondPoint["explicitBounds"].([]any); len(bounds) != len(SecondDurationBoundaries) {
		t.Fatalf("second bounds = %d", len(bounds))
	}
}

func TestMetricsClientShutdownFlushes(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if requests := doer.snapshot(); len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestMetricsExportTimeoutResolutionLikeRust(t *testing.T) {
	t.Setenv(MetricsExporterTimeoutEnv, "")
	t.Setenv(MetricsExporterSignalTimeoutEnv, "")
	if got := resolveMetricsExportTimeout(); got != DefaultMetricsExportTimeout {
		t.Fatalf("default timeout = %v", got)
	}
	t.Setenv(MetricsExporterTimeoutEnv, "1500")
	if got := resolveMetricsExportTimeout(); got != 1500*time.Millisecond {
		t.Fatalf("generic timeout = %v", got)
	}
	t.Setenv(MetricsExporterSignalTimeoutEnv, "25")
	if got := resolveMetricsExportTimeout(); got != 25*time.Millisecond {
		t.Fatalf("signal timeout = %v", got)
	}
	t.Setenv(MetricsExporterSignalTimeoutEnv, "-1")
	if got := resolveMetricsExportTimeout(); got != 1500*time.Millisecond {
		t.Fatalf("negative timeout = %v, want the generic value", got)
	}
}

// A failed export is dropped (the SDK does not retain a failed batch) and does
// not block later recordings.
func TestMetricsClientDropsFailedExports(t *testing.T) {
	doer := &recordingHTTPDoer{err: errors.New("offline")}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("Flush() error = nil, want the transport failure")
	}
	doer.err = nil
	client.Counter("codex.thread.started", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if requests := doer.snapshot(); len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
}

// Rust always installs a periodic reader: a zero interval uses the reader's
// default cadence, while the Go-only negative value leaves the loop off.
func TestMetricsClientExportLoopCadence(t *testing.T) {
	client := NewMetricsClient(MetricsClientOptions{
		Endpoint:   "https://metrics.test/otlp/v1/metrics",
		HTTPClient: &recordingHTTPDoer{},
	})
	if client.done == nil {
		t.Fatal("the default interval did not start the export loop")
	}
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	manual := newTestMetricsClient(t, &recordingHTTPDoer{}, MetricsClientOptions{})
	if manual.done != nil {
		t.Fatal("a negative interval started the export loop")
	}
}

// The description variants carry Rust's instrument description into the OTLP
// metric, and the plain variants leave it empty.
func TestMetricsClientInstrumentDescriptions(t *testing.T) {
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	client.CounterWithDescription("codex.thread.started", "Threads started.", 1, nil)
	client.GaugeWithDescription("codex.gauge", "A gauge.", 2, nil)
	client.Counter("codex.turn.tool.call", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	if got := metrics["codex.thread.started"]["description"]; got != "Threads started." {
		t.Fatalf("counter description = %#v", got)
	}
	if got := metrics["codex.gauge"]["description"]; got != "A gauge." {
		t.Fatalf("gauge description = %#v", got)
	}
	if _, ok := metrics["codex.turn.tool.call"]["description"]; ok {
		t.Fatalf("plain counter carried a description: %#v", metrics["codex.turn.tool.call"])
	}
}

// A real HTTP loopback export verifies the transport end to end.
func TestMetricsClientExportsToLoopbackServer(t *testing.T) {
	type received struct {
		path        string
		contentType string
		body        map[string]any
	}
	receivedCh := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := map[string]any{}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		receivedCh <- received{path: request.URL.Path, contentType: request.Header.Get("Content-Type"), body: payload}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestMetricsClient(t, nil, MetricsClientOptions{})
	client.exporter = NewOTLPMetricsExporter(OTLPMetricsExporterOptions{Endpoint: server.URL + "/v1/metrics"})
	client.Counter("codex.turn.tool.call", 1, nil)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-receivedCh:
		if got.path != "/v1/metrics" || got.contentType != "application/json" {
			t.Fatalf("received = %#v", got)
		}
		if _, ok := metricsByName(t, recordedMetricsRequest{body: got.body})["codex.turn.tool.call"]; !ok {
			t.Fatalf("payload = %#v", got.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the loopback server did not receive an export")
	}
}

func attributeMap(raw any) map[string]string {
	attributes := map[string]string{}
	entries, ok := raw.([]any)
	if !ok {
		return attributes
	}
	for _, entry := range entries {
		attribute, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		key, _ := attribute["key"].(string)
		value, _ := attribute["value"].(map[string]any)
		if value == nil {
			continue
		}
		text, _ := value["stringValue"].(string)
		attributes[key] = text
	}
	return attributes
}

func metricsByName(t *testing.T, request recordedMetricsRequest) map[string]map[string]any {
	t.Helper()
	metrics := map[string]map[string]any{}
	resourceMetrics, ok := request.body["resourceMetrics"].([]any)
	if !ok || len(resourceMetrics) == 0 {
		return metrics
	}
	scoped, ok := resourceMetrics[0].(map[string]any)["scopeMetrics"].([]any)
	if !ok || len(scoped) == 0 {
		return metrics
	}
	entries, _ := scoped[0].(map[string]any)["metrics"].([]any)
	for _, entry := range entries {
		metric, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := metric["name"].(string)
		metrics[name] = metric
	}
	return metrics
}
