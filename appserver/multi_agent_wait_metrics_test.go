package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/state"
	"codex_go/telemetry"
)

func multiAgentWaitDurationRecords(metrics *state.TaskMetrics) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == telemetry.MultiAgentWaitDurationMetric {
			out = append(out, record)
		}
	}
	return out
}

// Rust #51332 (c2ae67d769, codex-rs/core/src/tools/handlers/multi_agents_v2/wait.rs):
// a completed wait_agent records `codex.multi_agent.wait.duration_ms` tagged
// with the outcome the wait observed (mailbox | steered | timed_out), and a
// dropped wait is skipped because it observed no outcome. Rust has no dedicated
// test for the series; the WaitOutcome arms are what the handler maps.
func TestMultiAgentWaitDurationRecordsOutcomeLikeRust(t *testing.T) {
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{TurnMetrics: metrics})
	controller := &runtimeAgentController{router: router, rootID: "thread-1"}

	// Mailbox activity: the wait wakes because an agent activity was delivered.
	// The activity is delivered only after the wait has started, so the recorded
	// duration is real elapsed wait time: delivering first would let the mailbox
	// branch return within the host clock resolution and record 0ms.
	type waitResult struct {
		activity *agent.WaitForActivityResult
		err      error
	}
	mailboxResults := make(chan waitResult, 1)
	go func() {
		activity, err := controller.WaitForActivity(context.Background(), nil)
		mailboxResults <- waitResult{activity: activity, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	router.notifyRuntimeAgentActivity("thread-1", "child finished")
	mailbox := <-mailboxResults
	if mailbox.err != nil {
		t.Fatalf("WaitForActivity(mailbox): %v", mailbox.err)
	}
	activity := mailbox.activity
	if activity == nil || activity.TimedOut || activity.Message != "child finished" {
		t.Fatalf("mailbox activity = %#v, want the notified message", activity)
	}

	// Timeout: the wait wakes on its own deadline.
	timeoutMS := int64(1)
	timedOut, err := controller.WaitForActivity(context.Background(), &agent.WaitForActivityArgs{TimeoutMS: &timeoutMS})
	if err != nil {
		t.Fatalf("WaitForActivity(timeout): %v", err)
	}
	if timedOut == nil || !timedOut.TimedOut {
		t.Fatalf("timed-out result = %#v, want TimedOut", timedOut)
	}

	records := multiAgentWaitDurationRecords(metrics)
	if len(records) != 2 {
		t.Fatalf("wait duration records = %d, want 2", len(records))
	}
	if records[0].Kind != "duration" || records[0].Tags["outcome"] != telemetry.MultiAgentWaitOutcomeMailbox {
		t.Fatalf("mailbox record = %#v, want a duration tagged outcome=mailbox", records[0])
	}
	if records[1].Kind != "duration" || records[1].Tags["outcome"] != telemetry.MultiAgentWaitOutcomeTimedOut {
		t.Fatalf("timeout record = %#v, want a duration tagged outcome=timed_out", records[1])
	}
	for _, record := range records {
		if record.Tags["outcome"] == telemetry.MultiAgentWaitOutcomeSteered {
			t.Fatalf("Go observed the steered outcome, which its mailbox cannot report: %#v", record)
		}
		if record.DurationMS <= 0 {
			t.Fatalf("record %#v has a non-positive duration", record)
		}
	}

	// A dropped (cancelled) wait observed no outcome and must not be recorded.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := controller.WaitForActivity(cancelled, &agent.WaitForActivityArgs{TimeoutMS: &timeoutMS}); err == nil {
		t.Fatalf("cancelled WaitForActivity returned no error")
	}
	if got := len(multiAgentWaitDurationRecords(metrics)); got != 2 {
		t.Fatalf("wait duration records after a dropped wait = %d, want 2", got)
	}
}
