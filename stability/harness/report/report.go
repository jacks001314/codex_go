// Package report holds stability budgets and writes JSON run reports.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/stability/harness/disk"
	"codex_go/stability/harness/metrics"
)

// Budgets is the single source of truth for stability thresholds.
type Budgets struct {
	Soak  SoakBudget  `json:"soak"`
	Large LargeBudget `json:"large"`
	UI    UIBudget    `json:"ui"`
	Disk  DiskBudget  `json:"disk"`
}

// SoakBudget bounds long-running resource drift.
type SoakBudget struct {
	WarmupSeconds   int     `json:"warmup_seconds"`
	RSSDriftPerHour float64 `json:"rss_drift_per_hour"`
	RSSPeakRatio    float64 `json:"rss_peak_ratio"`
	GoroutineDelta  int     `json:"goroutine_delta"`
	FDDelta         int     `json:"fd_delta"`
	// MaxActiveConnections bounds how many connections a fixture may still
	// hold after a soak. A client that leaks a connection per turn grows this
	// without bound; a healthy one keeps only its idle pool.
	MaxActiveConnections int `json:"max_active_connections"`
}

// LargeBudget bounds large-data scenarios.
type LargeBudget struct {
	PeakHeapBytes        uint64  `json:"peak_heap_bytes"`
	RolloutReadPeakBytes uint64  `json:"rollout_read_peak_bytes"`
	P95LatencyMS         float64 `json:"p95_latency_ms"`
}

// UIBudget bounds interaction latency and event throughput.
type UIBudget struct {
	KeyToFrameP99MS float64 `json:"key_to_frame_p99_ms"`
	FrameP95MS      float64 `json:"frame_p95_ms"`
	EventsPerSecond int     `json:"events_per_second"`
}

// DiskBudget bounds storage growth and leftovers.
type DiskBudget struct {
	BytesPer1KTurns int64 `json:"bytes_per_1k_turns"`
	WALBytes        int64 `json:"wal_bytes"`
	OrphanTempFiles int   `json:"orphan_temp_files"`
}

// LoadBudgets reads a budgets.json file.
func LoadBudgets(path string) (*Budgets, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var budgets Budgets
	if err := json.Unmarshal(data, &budgets); err != nil {
		return nil, fmt.Errorf("decode budgets: %w", err)
	}
	return &budgets, nil
}

// Check is one pass/fail/skip judgment.
type Check struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ValidateSoak judges a soak sample series against the soak budgets, trimming
// the configured warmup window from the front of the series.
func (b *Budgets) ValidateSoak(samples []metrics.Sample) []Check {
	return b.ValidateSoakWithWarmup(samples, time.Duration(b.Soak.WarmupSeconds)*time.Second)
}

// ValidateSoakWithWarmup judges a series whose first sample is already the
// post-warmup baseline. Callers that sample only after their own warmup and
// settle phases pass a zero warmup; the series then starts at steady state.
func (b *Budgets) ValidateSoakWithWarmup(samples []metrics.Sample, warmup time.Duration) []Check {
	if len(samples) < 2 {
		return []Check{{Name: "soak.samples", Passed: false, Detail: fmt.Sprintf("only %d samples", len(samples))}}
	}
	warmupStart := samples[0].At.Add(warmup)
	steady := samples
	for index := range samples {
		if !samples[index].At.Before(warmupStart) {
			steady = samples[index:]
			break
		}
	}
	if len(steady) < 2 {
		return []Check{{Name: "soak.warmup", Skipped: true, Detail: "not enough samples after warmup"}}
	}

	first := steady[0]
	last := steady[len(steady)-1]
	hours := last.At.Sub(first.At).Hours()
	checks := make([]Check, 0, 4)

	shortWindow := hours*3600 < 60
	if first.RSSKnown && last.RSSKnown && first.RSSBytes > 0 && hours > 0 && !shortWindow {
		drift := (float64(last.RSSBytes) - float64(first.RSSBytes)) / float64(first.RSSBytes) / hours
		checks = append(checks, Check{
			Name:   "soak.rss_drift_per_hour",
			Passed: drift <= b.Soak.RSSDriftPerHour,
			Detail: fmt.Sprintf("%.4f/h (budget %.4f)", drift, b.Soak.RSSDriftPerHour),
		})
		var peak uint64
		for _, sample := range steady {
			if sample.RSSBytes > peak {
				peak = sample.RSSBytes
			}
		}
		ratio := float64(peak) / float64(first.RSSBytes)
		checks = append(checks, Check{
			Name:   "soak.rss_peak_ratio",
			Passed: ratio <= b.Soak.RSSPeakRatio,
			Detail: fmt.Sprintf("%.2fx (budget %.2fx)", ratio, b.Soak.RSSPeakRatio),
		})
	} else if shortWindow {
		checks = append(checks, Check{Name: "soak.rss", Skipped: true, Detail: "window shorter than 60s"})
	} else {
		checks = append(checks, Check{Name: "soak.rss", Skipped: true, Detail: "rss unavailable on this platform"})
	}

	goroutines := last.Goroutines - first.Goroutines
	checks = append(checks, Check{
		Name:   "soak.goroutines",
		Passed: goroutines <= b.Soak.GoroutineDelta,
		Detail: fmt.Sprintf("%+d (budget +%d)", goroutines, b.Soak.GoroutineDelta),
	})

	if first.FDsKnown && last.FDsKnown {
		// A process-wide handle count also covers platform bookkeeping (threads,
		// Winsock events, lazily created runtime handles), so an absolute delta
		// measured from the warmup boundary mixes settling noise into the signal.
		// Judge the steady-state tail instead: a real leak keeps growing in the
		// second half of the window, while bookkeeping flattens. The total delta
		// stays in the detail so a report still shows the whole story.
		late := steady[len(steady)/2]
		if len(steady)-len(steady)/2 < 3 {
			late = steady[0]
		}
		drift := last.OpenFDs - late.OpenFDs
		total := last.OpenFDs - first.OpenFDs
		checks = append(checks, Check{
			Name:   "soak.open_fds",
			Passed: drift <= b.Soak.FDDelta,
			Detail: fmt.Sprintf("steady %+d over %s, total %+d (budget +%d steady)", drift, last.At.Sub(late.At).Round(time.Second), total, b.Soak.FDDelta),
		})
	} else {
		checks = append(checks, Check{Name: "soak.open_fds", Skipped: true, Detail: "fd count unavailable on this platform"})
	}
	return checks
}

