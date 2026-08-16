package doctor

import "testing"

func TestDiskCheckThresholdsLikeRust(t *testing.T) {
	measure := func(available uint64) func(string) (uint64, bool) {
		return func(string) (uint64, bool) { return available, true }
	}
	cases := []struct {
		name      string
		available uint64
		status    CheckStatus
		summary   string
		issues    int
	}{
		{"plenty", 10 * diskGiB, CheckStatusOK, "sufficient free disk space (10.0 GiB)", 0},
		{"low", 2 * diskGiB, CheckStatusWarning, "low disk space (2.0 GiB)", 2},
		{"critical", diskGiB - 1, CheckStatusFail, "critically low disk space (1024.0 MiB)", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := diskCheckWithMeasure("/codex", "/worktree", measure(tc.available))
			if check.Status != tc.status {
				t.Fatalf("status = %q, want %q", check.Status, tc.status)
			}
			if len(check.Issues) != tc.issues {
				t.Fatalf("issues = %d, want %d", len(check.Issues), tc.issues)
			}
			if check.Summary != tc.summary {
				t.Fatalf("summary = %q, want %q", check.Summary, tc.summary)
			}
		})
	}
}

func TestDiskCheckMeasurementFailuresAndMissingPaths(t *testing.T) {
	fail := func(string) (uint64, bool) { return 0, false }
	check := diskCheckWithMeasure("/codex", "/worktree", fail)
	if check.Status != CheckStatusWarning {
		t.Fatalf("measurement failure status = %q, want warning", check.Status)
	}
	if len(check.Issues) != 2 {
		t.Fatalf("measurement failure issues = %d, want 2", len(check.Issues))
	}
	missing := diskCheckWithMeasure("", "", func(string) (uint64, bool) { return 0, false })
	if missing.Status != CheckStatusWarning {
		t.Fatalf("missing paths status = %q, want warning", missing.Status)
	}
	if missing.Summary != "disk capacity could not be fully verified" {
		t.Fatalf("missing paths summary = %q", missing.Summary)
	}
}

func TestFormatDiskCapacity(t *testing.T) {
	if got := formatDiskCapacity(diskGiB); got != "1.0 GiB" {
		t.Fatalf("formatDiskCapacity(1GiB) = %q", got)
	}
	if got := formatDiskCapacity(3 * diskGiB / 2); got != "1.5 GiB" {
		t.Fatalf("formatDiskCapacity(1.5GiB) = %q", got)
	}
	if got := formatDiskCapacity(512 * 1024 * 1024); got != "512.0 MiB" {
		t.Fatalf("formatDiskCapacity(512MiB) = %q", got)
	}
}
