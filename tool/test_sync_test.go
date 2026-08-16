package tool

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTestSyncHandlerSleepsAndReturnsOK(t *testing.T) {
	handler := &TestSyncHandler{}
	start := time.Now()
	out, err := handler.Execute(context.Background(), &Invocation{
		Payload: Payload{Arguments: `{"sleep_before_ms": 20}`},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out == nil || !out.Success || out.Body != "ok" {
		t.Fatalf("output = %#v", out)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("sleep_before_ms not honored: %v", elapsed)
	}
}

func TestTestSyncHandlerBarrierRendezvous(t *testing.T) {
	// Mirrors Rust test_sync_tool barrier: concurrent calls with the same id
	// and participants count rendezvous and all return "ok".
	handler := &TestSyncHandler{}
	const participants = 3
	var wg sync.WaitGroup
	errs := make([]error, participants)
	wg.Add(participants)
	for i := 0; i < participants; i++ {
		go func(index int) {
			defer wg.Done()
			args := `{"barrier":{"id":"rendezvous-1","participants":3,"timeout_ms":2000}}`
			_, errs[index] = handler.Execute(context.Background(), &Invocation{Payload: Payload{Arguments: args}})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("participant %d: %v", i, err)
		}
	}
}

func TestTestSyncHandlerBarrierValidation(t *testing.T) {
	handler := &TestSyncHandler{}
	if _, err := handler.Execute(context.Background(), &Invocation{Payload: Payload{Arguments: `{"barrier":{"id":"b","participants":0}}`}}); err == nil {
		t.Fatal("zero participants must be rejected")
	}
	// A barrier with a different participant count for the same id is rejected.
	first := &TestSyncHandler{}
	_, _ = first.Execute(context.Background(), &Invocation{Payload: Payload{Arguments: `{"barrier":{"id":"conflict","participants":2,"timeout_ms":1000}}`}})
	if _, err := first.Execute(context.Background(), &Invocation{Payload: Payload{Arguments: `{"barrier":{"id":"conflict","participants":3,"timeout_ms":1000}}`}}); err == nil {
		t.Fatal("conflicting participants must be rejected")
	}
}

func TestTestSyncHandlerSpecMirrorsRust(t *testing.T) {
	handler := &TestSyncHandler{}
	spec := handler.Spec()
	if spec.Name.Key() != "test_sync_tool" {
		t.Fatalf("name = %q", spec.Name.Key())
	}
	if !spec.Parallel {
		t.Fatal("test_sync_tool must support parallel calls")
	}
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input schema = %#v", spec.InputSchema)
	}
	for _, key := range []string{"sleep_before_ms", "sleep_after_ms", "barrier"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("missing property %q in %#v", key, properties)
		}
	}
}
