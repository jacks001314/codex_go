package metrics

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type bufferedTestRecorder struct {
	mu        sync.Mutex
	counters  []string
	durations []string
	tags      []map[string]string
}

func (r *bufferedTestRecorder) Counter(name string, inc int, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, name)
	r.tags = append(r.tags, tags)
}

func (r *bufferedTestRecorder) Histogram(name string, value int, tags map[string]string) {}

func (r *bufferedTestRecorder) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations = append(r.durations, name)
}

func resetGlobalOperationBufferForTest(t *testing.T) {
	t.Helper()
	globalOperationBuffer.mu.Lock()
	globalOperationBuffer.state = globalOperationStartup
	globalOperationBuffer.pending = nil
	globalOperationBuffer.recorder = nil
	globalOperationBuffer.mu.Unlock()
	t.Cleanup(func() { InstallGlobal(nil) })
}

func TestGlobalOperationBufferLimitsAndValidationLikeRust(t *testing.T) {
	resetGlobalOperationBufferForTest(t)

	oversized := make(map[string]string, MaxGlobalOperationTags+1)
	for index := 0; index <= MaxGlobalOperationTags; index++ {
		oversized["tag"+string(rune('a'+index))] = "value"
	}
	if err := RecordGlobalOperation("count", "duration", time.Millisecond, oversized); !errors.Is(err, ErrGlobalOperationMetadataTooLarge) {
		t.Fatalf("oversized tags error = %v", err)
	}
	if err := RecordGlobalOperation("count", "duration", time.Millisecond, map[string]string{"tag": string(make([]byte, MaxGlobalOperationMetadataBytes))}); !errors.Is(err, ErrGlobalOperationMetadataTooLarge) {
		t.Fatalf("oversized metadata error = %v", err)
	}
	for _, name := range []string{"", "1duration", "invalid duration", string(make([]byte, 256))} {
		if err := RecordGlobalOperation("count", name, time.Millisecond, nil); err == nil {
			t.Fatalf("RecordGlobalOperation accepted duration name %q", name)
		}
		if err := RecordGlobalOperation(name, "duration", time.Millisecond, nil); err == nil {
			t.Fatalf("RecordGlobalOperation accepted count name %q", name)
		}
	}
	if err := RecordGlobalOperation("count", "duration", time.Millisecond, map[string]string{"tag": "invalid value"}); !errors.Is(err, ErrInvalidGlobalMetricTag) {
		t.Fatalf("invalid tag error = %v", err)
	}

	// A previous disable leaves recording disabled, not buffering.
	globalOperationBuffer.disable()
	for index := 0; index < MaxPendingGlobalOperations+1; index++ {
		if err := RecordGlobalOperation("count", "duration", 7*time.Millisecond, map[string]string{"component": "test"}); err != nil {
			t.Fatalf("record %d error = %v", index, err)
		}
	}
	recorder := &bufferedTestRecorder{}
	InstallGlobal(recorder)
	if len(recorder.counters) != 0 || len(recorder.durations) != 0 {
		t.Fatalf("disabled buffer drained %d counters and %d durations", len(recorder.counters), len(recorder.durations))
	}
}

func TestGlobalOperationBufferDrainsPairsAndResumesOnSuspendLikeRust(t *testing.T) {
	resetGlobalOperationBufferForTest(t)

	if err := RecordGlobalOperation("count", "duration", 7*time.Millisecond, map[string]string{"component": "test"}); err != nil {
		t.Fatalf("RecordGlobalOperation error = %v", err)
	}
	first := &bufferedTestRecorder{}
	InstallGlobal(first)
	if len(first.counters) != 1 || len(first.durations) != 1 || first.tags[0]["component"] != "test" {
		t.Fatalf("first recorder = %#v", first)
	}

	second := &bufferedTestRecorder{}
	InstallGlobal(second)
	SuspendGlobal(first)
	if err := RecordGlobalOperation("count", "duration", 8*time.Millisecond, nil); err != nil {
		t.Fatalf("RecordGlobalOperation error = %v", err)
	}
	if len(second.counters) != 1 || len(second.durations) != 1 {
		t.Fatalf("second recorder = %#v", second)
	}

	SuspendGlobal(second)
	if err := RecordGlobalOperation("count", "duration", 9*time.Millisecond, nil); err != nil {
		t.Fatalf("RecordGlobalOperation error = %v", err)
	}
	third := &bufferedTestRecorder{}
	InstallGlobal(third)
	if len(third.counters) != 1 || len(third.durations) != 1 {
		t.Fatalf("third recorder = %#v", third)
	}
}

func TestGlobalOperationBufferOverflowDropsWholePairsLikeRust(t *testing.T) {
	resetGlobalOperationBufferForTest(t)

	for index := 0; index < MaxPendingGlobalOperations+1; index++ {
		if err := RecordGlobalOperation("count", "duration", 7*time.Millisecond, nil); err != nil {
			t.Fatalf("record %d error = %v", index, err)
		}
	}
	recorder := &bufferedTestRecorder{}
	InstallGlobal(recorder)
	if len(recorder.counters) != MaxPendingGlobalOperations || len(recorder.durations) != MaxPendingGlobalOperations {
		t.Fatalf("drained counters=%d durations=%d, want %d", len(recorder.counters), len(recorder.durations), MaxPendingGlobalOperations)
	}
}
