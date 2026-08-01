package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryStage1JobLifecycle(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)

	claim, err := runtime.TryClaimStage1Job(ctx, "thread-a", "worker-a", 100, 3600, 8)
	if err != nil || claim.Outcome != Stage1JobClaimed || claim.OwnershipToken == "" {
		t.Fatalf("first claim = %+v, %v", claim, err)
	}
	duplicate, err := runtime.TryClaimStage1Job(ctx, "thread-a", "worker-b", 100, 3600, 8)
	if err != nil || duplicate.Outcome != Stage1JobSkippedRunning {
		t.Fatalf("duplicate claim = %+v, %v", duplicate, err)
	}
	if updated, err := runtime.MarkStage1JobSucceeded(ctx, "thread-a", "wrong-token", 100, "raw", "summary", nil); err != nil || updated {
		t.Fatalf("wrong-token success = %v, %v", updated, err)
	}
	slug := "rollout-a"
	if updated, err := runtime.MarkStage1JobSucceeded(ctx, "thread-a", claim.OwnershipToken, 100, "raw", "summary", &slug); err != nil || !updated {
		t.Fatalf("success = %v, %v", updated, err)
	}
	assertMemoryStage1Output(t, runtime, "thread-a", 100, "raw", "summary", sql.NullString{String: slug, Valid: true})
	assertMemoryJob(t, runtime, MemoryJobKindStage1, "thread-a", "done", 3, 100, sql.NullInt64{Int64: 100, Valid: true})
	assertMemoryJob(t, runtime, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, "pending", 3, 100, sql.NullInt64{Int64: 0, Valid: true})

	upToDate, err := runtime.TryClaimStage1Job(ctx, "thread-a", "worker-a", 100, 3600, 8)
	if err != nil || upToDate.Outcome != Stage1JobSkippedUpToDate {
		t.Fatalf("up-to-date claim = %+v, %v", upToDate, err)
	}
	newer, err := runtime.TryClaimStage1Job(ctx, "thread-a", "worker-a", 101, 3600, 8)
	if err != nil || newer.Outcome != Stage1JobClaimed {
		t.Fatalf("newer-watermark claim = %+v, %v", newer, err)
	}
	if updated, err := runtime.MarkStage1JobSucceededNoOutput(ctx, "thread-a", newer.OwnershipToken); err != nil || !updated {
		t.Fatalf("no-output success = %v, %v", updated, err)
	}
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "thread-a", 0)
	assertMemoryJob(t, runtime, MemoryJobKindStage1, "thread-a", "done", 3, 101, sql.NullInt64{Int64: 101, Valid: true})
	assertMemoryJob(t, runtime, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, "pending", 3, 101, sql.NullInt64{Int64: 0, Valid: true})
}

func TestMemoryStage1RetryExhaustionAndNewWatermarkReset(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)

	for attempt := int64(0); attempt < memoryDefaultRetryRemaining; attempt++ {
		claim, err := runtime.TryClaimStage1Job(ctx, "retry-thread", "worker", 10, 3600, 8)
		if err != nil || claim.Outcome != Stage1JobClaimed {
			t.Fatalf("attempt %d claim = %+v, %v", attempt, claim, err)
		}
		if updated, err := runtime.MarkStage1JobFailed(ctx, "retry-thread", claim.OwnershipToken, fmt.Sprintf("failure-%d", attempt), 3600); err != nil || !updated {
			t.Fatalf("attempt %d failure = %v, %v", attempt, updated, err)
		}
		blocked, err := runtime.TryClaimStage1Job(ctx, "retry-thread", "worker", 10, 3600, 8)
		want := Stage1JobSkippedRetryBackoff
		if attempt == memoryDefaultRetryRemaining-1 {
			want = Stage1JobSkippedRetryExhausted
		}
		if err != nil || blocked.Outcome != want {
			t.Fatalf("attempt %d blocked claim = %+v, %v; want %s", attempt, blocked, err, want)
		}
		if _, err := runtime.MemoriesDB().ExecContext(ctx, `UPDATE jobs SET retry_at = 0 WHERE kind = ? AND job_key = ?`, MemoryJobKindStage1, "retry-thread"); err != nil {
			t.Fatal(err)
		}
	}

	exhausted, err := runtime.TryClaimStage1Job(ctx, "retry-thread", "worker", 10, 3600, 8)
	if err != nil || exhausted.Outcome != Stage1JobSkippedRetryExhausted {
		t.Fatalf("exhausted claim = %+v, %v", exhausted, err)
	}
	reset, err := runtime.TryClaimStage1Job(ctx, "retry-thread", "worker", 11, 3600, 8)
	if err != nil || reset.Outcome != Stage1JobClaimed {
		t.Fatalf("new-watermark reset = %+v, %v", reset, err)
	}
	assertMemoryJob(t, runtime, MemoryJobKindStage1, "retry-thread", "running", 3, 11, sql.NullInt64{})
}

