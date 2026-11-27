package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #64002: cancellation can detach startup after admission but
// before the user item is recorded. The parked input remains with the registered
// turn, so cleanup records it and the startup path cannot add a duplicate.
func TestRuntimeTurnInitialInputSurvivesSuspensionRaceLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	cwd := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   cwd,
	})
	defer router.Close()
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	const (
		turnID = "turn-suspended-startup"
		prompt = "input retained across suspension"
	)
	params := &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Prompt: prompt}
	ctx, cancel := context.WithCancel(context.Background())
	if err := router.registerTrackedActiveRuntimeTurn(threadID, turnID, cancel, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("registerTrackedActiveRuntimeTurn() error = %v", err)
	}
	if !router.parkRuntimeTurnInitialInput(threadID, turnID, params) {
		t.Fatal("initial input was not parked with the active turn")
	}
	router.armRuntimeTurnInitialInputPersistence(ctx, threadID, turnID)

	// SuspendAgentTree cancels the active task directly; cleanup must still own
	// and record the parked submission after the task detaches.
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		record, err := store.Read(session.ThreadID(threadID), true, true)
		if err != nil {
			t.Fatalf("Read thread error = %v", err)
		}
		if record != nil {
			prompts := 0
			for _, item := range record.Items {
				if item.Text == prompt {
					prompts++
				}
			}
			if prompts == 1 {
				break
			}
			if prompts > 1 {
				t.Fatalf("suspended prompt recorded %d times: %#v", prompts, record.Items)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("suspended initial input was not recorded by cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if router.persistRuntimeTurnInitialInput(threadID, turnID, params, time.Now()) {
		t.Fatal("startup path recorded an input already claimed by cleanup")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read final thread error = %v", err)
	}
	prompts := 0
	for _, item := range record.Items {
		if item.Text == prompt {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("final prompt count = %d, want 1: %#v", prompts, record.Items)
	}
	router.clearActiveRuntimeTurn(threadID, turnID)
}
