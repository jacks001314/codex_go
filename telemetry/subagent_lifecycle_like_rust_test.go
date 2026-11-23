package telemetry

import (
	"context"
	"testing"
)

// Mirrors Rust #62567: each lifecycle boundary emits the named counter with
// bounded labels, while the correlation ids stay out of metric labels.
func TestSubagentLifecycleCountersLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	ctx := context.Background()
	EmitMultiAgentSpawned(ctx, nil, sink, MultiAgentSpawned{
		RootThreadID: "root", ParentTurnID: "parent-turn", ChildThreadID: "child",
	})
	EmitMultiAgentTurnLifecycle(ctx, nil, sink, MultiAgentTurnLifecycle{
		ChildThreadID: "child", TurnID: "turn-1", TurnKind: SubagentTurnKindInitial, Status: SubagentTurnStatusStarted,
	})
	EmitMultiAgentTurnLifecycle(ctx, nil, sink, MultiAgentTurnLifecycle{
		ChildThreadID: "child", TurnID: "turn-1", TurnKind: SubagentTurnKindInitial, Status: SubagentTurnStatusInterrupted,
		AbortReason: "unknown",
	})
	EmitMultiAgentStopAccepted(ctx, nil, sink, MultiAgentStopAccepted{
		RootThreadID: "root", ChildThreadID: "child", CallerThreadID: "root",
		Caller: SubagentStopCallerRoot, Action: SubagentStopActionClose,
	})
	if len(sink.counters) != 4 {
		t.Fatalf("counters = %#v, want one per emitted boundary", sink.counters)
	}
	names := []string{MultiAgentSpawnedMetric, MultiAgentTurnLifecycleMetric, MultiAgentTurnLifecycleMetric, MultiAgentStopAcceptedMetric}
	for index, name := range names {
		if sink.counters[index].name != name || sink.counters[index].value != 1 {
			t.Fatalf("counter %d = %#v, want %s incremented once", index, sink.counters[index], name)
		}
	}
	if tags := sink.counters[0].tags; len(tags) != 0 {
		t.Fatalf("spawned labels = %#v, want only bounded default tags", tags)
	}
	started := sink.counters[1].tags
	if started["turn_kind"] != SubagentTurnKindInitial || started["status"] != SubagentTurnStatusStarted {
		t.Fatalf("turn labels = %#v", started)
	}
	interrupted := sink.counters[2].tags
	if interrupted["status"] != SubagentTurnStatusInterrupted || interrupted["abort_reason"] != "unknown" {
		t.Fatalf("interrupted labels = %#v", interrupted)
	}
	stop := sink.counters[3].tags
	if stop["caller"] != SubagentStopCallerRoot || stop["action"] != SubagentStopActionClose || stop["previous_status"] != SubagentStopPreviousStatusUnknown {
		t.Fatalf("stop labels = %#v", stop)
	}
	for _, counter := range sink.counters {
		for _, forbidden := range []string{"root_thread_id", "child_thread_id", "turn_id", "call_id", "caller_thread_id"} {
			if _, present := counter.tags[forbidden]; present {
				t.Fatalf("counter %s leaked correlation label %q: %#v", counter.name, forbidden, counter.tags)
			}
		}
	}
}