func TestMemoryStage1LeaseTakeoverAndGlobalRunningLimit(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)

	first, err := runtime.TryClaimStage1Job(ctx, "lease-thread", "worker-a", 1, 3600, 8)
	if err != nil || first.Outcome != Stage1JobClaimed {
		t.Fatalf("first lease = %+v, %v", first, err)
	}
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `UPDATE jobs SET lease_until = 0 WHERE kind = ? AND job_key = ?`, MemoryJobKindStage1, "lease-thread"); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.TryClaimStage1Job(ctx, "lease-thread", "worker-b", 1, 3600, 8)
	if err != nil || second.Outcome != Stage1JobClaimed || second.OwnershipToken == first.OwnershipToken {
		t.Fatalf("takeover lease = %+v, %v", second, err)
	}
	if updated, err := runtime.MarkStage1JobSucceeded(ctx, "lease-thread", first.OwnershipToken, 1, "stale", "stale", nil); err != nil || updated {
		t.Fatalf("stale owner success = %v, %v", updated, err)
	}
	if updated, err := runtime.MarkStage1JobSucceeded(ctx, "lease-thread", second.OwnershipToken, 1, "fresh", "fresh", nil); err != nil || !updated {
		t.Fatalf("new owner success = %v, %v", updated, err)
	}

	running, err := runtime.TryClaimStage1Job(ctx, "limit-a", "worker", 1, 3600, 1)
	if err != nil || running.Outcome != Stage1JobClaimed {
		t.Fatalf("running-limit first claim = %+v, %v", running, err)
	}
	limited, err := runtime.TryClaimStage1Job(ctx, "limit-b", "worker", 1, 3600, 1)
	if err != nil || limited.Outcome != Stage1JobSkippedRunning {
		t.Fatalf("running-limit second claim = %+v, %v", limited, err)
	}
	if updated, err := runtime.MarkStage1JobSucceededNoOutput(ctx, "limit-a", running.OwnershipToken); err != nil || !updated {
		t.Fatalf("finish running-limit owner = %v, %v", updated, err)
	}
	available, err := runtime.TryClaimStage1Job(ctx, "limit-b", "worker", 1, 3600, 1)
	if err != nil || available.Outcome != Stage1JobClaimed {
		t.Fatalf("running-limit released claim = %+v, %v", available, err)
	}
}

