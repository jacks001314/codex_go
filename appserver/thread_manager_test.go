package appserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

func TestThreadManagerClaimTerminalIsAtomic(t *testing.T) {
	manager := NewThreadManager(nil)
	var claimed atomic.Int32
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if manager.ClaimTerminal("thread-1", "turn-1") {
				claimed.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("terminal claims = %d, want 1", got)
	}
	if !manager.ClaimTerminal("thread-1", "turn-2") {
		t.Fatal("different turn terminal was incorrectly suppressed")
	}
}

func TestThreadManagerShutdownCancelsAndWaitsForTrackedTurns(t *testing.T) {
	manager := NewThreadManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.RegisterTrackedTurn("thread-1", "turn-1", cancel, 1, &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("RegisterTrackedTurn() error = %v", err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		defer manager.TurnWorkerDone()
		<-ctx.Done()
	}()
	active := manager.BeginShutdown()
	if len(active) != 1 || active[0].TurnID != "turn-1" {
		t.Fatalf("active turns = %#v", active)
	}
	active[0].Cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := manager.WaitForTurnWorkers(waitCtx); err != nil {
		t.Fatalf("WaitForTurnWorkers() error = %v", err)
	}
	<-workerDone
	if err := manager.ReserveTurn("thread-2"); err == nil {
		t.Fatal("ReserveTurn() succeeded after shutdown")
	}
}

func TestThreadManagerEphemeralRecordConcurrentAppendReturnsSnapshot(t *testing.T) {
	manager := NewThreadManager(nil)
	if !manager.SaveEphemeralRecord(&session.Record{
		ID:       "thread-ephemeral",
		Metadata: session.Metadata{Extra: map[string]any{"ephemeral": true}},
	}) {
		t.Fatal("SaveEphemeralRecord() = false")
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for i := range 250 {
			if _, ok := manager.AppendEphemeralItems("thread-ephemeral", []session.Item{{ID: fmt.Sprintf("item-%d", i), Text: "payload"}}); !ok {
				t.Errorf("AppendEphemeralItems() iteration %d = false", i)
				return
			}
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for range 250 {
			record, ok := manager.EphemeralRecord("thread-ephemeral", true)
			if !ok || record == nil {
				t.Error("EphemeralRecord() did not return a snapshot")
				return
			}
		}
	}()
	close(start)
	wait.Wait()

	record, ok := manager.EphemeralRecord("thread-ephemeral", true)
	if !ok || len(record.Items) != 250 {
		t.Fatalf("final item count = %d, ok = %v, want 250, true", len(record.Items), ok)
	}
}

func TestRuntimeRouterCloseInterruptsOnceAndClearsManagedState(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	if err := router.registerActiveRuntimeTurn("thread-1", "turn-1", func() {
		cancelOnce.Do(func() { close(cancelled) })
	}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	router.requireThreadStatus().NoteTurnStarted("thread-1")
	router.threads.Subscribe("thread-1", "conn-1")
	router.ephemeralThreads["thread-1"] = &session.Record{
		ID:       "thread-1",
		Metadata: session.Metadata{Extra: map[string]any{"ephemeral": true}},
	}
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := router.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}()
	}
	wait.Wait()
	select {
	case <-cancelled:
	default:
		t.Fatal("active turn was not cancelled")
	}
	completed := 0
	for _, notification := range sink.List() {
		if notification.Method == NotificationTurnCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("turn/completed notifications = %d, want 1", completed)
	}
	if active := router.threads.ActiveTurns(); len(active) != 0 {
		t.Fatalf("active turns after close = %#v", active)
	}
	if subscribers := router.threads.Subscribers("thread-1"); len(subscribers) != 0 {
		t.Fatalf("subscribers after close = %#v", subscribers)
	}
	if _, ok := router.ephemeralThreadRecord("thread-1", true); ok {
		t.Fatal("ephemeral record remained after close")
	}
	if status := router.requireThreadStatus().LoadedStatusForThread("thread-1"); status.Type != NotLoadedStatus().Type {
		t.Fatalf("status after close = %#v", status)
	}
}

func TestRuntimeRouterNotifyTurnCompletedOnce(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	notification := &TurnCompletedNotification{
		ThreadID: "thread-1",
		Turn:     Turn{ID: "turn-1", Status: TurnStatusCompleted},
	}
	var sent atomic.Int32
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if router.notifyTurnCompletedOnce(notification) {
				sent.Add(1)
			}
		}()
	}
	wait.Wait()
	if got := sent.Load(); got != 1 {
		t.Fatalf("successful sends = %d, want 1", got)
	}
	completed := 0
	for _, item := range sink.List() {
		if item.Method == NotificationTurnCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("turn/completed notifications = %d, want 1", completed)
	}
}
