package appserver

import (
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/turn"
)

func startupMetricRecords(metrics *state.TaskMetrics, name string) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == name {
			out = append(out, record)
		}
	}
	return out
}

// Mirrors the app-server's thread_start_create_thread / thread_start_total
// startup phases (Rust thread_processor.rs): both are durations tagged with the
// phase and the "ready" status.
func TestThreadStartRecordsStartupPhasesLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
	})
	defer router.Close()

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if response.Error != nil {
		t.Fatalf("thread start error: %+v", response.Error)
	}
	phases := map[string]*state.TaskMetric{}
	for _, record := range startupMetricRecords(metrics, telemetry.StartupPhaseDurationMetric) {
		if record.Kind != "duration" || record.Tags["status"] != "ready" || record.DurationMS < 0 {
			t.Fatalf("startup phase record = %#v", record)
		}
		phases[record.Tags["phase"]] = record
	}
	for _, phase := range []string{"thread_start_create_thread", "thread_start_total"} {
		if phases[phase] == nil {
			t.Fatalf("phase %s missing: %#v", phase, phases)
		}
	}
}

// Mirrors the prewarm task's phase/duration samples and the resolve path's
// age-at-first-turn sample (Rust session_startup_prewarm.rs): the prewarm
// reports status ready at completion, and the first turn consumes it.
func TestStartupPrewarmRecordsMetricsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	metrics := state.NewTaskMetrics()
	agent := &prewarmingRuntimeAgent{recordingRuntimeAgent: newRecordingRuntimeAgent("ok"), prewarmedID: "prewarm-resp-metrics"}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Model: "gpt-test"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	deadline := time.Now().Add(5 * time.Second)
	for {
		if state := router.startupPrewarmsSnapshot()[threadID]; state != nil && state.finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup prewarm did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	if request := waitForRuntimeAgentRequest(t, agent.recordingRuntimeAgent); request.PreviousResponseID != "prewarm-resp-metrics" {
		t.Fatalf("first turn PreviousResponseID = %q", request.PreviousResponseID)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	prewarmPhases := startupMetricRecords(metrics, telemetry.StartupPhaseDurationMetric)
	foundPrewarmPhase := false
	for _, record := range prewarmPhases {
		if record.Tags["phase"] == "startup_prewarm_total" {
			foundPrewarmPhase = true
			if record.Tags["status"] != "ready" || record.DurationMS < 0 {
				t.Fatalf("prewarm phase record = %#v", record)
			}
		}
	}
	if !foundPrewarmPhase {
		t.Fatalf("startup_prewarm_total missing: %#v", prewarmPhases)
	}
	durations := startupMetricRecords(metrics, telemetry.StartupPrewarmDurationMetric)
	if len(durations) != 1 || durations[0].Tags["status"] != "ready" {
		t.Fatalf("prewarm durations = %#v", durations)
	}
	ages := startupMetricRecords(metrics, telemetry.StartupPrewarmAgeAtFirstTurnMetric)
	if len(ages) != 1 || ages[0].Tags["status"] != "consumed" {
		t.Fatalf("prewarm ages = %#v", ages)
	}
}