func TestMemoryGlobalPhase2Lifecycle(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)

	first, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker-a", 3600)
	if err != nil || first.Outcome != Phase2JobClaimed || first.InputWatermark != 0 {
		t.Fatalf("first phase2 claim = %+v, %v", first, err)
	}
	running, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker-b", 3600)
	if err != nil || running.Outcome != Phase2JobSkippedRunning {
		t.Fatalf("running phase2 claim = %+v, %v", running, err)
	}
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `UPDATE jobs SET lease_until = 0 WHERE kind = ? AND job_key = ?`, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey); err != nil {
		t.Fatal(err)
	}
	takeover, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker-b", 3600)
	if err != nil || takeover.Outcome != Phase2JobClaimed || takeover.OwnershipToken == first.OwnershipToken {
		t.Fatalf("phase2 takeover = %+v, %v", takeover, err)
	}
	if updated, err := runtime.MarkGlobalPhase2JobFailed(ctx, first.OwnershipToken, "stale", 1); err != nil || updated {
		t.Fatalf("stale phase2 failure = %v, %v", updated, err)
	}
	if updated, err := runtime.HeartbeatGlobalPhase2Job(ctx, takeover.OwnershipToken, 7200); err != nil || !updated {
		t.Fatalf("phase2 heartbeat = %v, %v", updated, err)
	}

	now := time.Now().UTC()
	insertMemoryThread(t, runtime, "selected-a", now.Add(-time.Hour), "enabled", "cli", false, "preview")
	insertMemoryThread(t, runtime, "selected-b", now.Add(-time.Hour), "enabled", "cli", false, "preview")
	insertMemoryOutput(t, runtime, "selected-a", 10, "raw-a", "summary-a", 0, nil, true)
	insertMemoryOutput(t, runtime, "selected-b", 11, "raw-b", "summary-b", 0, nil, false)
	selected := []Stage1Output{{ThreadID: "selected-b", SourceUpdatedAt: time.Unix(11, 0)}}
	if updated, err := runtime.MarkGlobalPhase2JobSucceeded(ctx, takeover.OwnershipToken, 12, selected); err != nil || !updated {
		t.Fatalf("phase2 success = %v, %v", updated, err)
	}
	assertMemorySelection(t, runtime, "selected-a", false, sql.NullInt64{})
	assertMemorySelection(t, runtime, "selected-b", true, sql.NullInt64{Int64: 11, Valid: true})
	cooldown, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker", 3600)
	if err != nil || cooldown.Outcome != Phase2JobSkippedCooldown {
		t.Fatalf("phase2 cooldown = %+v, %v", cooldown, err)
	}

	if err := runtime.EnqueueGlobalConsolidation(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err := runtime.EnqueueGlobalConsolidation(ctx, 10); err != nil {
		t.Fatal(err)
	}
	assertMemoryJob(t, runtime, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey, "pending", 3, 21, sql.NullInt64{Int64: 12, Valid: true})
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `UPDATE jobs SET finished_at = ?, retry_at = NULL WHERE kind = ? AND job_key = ?`, time.Now().Add(-7*time.Hour).Unix(), MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey); err != nil {
		t.Fatal(err)
	}
	retryClaim, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker", 3600)
	if err != nil || retryClaim.Outcome != Phase2JobClaimed || retryClaim.InputWatermark != 21 {
		t.Fatalf("phase2 post-cooldown claim = %+v, %v", retryClaim, err)
	}
	if updated, err := runtime.MarkGlobalPhase2JobFailed(ctx, retryClaim.OwnershipToken, "failure", 3600); err != nil || !updated {
		t.Fatalf("phase2 failure = %v, %v", updated, err)
	}
	backoff, err := runtime.TryClaimGlobalPhase2Job(ctx, "worker", 3600)
	if err != nil || backoff.Outcome != Phase2JobSkippedRetryUnavailable {
		t.Fatalf("phase2 backoff = %+v, %v", backoff, err)
	}
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `UPDATE jobs SET status = 'running', ownership_token = NULL, retry_at = NULL WHERE kind = ? AND job_key = ?`, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey); err != nil {
		t.Fatal(err)
	}
	if updated, err := runtime.MarkGlobalPhase2JobFailedIfUnowned(ctx, "unused-token", "orphaned", 1); err != nil || !updated {
		t.Fatalf("phase2 unowned failure = %v, %v", updated, err)
	}
}

