package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"codex_go/rollout"
	"codex_go/state"
)

// migrationProgressOutput mirrors Rust migrate_rollouts.rs ProgressOutput.
type migrationProgressOutput int

const (
	migrationProgressQuiet migrationProgressOutput = iota
	migrationProgressTTY
	migrationProgressPlain
)

const (
	migrationTTYProgressInterval = 250 * time.Millisecond
	migrationNonTTYProgressStep  = 1000
	migrationMaxExceptionDetails = 20
)

// migrationCounts mirrors Rust MigrationCounts.
type migrationCounts struct {
	eligible         int
	migrated         int
	alreadyPaginated int
	skippedEmpty     int
	skippedBusy      int
	failed           int
}

func (c *migrationCounts) observe(status rollout.MigrationStatus) {
	switch status {
	case rollout.MigrationStatusEligible:
		c.eligible++
	case rollout.MigrationStatusMigrated:
		c.migrated++
	case rollout.MigrationStatusAlreadyPaginated:
		c.alreadyPaginated++
	case rollout.MigrationStatusSkippedEmpty:
		c.skippedEmpty++
	case rollout.MigrationStatusSkippedBusy:
		c.skippedBusy++
	case rollout.MigrationStatusFailed:
		c.failed++
	}
}

func (c migrationCounts) skipped() int { return c.skippedEmpty + c.skippedBusy }

// migrationProgress mirrors Rust MigrationProgress: the stderr progress line fed
// by the rollout migration callback, plus the elapsed clock the report reuses.
type migrationProgress struct {
	apply          bool
	output         migrationProgressOutput
	stderr         io.Writer
	startedAt      time.Time
	lastRenderedAt time.Time
	lastPlainCount int
	counts         migrationCounts
	wroteTTYLine   bool
}

func newMigrationProgress(apply, jsonOutput bool, stderr io.Writer) *migrationProgress {
	now := time.Now()
	output := migrationProgressPlain
	if jsonOutput {
		output = migrationProgressQuiet
	} else if migrationStderrIsTerminal(stderr) && os.Getenv("TERM") != "dumb" {
		output = migrationProgressTTY
	}
	return &migrationProgress{
		apply:          apply,
		output:         output,
		stderr:         stderr,
		startedAt:      now,
		lastRenderedAt: now,
	}
}

func migrationStderrIsTerminal(stderr io.Writer) bool {
	if terminal, ok := stderr.(interface{ IsTerminal() bool }); ok {
		return terminal.IsTerminal()
	}
	file, ok := stderr.(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p *migrationProgress) begin() {
	if p.output != migrationProgressQuiet {
		fmt.Fprintln(p.stderr, "Scanning local rollouts...")
	}
}

func (p *migrationProgress) update(update rollout.MigrationProgress) {
	if update.OutcomeStatus != nil {
		p.counts.observe(*update.OutcomeStatus)
	}
	switch p.output {
	case migrationProgressTTY:
		if update.ProcessedPaths == update.TotalPaths || time.Since(p.lastRenderedAt) >= migrationTTYProgressInterval {
			fmt.Fprint(p.stderr, "\r\x1b[2K"+p.line(update))
			p.lastRenderedAt = time.Now()
			p.wroteTTYLine = true
		}
	case migrationProgressPlain:
		if update.ProcessedPaths == update.TotalPaths || update.ProcessedPaths-p.lastPlainCount >= migrationNonTTYProgressStep {
			fmt.Fprintln(p.stderr, p.line(update))
			p.lastPlainCount = update.ProcessedPaths
		}
	}
}

func (p *migrationProgress) finish() {
	if p.output != migrationProgressTTY || !p.wroteTTYLine {
		return
	}
	fmt.Fprint(p.stderr, "\r\x1b[2K")
	p.wroteTTYLine = false
}

func (p *migrationProgress) elapsed() time.Duration { return time.Since(p.startedAt) }

func (p *migrationProgress) line(update rollout.MigrationProgress) string {
	percent := 100
	if update.TotalPaths > 0 {
		percent = update.ProcessedPaths * 100 / update.TotalPaths
	}
	action := "Checking"
	statusCounts := fmt.Sprintf("%d eligible  •  %d already paginated", p.counts.eligible, p.counts.alreadyPaginated)
	if p.apply {
		action = "Migrating"
		statusCounts = fmt.Sprintf("%d migrated  •  %d already paginated", p.counts.migrated, p.counts.alreadyPaginated)
	}
	return fmt.Sprintf("%s rollouts  %d/%d (%d%%)  •  %s  •  %d skipped  •  %d failed  •  %s",
		action, update.ProcessedPaths, update.TotalPaths, percent, statusCounts,
		p.counts.skipped(), p.counts.failed, formatMigrationElapsed(p.elapsed()))
}

// formatMigrationElapsed mirrors Rust format_elapsed.
func formatMigrationElapsed(elapsed time.Duration) string {
	seconds := int64(elapsed / time.Second)
	minutes := seconds / 60
	if minutes == 0 {
		return fmt.Sprintf("%ds", seconds)
	}
	hours := minutes / 60
	if hours == 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", hours, minutes%60, seconds%60)
}

// formatMigrationBytes mirrors Rust format_bytes.
func formatMigrationBytes(bytes uint64) string {
	const kib = 1024.0
	const mib = kib * 1024.0
	const gib = mib * 1024.0
	value := float64(bytes)
	switch {
	case value >= gib:
		return fmt.Sprintf("%.1f GB", value/gib)
	case value >= mib:
		return fmt.Sprintf("%.1f MB", value/mib)
	case value >= kib:
		return fmt.Sprintf("%.1f KB", value/kib)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// migrationThreadStorageBytes mirrors Rust thread_storage_bytes: every regular
// file under the active and archived session roots plus the thread history
// database and its write-ahead companions.
func migrationThreadStorageBytes(codexHome, threadHistoryDBPath string) (uint64, error) {
	var bytes uint64
	directories := []string{
		filepath.Join(codexHome, rollout.SessionsSubdir),
		filepath.Join(codexHome, rollout.ArchivedSessionsSubdir),
	}
	for len(directories) > 0 {
		directory := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		entries, err := os.ReadDir(directory)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		for _, entry := range entries {
			// Rust checks the entry type: symlinks are neither files nor
			// directories and contribute nothing.
			switch {
			case entry.IsDir():
				directories = append(directories, filepath.Join(directory, entry.Name()))
			case entry.Type().IsRegular():
				info, err := entry.Info()
				if err != nil {
					return 0, err
				}
				bytes += uint64(info.Size())
			}
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(threadHistoryDBPath + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		bytes += uint64(info.Size())
	}
	return bytes, nil
}

func migrationThreadHistoryDBPath(codexHome string) (string, error) {
	config, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return "", err
	}
	return config.ThreadHistoryDBPath(), nil
}
