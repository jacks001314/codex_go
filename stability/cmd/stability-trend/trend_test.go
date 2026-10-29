package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/report"
)

func writeReport(t *testing.T, dir, name string, start time.Time, value float64) string {
	t.Helper()
	run := &report.Report{Name: name, StartedAt: start, Passed: true}
	run.Metric("memory.peak_heap_bytes", value)
	path, err := run.Write(dir)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}

func TestTrendStableSeriesIsOK(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 5; day++ {
		value := 100e6 + float64(day%2)*1e6
		writeReport(t, dir, "memory-long-session", start.Add(time.Duration(day)*24*time.Hour), value)
	}

	result, err := Analyze(Options{Dir: dir, Last: 7, MinPoints: 3, GrowthPerHour: 0.01})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !result.OK {
		t.Fatalf("stable series alerted: %v", result.Alerts)
	}
	if len(result.Scenarios) != 1 || len(result.Scenarios[0].Series) == 0 {
		t.Fatalf("scenarios = %+v, want one scenario with series", result.Scenarios)
	}
}

func TestTrendRegressionAlerts(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 5; day++ {
		// Doubling every day is ~4.2%/h, far above the 1%/h budget.
		value := 100e6 * (1 + 1.0*float64(day))
		writeReport(t, dir, "memory-long-session", start.Add(time.Duration(day)*24*time.Hour), value)
	}

	result, err := Analyze(Options{Dir: dir, Last: 7, MinPoints: 3, GrowthPerHour: 0.01})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.OK {
		t.Fatal("growing series did not alert")
	}
	if len(result.Alerts) == 0 || !strings.Contains(result.Alerts[0], "memory.peak_heap_bytes") {
		t.Fatalf("alerts = %v, want the heap metric named", result.Alerts)
	}
	series := result.Scenarios[0].Series[0]
	if !series.Alert || series.RelGrowthPerHour <= 0.01 {
		t.Fatalf("series = %+v, want an alerting positive slope", series)
	}
}

func TestTrendFailedRunAlerts(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	run := &report.Report{Name: "soak", StartedAt: start, Passed: false}
	run.Metric("soak.turns", 10)
	if _, err := run.Write(dir); err != nil {
		t.Fatalf("write report: %v", err)
	}

	result, err := Analyze(Options{Dir: dir, Last: 7, MinPoints: 1, GrowthPerHour: 0.01})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.OK {
		t.Fatal("failed run did not alert")
	}
	if !strings.Contains(result.Alerts[0], "soak") {
		t.Fatalf("alerts = %v, want the scenario named", result.Alerts)
	}
}

func TestTrendDerivesSeriesFromSamples(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 3; day++ {
		run := &report.Report{Name: "soak", StartedAt: start.Add(time.Duration(day) * 24 * time.Hour), Passed: true}
		run.AddSample(metrics.Sample{
			At:             run.StartedAt,
			HeapAllocBytes: uint64(10e6),
			Goroutines:     10,
			OpenFDs:        20,
			FDsKnown:       true,
			RSSKnown:       true,
			RSSBytes:       uint64(20e6),
		})
		if _, err := run.Write(dir); err != nil {
			t.Fatalf("write report: %v", err)
		}
	}

	result, err := Analyze(Options{Dir: dir, Last: 7, MinPoints: 3, GrowthPerHour: 0.01})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	names := map[string]bool{}
	for _, series := range result.Scenarios[0].Series {
		names[series.Metric] = true
	}
	if !names["samples.heap_peak_bytes"] || !names["samples.goroutines_final"] {
		t.Fatalf("derived series = %v, want heap and goroutine series", names)
	}
}

func TestTrendIgnoresUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write broken file: %v", err)
	}
	if _, err := Analyze(Options{Dir: dir}); err == nil {
		t.Fatal("Analyze accepted a malformed report")
	}
}

func TestTrendShortSpanDoesNotAlert(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for minute := 0; minute < 4; minute++ {
		value := 100e6 * (1 + float64(minute))
		writeReport(t, dir, "soak", start.Add(time.Duration(minute)*time.Minute), value)
	}

	result, err := Analyze(Options{Dir: dir, Last: 7, MinPoints: 3, GrowthPerHour: 0.01, MinSpanHours: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !result.OK {
		t.Fatalf("minutes-long series alerted: %v", result.Alerts)
	}
}