func TestMemoryStartupClaimsApplyRustFilters(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	now := time.Now().UTC()
	tests := []struct {
		id       string
		updated  time.Time
		mode     string
		source   string
		archived bool
		preview  string
	}{
		{id: "eligible", updated: now.Add(-2 * time.Hour), mode: "enabled", source: "cli", preview: "eligible"},
		{id: "current", updated: now.Add(-2 * time.Hour), mode: "enabled", source: "cli", preview: "current"},
		{id: "disabled", updated: now.Add(-2 * time.Hour), mode: "disabled", source: "cli", preview: "disabled"},
		{id: "archived", updated: now.Add(-2 * time.Hour), mode: "enabled", source: "cli", archived: true, preview: "archived"},
		{id: "recent", updated: now.Add(-10 * time.Minute), mode: "enabled", source: "cli", preview: "recent"},
		{id: "old", updated: now.Add(-31 * 24 * time.Hour), mode: "enabled", source: "cli", preview: "old"},
		{id: "wrong-source", updated: now.Add(-2 * time.Hour), mode: "enabled", source: "mcp", preview: "source"},
		{id: "empty-preview", updated: now.Add(-2 * time.Hour), mode: "enabled", source: "cli"},
	}
	for _, test := range tests {
		insertMemoryThread(t, runtime, test.id, test.updated, test.mode, test.source, test.archived, test.preview)
	}
	claims, err := runtime.ClaimStage1JobsForStartup(ctx, "current", Stage1StartupClaimParams{
		ScanLimit:           100,
		MaxClaimed:          8,
		MaxAgeDays:          30,
		MinRolloutIdleHours: 1,
		AllowedSources:      []string{"cli"},
		LeaseSeconds:        3600,
		MaxRunningJobs:      8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Thread.ID != "eligible" || claims[0].OwnershipToken == "" {
		t.Fatalf("startup claims = %+v", claims)
	}

	if updated, err := runtime.MarkStage1JobSucceeded(ctx, "eligible", claims[0].OwnershipToken, claims[0].Thread.UpdatedAt.Unix(), "raw", "summary", nil); err != nil || !updated {
		t.Fatalf("complete startup claim = %v, %v", updated, err)
	}
	claims, err = runtime.ClaimStage1JobsForStartup(ctx, "current", Stage1StartupClaimParams{
		ScanLimit: 100, MaxClaimed: 8, MaxAgeDays: 30, MinRolloutIdleHours: 1,
		AllowedSources: []string{"cli"}, LeaseSeconds: 3600, MaxRunningJobs: 8,
	})
	if err != nil || len(claims) != 0 {
		t.Fatalf("up-to-date startup claims = %+v, %v", claims, err)
	}
}

func TestMemorySelectionUsageRetentionAndHydration(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	now := time.Now().UTC()
	for _, thread := range []struct {
		id   string
		mode string
	}{
		{id: "a", mode: "enabled"},
		{id: "b", mode: "enabled"},
		{id: "c", mode: "enabled"},
		{id: "d", mode: "disabled"},
		{id: "e", mode: "enabled"},
	} {
		insertMemoryThread(t, runtime, thread.id, now.Add(-2*time.Hour), thread.mode, "cli", false, thread.id+" preview")
	}
	old := now.Add(-30 * 24 * time.Hour).Unix()
	recent := now.Add(-time.Hour).Unix()
	insertMemoryOutput(t, runtime, "a", old, "raw-a", "summary-a", 1, nil, false)
	insertMemoryOutput(t, runtime, "b", old+1, "raw-b", "summary-b", 3, &recent, true)
	insertMemoryOutput(t, runtime, "c", recent, "raw-c", "summary-c", 3, &recent, false)
	insertMemoryOutput(t, runtime, "d", recent+1, "raw-d", "summary-d", 100, &recent, false)
	insertMemoryOutput(t, runtime, "e", old-1, "raw-e", "summary-e", 0, nil, false)

	selected, err := runtime.GetPhase2InputSelection(ctx, 2, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ThreadID != "b" || selected[1].ThreadID != "c" {
		t.Fatalf("phase2 selection = %+v", selected)
	}
	if selected[0].RolloutPath != filepath.Join("rollouts", "b.jsonl") || selected[0].CWD != "/workspace/b" || selected[0].GitBranch != "main" {
		t.Fatalf("hydrated selection = %+v", selected[0])
	}

	updated, err := runtime.RecordStage1OutputUsage(ctx, []string{"a", "a", "missing", ""})
	if err != nil || updated != 1 {
		t.Fatalf("usage update = %d, %v", updated, err)
	}
	var usage int64
	var lastUsage sql.NullInt64
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT usage_count, last_usage FROM stage1_outputs WHERE thread_id = 'a'`).Scan(&usage, &lastUsage); err != nil || usage != 2 || !lastUsage.Valid {
		t.Fatalf("usage row = %d/%v, %v", usage, lastUsage, err)
	}

	pruned, err := runtime.PruneStage1OutputsForRetention(ctx, 7, 1)
	if err != nil || pruned != 1 {
		t.Fatalf("first prune = %d, %v", pruned, err)
	}
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "e", 0)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "b", 1)
	pruned, err = runtime.PruneStage1OutputsForRetention(ctx, 7, 10)
	if err != nil || pruned != 0 {
		t.Fatalf("second prune = %d, %v", pruned, err)
	}
}

func TestMemoryPollutedModeAndClearData(t *testing.T) {
	ctx := context.Background()
	runtime := newBackfillTestRuntime(t)
	now := time.Now().UTC()
	insertMemoryThread(t, runtime, "selected", now, "enabled", "cli", false, "selected")
	insertMemoryThread(t, runtime, "unselected", now, "enabled", "cli", false, "unselected")
	insertMemoryOutput(t, runtime, "selected", 10, "raw", "summary", 0, nil, true)
	insertMemoryOutput(t, runtime, "unselected", 11, "raw", "summary", 0, nil, false)

	changed, err := runtime.MarkThreadMemoryModePolluted(ctx, "selected")
	if err != nil || !changed {
		t.Fatalf("mark selected polluted = %v, %v", changed, err)
	}
	var mode string
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT memory_mode FROM threads WHERE id = 'selected'`).Scan(&mode); err != nil || mode != "polluted" {
		t.Fatalf("selected memory mode = %q, %v", mode, err)
	}
	var consolidationWatermark int64
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT input_watermark FROM jobs WHERE kind = ? AND job_key = ?`, MemoryJobKindConsolidateGlobal, MemoryConsolidationJobKey).Scan(&consolidationWatermark); err != nil || consolidationWatermark <= 0 {
		t.Fatalf("polluted consolidation watermark = %d, %v", consolidationWatermark, err)
	}

	if _, err := runtime.MemoriesDB().ExecContext(ctx, `INSERT INTO jobs (kind, job_key, status, retry_remaining) VALUES ('unrelated', 'keep', 'pending', 9)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.TryClaimStage1Job(ctx, "memory-job", "worker", 1, 3600, 8); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ClearMemoryData(ctx); err != nil {
		t.Fatal(err)
	}
	assertMemoryTableCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "selected", 0)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "jobs", "kind", MemoryJobKindStage1, 0)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "jobs", "kind", MemoryJobKindConsolidateGlobal, 0)
	assertMemoryTableCount(t, runtime.MemoriesDB(), "jobs", "kind", "unrelated", 1)
}