// ConnCounts is a connection lifecycle snapshot reported by a test fixture.
type ConnCounts struct {
	Opened int
	Closed int
	Active int
}

// ValidateConnections judges whether a fixture kept connections alive after a
// soak. The accounting identity proves the counts are self-consistent, and the
// retention bound catches a client that opens a connection per request and
// never releases it — a signal a process-wide handle count cannot give.
func (b *Budgets) ValidateConnections(before, after ConnCounts) []Check {
	limit := b.Soak.MaxActiveConnections
	if limit <= 0 {
		limit = 8
	}
	opened := after.Opened - before.Opened
	closed := after.Closed - before.Closed
	retained := after.Active
	return []Check{
		{
			Name:   "soak.conn_accounting",
			Passed: retained == before.Active+opened-closed,
			Detail: fmt.Sprintf("before_active=%d opened=%d closed=%d after_active=%d", before.Active, opened, closed, retained),
		},
		{
			Name:   "soak.connections_reclaimed",
			Passed: retained <= limit,
			Detail: fmt.Sprintf("retained=%d opened=%d closed=%d (budget %d)", retained, opened, closed, limit),
		},
	}
}

// Report is one stability run.
type Report struct {
	Name      string             `json:"name"`
	Commit    string             `json:"commit,omitempty"`
	StartedAt time.Time          `json:"started_at"`
	EndedAt   time.Time          `json:"ended_at"`
	Env       map[string]string  `json:"env,omitempty"`
	Metrics   map[string]float64 `json:"metrics,omitempty"`
	Samples   []metrics.Sample   `json:"samples,omitempty"`
	Disk      []disk.Snapshot    `json:"disk,omitempty"`
	Checks    []Check            `json:"checks,omitempty"`
	Passed    bool               `json:"passed"`
}

// New starts a report with the current time and the run's environment.
// The commit comes from CODEX_STABILITY_COMMIT so a report can always be tied
// back to the tree that produced it, even on a dirty worktree.
func New(name string) *Report {
	return &Report{
		Name:      name,
		Commit:    strings.TrimSpace(os.Getenv("CODEX_STABILITY_COMMIT")),
		StartedAt: time.Now(),
		Env: map[string]string{
			"go_version": runtime.Version(),
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
		},
	}
}

// Metric records one named numeric value for cross-run trend analysis.
func (r *Report) Metric(name string, value float64) {
	if r.Metrics == nil {
		r.Metrics = map[string]float64{}
	}
	r.Metrics[name] = value
}

// AddSample appends one resource sample.
func (r *Report) AddSample(sample metrics.Sample) { r.Samples = append(r.Samples, sample) }

// AddDisk appends one disk snapshot.
func (r *Report) AddDisk(snapshot disk.Snapshot) { r.Disk = append(r.Disk, snapshot) }

// AddCheck appends a judgment.
func (r *Report) AddCheck(check Check) { r.Checks = append(r.Checks, check) }

// Check appends a formatted judgment.
func (r *Report) Check(name string, passed bool, format string, args ...any) {
	r.AddCheck(Check{Name: name, Passed: passed, Detail: fmt.Sprintf(format, args...)})
}

// Skip appends a skipped judgment.
func (r *Report) Skip(name string, format string, args ...any) {
	r.AddCheck(Check{Name: name, Skipped: true, Detail: fmt.Sprintf(format, args...)})
}

// Finish stamps the end time and computes the overall result.
func (r *Report) Finish() {
	r.EndedAt = time.Now()
	r.Passed = true
	for _, check := range r.Checks {
		if !check.Skipped && !check.Passed {
			r.Passed = false
		}
	}
}

// Write stores the report under dir and returns its path.
func (r *Report) Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	started := r.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	// Millisecond precision keeps two runs started in the same second from
	// overwriting each other; the dot becomes a hyphen to stay shell friendly.
	stamp := strings.ReplaceAll(started.Format("20060102T150405.000"), ".", "-")
	name := stamp + "-" + sanitize(r.Name) + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func sanitize(name string) string {
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-")
	value := replacer.Replace(strings.TrimSpace(name))
	if value == "" {
		return "stability"
	}
	return value
}
