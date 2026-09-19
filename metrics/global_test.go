package metrics

import (
	"sync"
	"testing"
	"time"
)

type recordingRecorder struct {
	mu        sync.Mutex
	name      string
	duration  time.Duration
	tags      map[string]string
	callCount int
}

func (r *recordingRecorder) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callCount++
	r.name = name
	r.duration = duration
	r.tags = tags
}

// Mirrors Rust's global metrics install: a timer records through the installed
// recorder and is a no-op when no recorder is installed.
func TestGlobalRecorderInstallAndStartTimerLikeRust(t *testing.T) {
	InstallGlobal(nil)
	t.Cleanup(func() { InstallGlobal(nil) })

	if Global() != nil {
		t.Fatal("Global() = non-nil after clearing")
	}
	if timer := StartTimer("codex.remote_models.fetch_update.duration_ms", nil); timer != nil {
		t.Fatal("StartTimer returned a timer without an installed recorder")
	}
	// Stop on the nil timer must be safe (callers defer Stop unconditionally).
	var nilTimer *Timer
	nilTimer.Stop()

	recorder := &recordingRecorder{}
	if installed := InstallGlobal(recorder); installed != recorder {
		t.Fatalf("InstallGlobal returned %#v", installed)
	}
	if Global() != recorder {
		t.Fatal("Global() did not return the installed recorder")
	}
	timer := StartTimer("codex.remote_models.fetch_update.duration_ms", map[string]string{"auth_mode": "api_key"})
	if timer == nil {
		t.Fatal("StartTimer returned nil with an installed recorder")
	}
	time.Sleep(time.Millisecond)
	timer.Stop()
	if recorder.callCount != 1 {
		t.Fatalf("recorder calls = %d", recorder.callCount)
	}
	if recorder.name != "codex.remote_models.fetch_update.duration_ms" || recorder.tags["auth_mode"] != "api_key" {
		t.Fatalf("recorded %q %#v", recorder.name, recorder.tags)
	}
	if recorder.duration < time.Millisecond {
		t.Fatalf("recorded duration = %v", recorder.duration)
	}

	// The timer captures its tags at creation, so later mutation does not leak.
	tags := map[string]string{"auth_mode": "chatgpt"}
	second := StartTimer("codex.remote_models.fetch_update.duration_ms", tags)
	tags["auth_mode"] = "mutated"
	second.Stop()
	if recorder.tags["auth_mode"] != "chatgpt" {
		t.Fatalf("timer tags were not cloned: %#v", recorder.tags)
	}
}
