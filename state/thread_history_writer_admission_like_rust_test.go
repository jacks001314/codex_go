package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"codex_go/rollout"
)

// writeAdmissionRollout records one paginated rollout with the requested number
// of turns so the projection has real lines to apply.
func writeAdmissionRollout(t testing.TB, home string, threadID string, now time.Time, turns int) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: threadID, ThreadID: threadID,
		HistoryMode: "paginated", ModelProvider: "openai", CWD: home, Now: now,
	})
	if err != nil {
		t.Fatalf("new recorder for %s: %v", threadID, err)
	}
	for i := 0; i < turns; i++ {
		if err := recorder.AppendTurnStarted(fmt.Sprintf("turn-%d", i), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("append turn %d for %s: %v", i, threadID, err)
		}
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("close recorder for %s: %v", threadID, err)
	}
	return path
}

// TestHistoryWriteAdmission pins Rust #60006(b): thread-history projection takes
// the runtime's single writer permit before it opens its IMMEDIATE write
// transaction, so a caller arriving while another projection holds the permit
// waits for it (and honours cancellation) instead of stacking behind SQLite's
// write lock.
func TestHistoryWriteAdmission(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatalf("ThreadHistoryDB() error = %v", err)
	}
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	threadID := "thread-admission"
	path := writeAdmissionRollout(t, home, threadID, now, 2)

	// Holding the permit keeps the next projection out of the write transaction.
	runtime.historyWriter <- struct{}{}
	blocked, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := runtime.MaterializeThreadHistory(blocked, db, threadID, path, 0, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("MaterializeThreadHistory() while the permit is held = %v, want the context deadline", err)
	}
	if state, loadErr := LoadThreadHistoryProjectionState(ctx, db, threadID); loadErr != nil || state != nil {
		t.Fatalf("projection state after a refused admission = %+v, %v; want nothing written", state, loadErr)
	}
	<-runtime.historyWriter

	// With the permit free the same call projects and advances the checkpoint.
	if err := runtime.MaterializeThreadHistory(ctx, db, threadID, path, 0, nil); err != nil {
		t.Fatalf("MaterializeThreadHistory() error = %v", err)
	}
	state, err := LoadThreadHistoryProjectionState(ctx, db, threadID)
	if err != nil || state == nil || state.NextRolloutByteOffset == 0 {
		t.Fatalf("projection state after admission = %+v, %v", state, err)
	}

	// Concurrent writers queue on the permit instead of failing: every thread
	// projects its own rollout.
	const writers = 4
	ids := make([]string, writers)
	paths := make([]string, writers)
	for i := range ids {
		ids[i] = fmt.Sprintf("thread-admission-%d", i)
		paths[i] = writeAdmissionRollout(t, home, ids[i], now, 1)
	}
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = runtime.MaterializeThreadHistory(ctx, db, ids[i], paths[i], 0, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent MaterializeThreadHistory(%s) error = %v", ids[i], err)
		}
	}
	var projected int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_history_projection_state`).Scan(&projected); err != nil {
		t.Fatalf("count projection rows: %v", err)
	}
	if projected != writers+1 {
		t.Fatalf("projected threads = %d, want %d", projected, writers+1)
	}
}

// BenchmarkHistoryWriteAdmission reports the reader latency distribution while
// several thread-history writers run, with and without the single writer permit
// (Rust #60006(b)). The permit is a read-tail change: writer throughput is not
// the claim, and starvation was never reproduced (no reader above 100 ms).
func BenchmarkHistoryWriteAdmission(b *testing.B) {
	for _, admitted := range []bool{true, false} {
		name := "Unadmitted"
		if admitted {
			name = "Admitted"
		}
		b.Run(name, func(b *testing.B) {
			benchmarkHistoryWriteAdmission(b, admitted)
		})
	}
}

func benchmarkHistoryWriteAdmission(b *testing.B, admitted bool) {
	ctx := context.Background()
	home := b.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		b.Fatalf("NewSqliteConfig() error = %v", err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		b.Fatalf("InitStateRuntime() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		b.Fatalf("ThreadHistoryDB() error = %v", err)
	}
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

	const writers = 4
	ids := make([]string, writers)
	paths := make([]string, writers)
	recorders := make([]*rollout.Recorder, writers)
	for i := range ids {
		ids[i] = fmt.Sprintf("bench-admission-%d", i)
		recorders[i], err = rollout.NewRecorder(&rollout.CreateParams{
			CodexHome: home, SessionID: ids[i], ThreadID: ids[i],
			HistoryMode: "paginated", ModelProvider: "openai", CWD: home, Now: now,
		})
		if err != nil {
			b.Fatalf("new recorder: %v", err)
		}
		if err := recorders[i].AppendTurnStarted("turn-0", now); err != nil {
			b.Fatalf("append turn: %v", err)
		}
		paths[i] = recorders[i].Path()
		if err := runtime.MaterializeThreadHistory(ctx, db, ids[i], paths[i], 0, nil); err != nil {
			b.Fatalf("seed projection: %v", err)
		}
	}
	defer func() {
		for _, recorder := range recorders {
			_ = recorder.Close()
		}
	}()

	var mu sync.Mutex
	latencies := make([]time.Duration, 0, 4096)
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			started := time.Now()
			var count int
			_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_items`).Scan(&count)
			elapsed := time.Since(started)
			mu.Lock()
			if len(latencies) < cap(latencies) {
				latencies = append(latencies, elapsed)
			}
			mu.Unlock()
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < b.N; j++ {
				if err := recorders[i].AppendTurnStarted(fmt.Sprintf("turn-%d", j+1), now.Add(time.Duration(j+1)*time.Second)); err != nil {
					return
				}
				var err error
				if admitted {
					err = runtime.MaterializeThreadHistory(ctx, db, ids[i], paths[i], 0, nil)
				} else {
					err = MaterializeThreadHistory(ctx, db, ids[i], paths[i], 0, nil)
				}
				if err != nil {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	readerWG.Wait()

	mu.Lock()
	samples := append([]time.Duration(nil), latencies...)
	mu.Unlock()
	if len(samples) == 0 {
		b.Skip("no reader samples collected")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	percentile := func(p float64) time.Duration {
		index := int(p * float64(len(samples)-1))
		return samples[index]
	}
	b.ReportMetric(float64(percentile(0.5).Microseconds()), "read-p50_us")
	b.ReportMetric(float64(percentile(0.8).Microseconds()), "read-p80_us")
	b.ReportMetric(float64(percentile(0.99).Microseconds()), "read-p99_us")
}
