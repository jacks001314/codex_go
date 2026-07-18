package windowssandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestInstallWFPFiltersRecordsSuccessMetric(t *testing.T) {
	var logs []string
	var emitted []WFPSetupMetric
	metric := installWFPFiltersWithInstaller(
		t.TempDir(),
		OfflineUsername,
		func(line string) { logs = append(logs, line) },
		func(account string) (int, error) {
			if account != OfflineUsername {
				t.Fatalf("installer account = %q, want %q", account, OfflineUsername)
			}
			return 12, nil
		},
		func(metric WFPSetupMetric) error {
			emitted = append(emitted, metric)
			return nil
		},
	)
	if metric.Outcome != WFPSetupMetricSuccess || metric.InstalledFilterCount != 12 {
		t.Fatalf("metric = %#v, want success with 12 filters", metric)
	}
	if metric.Name() != WFPSetupSuccessMetric {
		t.Fatalf("metric.Name() = %q, want success metric", metric.Name())
	}
	if tags := metric.Tags(); tags["installed_filter_count"] != "12" || tags["target_account"] != OfflineUsername {
		t.Fatalf("metric.Tags() = %#v", tags)
	}
	if len(emitted) != 1 || emitted[0].Outcome != WFPSetupMetricSuccess {
		t.Fatalf("emitted = %#v, want one success metric", emitted)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "WFP setup succeeded") {
		t.Fatalf("logs = %#v, want success log", logs)
	}
}

func TestInstallWFPFiltersRecordsFailureMetricAndContinues(t *testing.T) {
	var logs []string
	metric := installWFPFiltersWithInstaller(
		t.TempDir(),
		OfflineUsername,
		func(line string) { logs = append(logs, line) },
		func(string) (int, error) { return 0, errors.New("boom") },
	)
	if metric.Outcome != WFPSetupMetricFailure || metric.Error != "boom" {
		t.Fatalf("metric = %#v, want failure boom", metric)
	}
	if metric.Name() != WFPSetupFailureMetric {
		t.Fatalf("metric.Name() = %q, want failure metric", metric.Name())
	}
	if tags := metric.Tags(); tags["message"] != "boom" {
		t.Fatalf("metric.Tags() = %#v, want sanitized error", tags)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "continuing elevated setup") {
		t.Fatalf("logs = %#v, want continuing log", logs)
	}
}

func TestInstallWFPFiltersRecoversInstallerPanic(t *testing.T) {
	metric := installWFPFiltersWithInstaller(
		t.TempDir(),
		OfflineUsername,
		nil,
		func(string) (int, error) { panic("flameout") },
	)
	if metric.Outcome != WFPSetupMetricFailure || metric.Error != "panic: flameout" {
		t.Fatalf("metric = %#v, want recovered panic", metric)
	}
}

func TestInstallWFPFiltersMetricEmitterIsBestEffort(t *testing.T) {
	var logs []string
	metric := installWFPFiltersWithInstaller(
		t.TempDir(),
		OfflineUsername,
		func(line string) { logs = append(logs, line) },
		func(string) (int, error) { return 12, nil },
		func(WFPSetupMetric) error { return errors.New("emit failed") },
		func(WFPSetupMetric) error { panic("emit panic") },
	)
	if metric.Outcome != WFPSetupMetricSuccess {
		t.Fatalf("metric = %#v, want success despite emitter failures", metric)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "failed to emit WFP setup metric") || !strings.Contains(joined, "metric emission panicked") {
		t.Fatalf("logs = %#v, want emitter error and panic logs", logs)
	}
}
