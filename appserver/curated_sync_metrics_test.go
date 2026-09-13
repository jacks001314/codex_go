package appserver

import (
	"testing"
	"time"

	"codex_go/plugin"
	"codex_go/state"
	"codex_go/telemetry"
)

// The default router installs the curated-plugin sync observer on its plugin
// service, so a sync records Rust's startup-sync counters in the runtime
// metrics sink.
func TestDefaultRouterCuratedSyncRecordsMetricsLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(nil, home, nil)
	defer router.Close()

	metrics, ok := router.services.TurnMetrics.(*state.TaskMetrics)
	if !ok {
		t.Fatalf("turn metrics = %T", router.services.TurnMetrics)
	}
	if router.services.Plugins == nil {
		t.Fatal("plugins service is missing")
	}
	router.services.Plugins.SetMarketplaceMaterializer(plugin.MarketplaceMaterializerFunc(func(*plugin.ParsedMarketplaceSource, []string, string) error {
		return nil
	}))
	if !router.services.Plugins.StartCuratedRepoSync(nil) {
		t.Fatal("curated repo sync did not start")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		synced, final := false, false
		for _, record := range metrics.Records() {
			if record.Tags["transport"] != "git" || record.Tags["status"] != "success" {
				continue
			}
			switch record.Name {
			case telemetry.CuratedPluginsStartupSyncMetric:
				synced = true
			case telemetry.CuratedPluginsStartupSyncFinalMetric:
				final = true
			}
		}
		if synced && final {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("records = %#v (synced=%v final=%v)", metrics.Records(), synced, final)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
