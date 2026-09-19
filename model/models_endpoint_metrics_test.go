package model

import (
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
)

type recordedModelMetric struct {
	name string
	tags map[string]string
}

type modelMetricsRecorder struct {
	mu    sync.Mutex
	calls []recordedModelMetric
}

func (r *modelMetricsRecorder) RecordDuration(name string, _ time.Duration, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedModelMetric{name: name, tags: tags})
}

func (r *modelMetricsRecorder) Counter(name string, _ int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedModelMetric{name: name, tags: tags})
}

func (r *modelMetricsRecorder) Histogram(name string, _ int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedModelMetric{name: name, tags: tags})
}

// Mirrors Rust #46570: the remote-model fetch duration is tagged with the
// authentication mode used for the fetch.
func TestRemoteModelsFetchRecordsAuthModeMetricLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		options RemoteModelsManagerOptions
		want    string
	}{
		{
			name:    "provider api key",
			options: RemoteModelsManagerOptions{SupportsAPIKeyModels: true, APIKeyAuth: true, HasAuth: true},
			want:    "api_key",
		},
		{
			name:    "chatgpt auth",
			options: RemoteModelsManagerOptions{SupportsAPIKeyModels: true, HasAuth: true},
			want:    "chatgpt",
		},
		{
			name:    "no auth",
			options: RemoteModelsManagerOptions{},
			want:    "none",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &modelMetricsRecorder{}
			metrics.InstallGlobal(recorder)
			t.Cleanup(func() { metrics.InstallGlobal(nil) })

			endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
				Models: []ModelInfo{{Slug: "remote", Visibility: VisibilityList, SupportedInAPI: true}},
			}}}
			options := testCase.options
			options.Endpoint = endpoint
			manager := NewRemoteModelsManagerWithOptions(&options)
			manager.SetAPIKeyModelDiscoveryEnabled(true)
			manager.RawModelCatalog(RefreshOnline)

			if len(recorder.calls) != 1 {
				t.Fatalf("recorded %d metrics, want 1: %#v", len(recorder.calls), recorder.calls)
			}
			call := recorder.calls[0]
			if call.name != remoteModelsFetchUpdateDurationMetric {
				t.Fatalf("metric name = %q, want %q", call.name, remoteModelsFetchUpdateDurationMetric)
			}
			if call.tags["auth_mode"] != testCase.want {
				t.Fatalf("auth_mode = %q, want %q", call.tags["auth_mode"], testCase.want)
			}
		})
	}
}

// A provider API key counts as API-key auth even when the picker used ChatGPT
// (Rust's `has_provider_api_key() || auth.is_api_key_auth()`).
func TestRemoteModelsMetricsAuthModePrefersAPIKeyLikeRust(t *testing.T) {
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{APIKeyAuth: true, HasAuth: true})
	if got := manager.metricsAuthMode(); got != "api_key" {
		t.Fatalf("metricsAuthMode() = %q, want api_key", got)
	}
}

// An explicit override wins, which lets the TUI picker classify a credential it
// resolved itself without changing the discovery gate.
func TestRemoteModelsMetricsAuthModeOverride(t *testing.T) {
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		SupportsAPIKeyModels: true,
		APIKeyAuth:           true,
		HasAuth:              true,
		MetricsAuthMode:      " chatgpt ",
	})
	if got := manager.metricsAuthMode(); got != "chatgpt" {
		t.Fatalf("metricsAuthMode() = %q, want chatgpt", got)
	}
	if !manager.SupportsAPIKeyDiscovery() {
		t.Fatal("the metric override must not change the discovery gate")
	}
}
