package plugin

import (
	"errors"
	"testing"
	"time"
)

func curatedSyncMetricsService(t *testing.T, materializeErr error) (*PluginService, chan CuratedSyncMetrics) {
	t.Helper()
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	samples := make(chan CuratedSyncMetrics, 4)
	service.SetCuratedSyncMetricsObserver(func(sample CuratedSyncMetrics) {
		samples <- sample
	})
	service.SetMarketplaceMaterializer(MarketplaceMaterializerFunc(func(*ParsedMarketplaceSource, []string, string) error {
		return materializeErr
	}))
	return service, samples
}

// Mirrors Rust's emit_curated_plugins_startup_sync_counter: a successful git
// sync records the per-attempt and final counters.
func TestCuratedSyncRecordsSuccessMetricsLikeRust(t *testing.T) {
	service, samples := curatedSyncMetricsService(t, nil)
	if !service.StartCuratedRepoSync(nil) {
		t.Fatal("curated repo sync did not start")
	}
	var got []CuratedSyncMetrics
	deadline := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case sample := <-samples:
			got = append(got, sample)
		case <-deadline:
			t.Fatalf("samples = %#v", got)
		}
	}
	if got[0] != (CuratedSyncMetrics{Transport: "git", Status: "success"}) {
		t.Fatalf("first sample = %#v", got[0])
	}
	if got[1] != (CuratedSyncMetrics{Transport: "git", Status: "success", Final: true}) {
		t.Fatalf("final sample = %#v", got[1])
	}
}

// A failed git sync records the per-attempt failure; Go has no HTTP or export
// archive fallback transport, so it records no final sample.
func TestCuratedSyncRecordsFailureMetricsLikeRust(t *testing.T) {
	service, samples := curatedSyncMetricsService(t, errors.New("offline"))
	if !service.StartCuratedRepoSync(nil) {
		t.Fatal("curated repo sync did not start")
	}
	select {
	case sample := <-samples:
		if sample != (CuratedSyncMetrics{Transport: "git", Status: "failure"}) {
			t.Fatalf("sample = %#v", sample)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no failure sample recorded")
	}
	select {
	case sample := <-samples:
		t.Fatalf("unexpected extra sample = %#v", sample)
	case <-time.After(50 * time.Millisecond):
	}
}
