package appserver

import (
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/mcp"
)

type oauthStorageMetricRecorder struct {
	counters  []map[string]string
	durations []string
}

func (r *oauthStorageMetricRecorder) Counter(name string, inc int, tags map[string]string) {
	if name == "codex.auth_storage.operation" {
		copied := map[string]string{}
		for key, value := range tags {
			copied[key] = value
		}
		r.counters = append(r.counters, copied)
	}
}

func (r *oauthStorageMetricRecorder) Histogram(string, int, map[string]string) {}

func (r *oauthStorageMetricRecorder) HistogramWithBounds(string, int, []float64, map[string]string) {
}

func (r *oauthStorageMetricRecorder) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	if name == "codex.auth_storage.duration" {
		r.durations = append(r.durations, tags["operation"])
	}
}

func TestOAuthStorageTelemetryEmitsBoundedMetricsLikeRust(t *testing.T) {
	metrics := &oauthStorageMetricRecorder{}
	router := &RuntimeRouter{services: RuntimeServices{TurnMetrics: metrics}}
	router.configureOAuthStorageTelemetry()
	defer mcp.SetOAuthStorageTelemetryObserver(nil)
	defer auth.SetStorageTelemetryObserver(nil)
	store := mcp.NewOAuthStoreWithMode(t.TempDir(), mcp.OAuthCredentialsStoreFile)
	if err := store.Save(&mcp.OAuthTokenSet{
		ServerName: "docs", ServerURL: "https://example.test/mcp", ClientID: "client", AccessToken: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	authStore := auth.NewStoreWithOptions(t.TempDir(), &auth.StoreOptions{Mode: auth.AuthCredentialsStoreFile})
	if err := authStore.Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatal(err)
	}
	if len(metrics.counters) != 2 || len(metrics.durations) != 2 {
		t.Fatalf("metrics = %#v/%#v", metrics.counters, metrics.durations)
	}
	tags := metrics.counters[0]
	if tags["credential_kind"] != "mcp" || tags["operation"] != "save" || tags["outcome"] != "success" ||
		tags["actual_store"] != "file" || tags["fallback_reason"] != "none" || tags["originator"] != "mcp" {
		t.Fatalf("telemetry tags = %#v", tags)
	}
	for _, sensitive := range []string{"docs", "https://example.test/mcp", "secret"} {
		for _, value := range tags {
			if value == sensitive {
				t.Fatalf("sensitive value %q leaked into telemetry tags: %#v", sensitive, tags)
			}
		}
	}
	authTags := metrics.counters[1]
	if authTags["credential_kind"] != "codex" || authTags["operation"] != "save" || authTags["outcome"] != "success" ||
		authTags["actual_store"] != "file" || authTags["originator"] != "cli" {
		t.Fatalf("CLI auth telemetry tags = %#v", authTags)
	}
}