func insertMemoryThread(t *testing.T, runtime *StateRuntime, id string, updated time.Time, mode, source string, archived bool, preview string) {
	t.Helper()
	updated = updated.UTC()
	_, err := runtime.StateDB().Exec(`
INSERT INTO threads (
    id, rollout_path, created_at, updated_at, source, model_provider, cwd, title,
    sandbox_policy, approval_mode, has_user_event, archived, git_branch,
    memory_mode, created_at_ms, updated_at_ms, preview
) VALUES (?, ?, ?, ?, ?, 'openai', ?, ?, 'read-only', 'on-request', 1, ?, 'main', ?, ?, ?, ?)`,
		id, filepath.Join("rollouts", id+".jsonl"), updated.Unix(), updated.Unix(), source,
		filepath.ToSlash(filepath.Join("/workspace", id)), preview, archived, mode,
		updated.UnixMilli(), updated.UnixMilli(), preview,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func insertMemoryOutput(t *testing.T, runtime *StateRuntime, threadID string, sourceUpdatedAt int64, raw, summary string, usage int64, lastUsage *int64, selected bool) {
	t.Helper()
	var last any
	if lastUsage != nil {
		last = *lastUsage
	}
	selectedValue := 0
	var selectedWatermark any
	if selected {
		selectedValue = 1
		selectedWatermark = sourceUpdatedAt
	}
	_, err := runtime.MemoriesDB().Exec(`
INSERT INTO stage1_outputs (
    thread_id, source_updated_at, raw_memory, rollout_summary, generated_at,
    usage_count, last_usage, selected_for_phase2, selected_for_phase2_source_updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		threadID, sourceUpdatedAt, raw, summary, sourceUpdatedAt, usage, last, selectedValue, selectedWatermark,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func assertMemoryStage1Output(t *testing.T, runtime *StateRuntime, threadID string, sourceUpdatedAt int64, raw, summary string, slug sql.NullString) {
	t.Helper()
	var gotSource int64
	var gotRaw, gotSummary string
	var gotSlug sql.NullString
	if err := runtime.MemoriesDB().QueryRow(`SELECT source_updated_at, raw_memory, rollout_summary, rollout_slug FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&gotSource, &gotRaw, &gotSummary, &gotSlug); err != nil {
		t.Fatal(err)
	}
	if gotSource != sourceUpdatedAt || gotRaw != raw || gotSummary != summary || gotSlug != slug {
		t.Fatalf("stage1 output = source:%d raw:%q summary:%q slug:%v", gotSource, gotRaw, gotSummary, gotSlug)
	}
}

func assertMemoryJob(t *testing.T, runtime *StateRuntime, kind, key, status string, retries, inputWatermark int64, successWatermark sql.NullInt64) {
	t.Helper()
	var gotStatus string
	var gotRetries int64
	var gotInput, gotSuccess sql.NullInt64
	if err := runtime.MemoriesDB().QueryRow(`SELECT status, retry_remaining, input_watermark, last_success_watermark FROM jobs WHERE kind = ? AND job_key = ?`, kind, key).Scan(&gotStatus, &gotRetries, &gotInput, &gotSuccess); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status || gotRetries != retries || !gotInput.Valid || gotInput.Int64 != inputWatermark || gotSuccess != successWatermark {
		t.Fatalf("memory job %s/%s = status:%q retries:%d input:%v success:%v", kind, key, gotStatus, gotRetries, gotInput, gotSuccess)
	}
}

func assertMemorySelection(t *testing.T, runtime *StateRuntime, threadID string, selected bool, sourceUpdatedAt sql.NullInt64) {
	t.Helper()
	var gotSelected bool
	var gotSource sql.NullInt64
	if err := runtime.MemoriesDB().QueryRow(`SELECT selected_for_phase2, selected_for_phase2_source_updated_at FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&gotSelected, &gotSource); err != nil {
		t.Fatal(err)
	}
	if gotSelected != selected || gotSource != sourceUpdatedAt {
		t.Fatalf("selection for %s = %v/%v", threadID, gotSelected, gotSource)
	}
}

func assertMemoryTableCount(t *testing.T, db *sql.DB, table, column, value string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, value).Scan(&got); err != nil || got != want {
		t.Fatalf("%s count for %s=%q = %d, %v; want %d", table, column, value, got, err, want)
	}
}
