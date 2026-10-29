package report_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/report"
)

func TestValidateSoakPassesAndFails(t *testing.T) {
	budgets := &report.Budgets{Soak: report.SoakBudget{
		RSSDriftPerHour: 0.01,
		RSSPeakRatio:    1.1,
		GoroutineDelta:  2,
		FDDelta:         2,
	}}
	start := time.Now()
	good := []metrics.Sample{
		{At: start, RSSBytes: 1000, RSSKnown: true, Goroutines: 10, OpenFDs: 20, FDsKnown: true},
		{At: start.Add(time.Hour), RSSBytes: 1005, RSSKnown: true, Goroutines: 11, OpenFDs: 21, FDsKnown: true},
	}
	for _, check := range budgets.ValidateSoak(good) {
		if !check.Skipped && !check.Passed {
			t.Fatalf("good series failed %s: %s", check.Name, check.Detail)
		}
	}

	bad := []metrics.Sample{
		good[0],
		{At: start.Add(time.Hour), RSSBytes: 4000, RSSKnown: true, Goroutines: 100, OpenFDs: 80, FDsKnown: true},
	}
	failed := false
	for _, check := range budgets.ValidateSoak(bad) {
		if !check.Skipped && !check.Passed {
			failed = true
		}
	}
	if !failed {
		t.Fatal("bad series passed every soak check")
	}
}

// TestValidateSoakJudgesSteadyStateHandles pins the handle judgment to the
// second half of the window. Platform bookkeeping inflates the absolute delta
// during warmup (a 15k-turn soak measured 927 opened / 927 closed connections
// and an fd delta that swung between 0 and 8 across identical runs), so only
// growth that continues at the end of the window is treated as a leak.
func TestValidateSoakJudgesSteadyStateHandles(t *testing.T) {
	budgets := &report.Budgets{Soak: report.SoakBudget{GoroutineDelta: 2, FDDelta: 2}}
	start := time.Now()
	sample := func(offset time.Duration, fds int) metrics.Sample {
		return metrics.Sample{At: start.Add(offset), Goroutines: 10, OpenFDs: fds, FDsKnown: true}
	}

	// Handles jump at the start and then stay flat: bookkeeping, not a leak.
	settled := []metrics.Sample{
		sample(0, 20), sample(time.Second, 28), sample(2*time.Second, 28),
		sample(3*time.Second, 28), sample(4*time.Second, 28), sample(5*time.Second, 28),
	}
	for _, check := range budgets.ValidateSoak(settled) {
		if !check.Skipped && !check.Passed {
			t.Fatalf("settled series failed %s: %s", check.Name, check.Detail)
		}
	}

	// Handles keep growing through the tail: a leak.
	growing := []metrics.Sample{
		sample(0, 20), sample(time.Second, 20), sample(2*time.Second, 20),
		sample(3*time.Second, 30), sample(4*time.Second, 40), sample(5*time.Second, 50),
	}
	failed := false
	for _, check := range budgets.ValidateSoak(growing) {
		if check.Name == "soak.open_fds" && !check.Skipped && !check.Passed {
			failed = true
		}
	}
	if !failed {
		t.Fatal("growing handle count passed the steady-state check")
	}
}

func TestValidateConnections(t *testing.T) {
	budgets := &report.Budgets{Soak: report.SoakBudget{MaxActiveConnections: 8}}
	before := report.ConnCounts{Opened: 10, Closed: 8, Active: 2}

	reclaimed := report.ConnCounts{Opened: 927, Closed: 925, Active: 2}
	for _, check := range budgets.ValidateConnections(before, reclaimed) {
		if !check.Passed {
			t.Fatalf("reclaimed connections failed %s: %s", check.Name, check.Detail)
		}
	}

	leaked := report.ConnCounts{Opened: 2000, Closed: 10, Active: 1990}
	checks := budgets.ValidateConnections(before, leaked)
	if !checks[0].Passed {
		t.Fatalf("consistent counts failed accounting: %+v", checks[0])
	}
	if checks[1].Passed {
		t.Fatalf("retained connections passed: %+v", checks[1])
	}

	inconsistent := report.ConnCounts{Opened: 5, Closed: 5, Active: 9}
	if checks := budgets.ValidateConnections(before, inconsistent); checks[0].Passed {
		t.Fatalf("accounting mismatch passed: %+v", checks[0])
	}
}

// TestValidateConnectionsDefaultsBudget keeps an older budgets file usable: a
// missing max_active_connections falls back to a small bound instead of zero.
func TestValidateConnectionsDefaultsBudget(t *testing.T) {
	budgets := &report.Budgets{}
	checks := budgets.ValidateConnections(report.ConnCounts{}, report.ConnCounts{Opened: 3, Closed: 2, Active: 1})
	if !checks[0].Passed || !checks[1].Passed {
		t.Fatalf("default budget rejected a single retained connection: %+v", checks)
	}
}

func TestValidateSoakSkipsWithoutSamples(t *testing.T) {
	budgets := &report.Budgets{}
	checks := budgets.ValidateSoak(nil)
	if len(checks) != 1 || checks[0].Passed {
		t.Fatalf("checks = %+v, want one failed sample check", checks)
	}
}

func TestWriteStoresJSON(t *testing.T) {
	dir := t.TempDir()
	run := report.New("soak smoke")
	run.Check("example", true, "all good")
	run.Finish()
	path, err := run.Write(dir)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), `"passed": true`) {
		t.Fatalf("report missing pass flag: %s", data)
	}
	if !strings.Contains(string(data), `"name": "example"`) {
		t.Fatalf("report missing check: %s", data)
	}
}
