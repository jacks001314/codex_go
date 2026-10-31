package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/rollout"
)

func migrationTestOutcome(status rollout.MigrationStatus, thread string, message string) rollout.MigrationOutcome {
	outcome := rollout.MigrationOutcome{Status: status, RolloutPath: "/home/sessions/rollout-" + thread + ".jsonl"}
	if thread != "" {
		outcome.ThreadID = &thread
	}
	if message != "" {
		outcome.Message = &message
	}
	return outcome
}

func TestFormatMigrationElapsedMatchesRust(t *testing.T) {
	// Mirrors Rust migrate_rollouts.rs format_elapsed.
	cases := []struct {
		seconds int
		want    string
	}{
		{0, "0s"},
		{59, "59s"},
		{60, "1m00s"},
		{61, "1m01s"},
		{3599, "59m59s"},
		{3600, "1h00m00s"},
		{3725, "1h02m05s"},
		{90061, "25h01m01s"},
	}
	for _, tc := range cases {
		if got := formatMigrationElapsed(time.Duration(tc.seconds) * time.Second); got != tc.want {
			t.Fatalf("formatMigrationElapsed(%ds) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestFormatMigrationBytesMatchesRust(t *testing.T) {
	// Mirrors Rust migrate_rollouts.rs format_bytes.
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1610612736, "1.5 GB"},
	}
	for _, tc := range cases {
		if got := formatMigrationBytes(tc.bytes); got != tc.want {
			t.Fatalf("formatMigrationBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestMigrateRolloutsHumanReportMatchesRust(t *testing.T) {
	// Mirrors Rust print_human_report: elapsed suffix, the dry-run apply hint,
	// the disk line for --apply, and the exception block printed without
	// --verbose.
	report := &rollout.MigrationReport{Outcomes: []rollout.MigrationOutcome{
		migrationTestOutcome(rollout.MigrationStatusEligible, "123e4567-e89b-42d3-a456-426614174000", ""),
		migrationTestOutcome(rollout.MigrationStatusFailed, "123e4567-e89b-42d3-a456-426614174001", "boom"),
	}}

	var out bytes.Buffer
	printMigrateRolloutsHumanReport(report, cli.MigrateRolloutsOptions{}, 65*time.Second, nil, &out)
	want := strings.Join([]string{
		"Scan complete in 1m05s.",
		"Scanned 2 rollout(s): 1 eligible, 0 already paginated, 0 skipped (0 empty, 0 busy), 1 failed.",
		"Run `codex migrate-rollouts --apply` to migrate eligible sessions.",
		"",
		"failed\t123e4567-e89b-42d3-a456-426614174001\tboom",
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("dry-run report =\n%q\nwant\n%q", out.String(), want)
	}

	out.Reset()
	storage := [2]uint64{567, 297000}
	printMigrateRolloutsHumanReport(report, cli.MigrateRolloutsOptions{Apply: true}, 0, &storage, &out)
	want = strings.Join([]string{
		"Migration complete in 0s.",
		"Scanned 2 rollout(s): 0 migrated, 0 already paginated, 0 skipped (0 empty, 0 busy), 1 failed.",
		"Disk used for thread storage: 567 B -> 290.0 KB",
		"",
		"failed\t123e4567-e89b-42d3-a456-426614174001\tboom",
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("apply report =\n%q\nwant\n%q", out.String(), want)
	}

	out.Reset()
	printMigrateRolloutsHumanReport(report, cli.MigrateRolloutsOptions{Verbose: true}, 0, nil, &out)
	want = strings.Join([]string{
		"Scan complete in 0s.",
		"Scanned 2 rollout(s): 1 eligible, 0 already paginated, 0 skipped (0 empty, 0 busy), 1 failed.",
		"Run `codex migrate-rollouts --apply` to migrate eligible sessions.",
		"eligible\t123e4567-e89b-42d3-a456-426614174000",
		"failed\t123e4567-e89b-42d3-a456-426614174001\tboom",
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("verbose report =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestMigrateRolloutsHumanReportTruncatesExceptionsLikeRust(t *testing.T) {
	report := &rollout.MigrationReport{}
	for index := 0; index < 21; index++ {
		report.Outcomes = append(report.Outcomes, migrationTestOutcome(rollout.MigrationStatusFailed, "", "boom"))
	}
	var out bytes.Buffer
	printMigrateRolloutsHumanReport(report, cli.MigrateRolloutsOptions{}, 0, nil, &out)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2+1+20+1 {
		t.Fatalf("report lines = %d, want 24:\n%s", len(lines), out.String())
	}
	if got, want := lines[len(lines)-1], "... and 1 more; rerun with --json for the complete report."; got != want {
		t.Fatalf("truncation line = %q, want %q", got, want)
	}
}

func TestMigrationProgressPlainMatchesRust(t *testing.T) {
	var stderr bytes.Buffer
	progress := newMigrationProgress(false, false, &stderr)
	progress.begin()
	if stderr.String() != "Scanning local rollouts...\n" {
		t.Fatalf("begin output = %q", stderr.String())
	}
	eligible := rollout.MigrationStatusEligible
	progress.update(rollout.MigrationProgress{ProcessedPaths: 1, TotalPaths: 1200, OutcomeStatus: &eligible})
	progress.update(rollout.MigrationProgress{ProcessedPaths: 999, TotalPaths: 1200, OutcomeStatus: &eligible})
	if strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("plain progress rendered before the 1000-path step: %q", stderr.String())
	}
	progress.update(rollout.MigrationProgress{ProcessedPaths: 1000, TotalPaths: 1200, OutcomeStatus: &eligible})
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if got, want := lines[len(lines)-1], "Checking rollouts  1000/1200 (83%)  •  3 eligible  •  0 already paginated  •  0 skipped  •  0 failed  •  0s"; got != want {
		t.Fatalf("1000-path line = %q, want %q", got, want)
	}
	progress.update(rollout.MigrationProgress{ProcessedPaths: 1200, TotalPaths: 1200, OutcomeStatus: &eligible})
	lines = strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if got, want := lines[len(lines)-1], "Checking rollouts  1200/1200 (100%)  •  4 eligible  •  0 already paginated  •  0 skipped  •  0 failed  •  0s"; got != want {
		t.Fatalf("final line = %q, want %q", got, want)
	}
}

func TestMigrationProgressQuietForJSONMatchesRust(t *testing.T) {
	var stderr bytes.Buffer
	progress := newMigrationProgress(true, true, &stderr)
	progress.begin()
	failed := rollout.MigrationStatusFailed
	progress.update(rollout.MigrationProgress{ProcessedPaths: 1, TotalPaths: 1, OutcomeStatus: &failed})
	progress.finish()
	if stderr.String() != "" {
		t.Fatalf("json progress wrote %q, want no output", stderr.String())
	}
}

type migrationTerminalBuffer struct {
	bytes.Buffer
}

func (migrationTerminalBuffer) IsTerminal() bool { return true }

func TestMigrationProgressTTYMatchesRust(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var stderr migrationTerminalBuffer
	progress := newMigrationProgress(false, false, &stderr)
	progress.begin()
	eligible := rollout.MigrationStatusEligible
	progress.update(rollout.MigrationProgress{ProcessedPaths: 1, TotalPaths: 1, OutcomeStatus: &eligible})
	progress.finish()
	got := stderr.String()
	if !strings.HasPrefix(got, "Scanning local rollouts...\n\r\x1b[2KChecking rollouts  1/1 (100%)") {
		t.Fatalf("tty progress = %q", got)
	}
	if !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("tty progress did not clear the line: %q", got)
	}
}
