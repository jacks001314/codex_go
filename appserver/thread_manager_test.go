package appserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/rollout"
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

func TestThreadManagerShutdownWaitsForTrackedTerminalWorker(t *testing.T) {
	manager := NewThreadManager(nil)
	if err := manager.RegisterTurn("thread-1", "turn-1", nil, 1, &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if _, ok := manager.ConsumeTurnTracked("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurnTracked() = false")
	}

	waitReturned := make(chan error, 1)
	go func() {
		manager.BeginShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		waitReturned <- manager.WaitForTurnWorkers(ctx)
	}()
	select {
	case err := <-waitReturned:
		t.Fatalf("WaitForTurnWorkers() returned before terminal worker completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	manager.TurnWorkerDone()
	if err := <-waitReturned; err != nil {
		t.Fatalf("WaitForTurnWorkers() error = %v", err)
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

func TestRuntimeRouterPaginatedPersistenceDoesNotBypassClosedLiveThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	record := &session.Record{
		ID:        "thread-live-persistence",
		CreatedAt: time.Now().UTC(),
		Metadata:  session.Metadata{HistoryMode: string(ThreadHistoryPaginated)},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	threadRouter := NewRouter(store)
	if err := threadRouter.retainLiveThread(record); err != nil {
		t.Fatalf("retainLiveThread() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	t.Cleanup(func() { _ = router.Close() })
	liveThread := router.threads.LiveThread(record.ID)
	if liveThread == nil {
		t.Fatal("paginated thread was not registered as a live thread")
	}
	if err := liveThread.Close(); err != nil {
		t.Fatalf("liveThread.Close() error = %v", err)
	}
	if _, err := router.runtimeAppendItem(record.ID, session.Item{ID: "must-not-persist"}); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("runtimeAppendItem() error = %v, want closed live-thread conflict", err)
	}
	if _, err := router.runtimeUpdateThreadMetadata(record.ID, &session.MetadataPatch{}, true); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("runtimeUpdateThreadMetadata() error = %v, want closed live-thread conflict", err)
	}
	if _, err := threadRouter.readThreadRecord(record.ID, true, true); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("readThreadRecord() error = %v, want closed live-thread conflict", err)
	}
	if err := threadRouter.saveThreadRecord(&session.Record{ID: record.ID, Title: "must-not-persist"}); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("saveThreadRecord() error = %v, want closed live-thread conflict", err)
	}
	metadataTitle := "must-not-persist"
	if _, err := threadRouter.updateThreadMetadata(record.ID, &session.MetadataPatch{Title: &metadataTitle}, true); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("updateThreadMetadata() error = %v, want closed live-thread conflict", err)
	}
	if _, err := threadRouter.appendThreadItems(record.ID, []session.Item{{ID: "also-must-not-persist"}}); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("appendThreadItems() error = %v, want closed live-thread conflict", err)
	}
	loaded, err := store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(loaded.Items) != 0 {
		t.Fatalf("closed live thread append bypassed manager: %#v", loaded.Items)
	}
	if loaded.Title != "" {
		t.Fatalf("closed live thread metadata bypassed manager: title=%q", loaded.Title)
	}
}

func TestThreadManagerLegacyLiveThreadStillAcquiresLifecycleWriter(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	record := &session.Record{ID: "thread-legacy-live", Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)}}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	manager := NewThreadManager(nil)
	if err := manager.RetainLiveThread(store, record); err != nil {
		t.Fatalf("RetainLiveThread() error = %v", err)
	}
	defer func() { _ = manager.CloseLiveThreads() }()
	liveThread := manager.LiveThread(record.ID)
	if liveThread == nil || liveThread.OwnsWriter() {
		t.Fatalf("legacy live thread = %#v, ownsWriter = %v", liveThread, liveThread != nil && liveThread.OwnsWriter())
	}
	locks, err := manager.AcquireLifecycleWriters(store, []session.ThreadID{record.ID})
	if err != nil {
		t.Fatalf("AcquireLifecycleWriters() error = %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("lifecycle writer count = %d, want 1", len(locks))
	}
	if _, err := session.NewStore(root).AcquireWriter(record.ID); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("competing AcquireWriter() error = %v, want ErrConflict", err)
	}
	closeTemporaryWriters(locks)
}

func TestThreadManagerSerializesRolloutRecorderLifecycle(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	record := &session.Record{ID: "thread-managed-rollout", SessionID: "thread-managed-rollout", CreatedAt: time.Now().UTC()}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	manager := NewThreadManager(nil)
	if err := manager.RetainLiveThread(store, record); err != nil {
		t.Fatalf("RetainLiveThread() error = %v", err)
	}
	openCount := 0
	var rolloutPath string
	open := func() (*rollout.Recorder, error) {
		openCount++
		if rolloutPath != "" {
			return rollout.Resume(rolloutPath)
		}
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome: root,
			SessionID: string(record.ID),
			ThreadID:  string(record.ID),
			Now:       record.CreatedAt,
		})
		if recorder != nil {
			rolloutPath = recorder.Path()
		}
		return recorder, err
	}
	for _, message := range []string{"first", "second"} {
		handled, err := manager.WithRolloutRecorder(record.ID, open, func(recorder *rollout.Recorder) error {
			return recorder.AppendTurnError(message, time.Now().UTC())
		})
		if !handled || err != nil {
			t.Fatalf("WithRolloutRecorder(%q) = %v, %v", message, handled, err)
		}
	}
	if openCount != 2 {
		t.Fatalf("rollout recorder opens = %d, want one short-lived handle per append", openCount)
	}
	if err := manager.CloseLiveThreads(); err != nil {
		t.Fatalf("CloseLiveThreads() error = %v", err)
	}
	lines, _, err := rollout.Load(rolloutPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("rollout line count = %d, want session meta plus two errors", len(lines))
	}
	if handled, err := manager.WithRolloutRecorder(record.ID, open, nil); handled || err != nil {
		t.Fatalf("WithRolloutRecorder() after close = %v, %v", handled, err)
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
