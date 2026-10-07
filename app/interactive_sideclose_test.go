package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"codex_go/cli"
	codexexec "codex_go/exec"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// localSideCloseRunner mirrors the real exec runner: it reports the started turn
// through the hook the production path installs (exec/exec.go OnTurnStarted ->
// app/interactive.go `execRequest.OnTurnStarted = controller.setActive`) and then
// blocks until the turn context is cancelled.
type localSideCloseRunner struct {
	started  chan struct{}
	done     chan struct{}
	threadID string
}

func (r *localSideCloseRunner) RunContext(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
	if req != nil && req.OnTurnStarted != nil {
		req.OnTurnStarted(r.threadID, "turn-side-1")
	}
	close(r.started)
	<-ctx.Done()
	close(r.done)
	return nil, ctx.Err()
}

var _ interactiveTurnRunner = (*localSideCloseRunner)(nil)

func newLocalSideCloseTestStore(t *testing.T) *session.Store {
	t.Helper()
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: "thread-parent", SessionID: "thread-parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Items: []session.Item{{ID: "item-user", Type: "message", Role: "user", Text: "parent question", CreatedAt: now}},
	}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	return store
}

func startLocalSideCloseTestTurn(t *testing.T, interrupts *interactiveInterruptController, threadID string) *localSideCloseRunner {
	t.Helper()
	runner := &localSideCloseRunner{started: make(chan struct{}), done: make(chan struct{}), threadID: threadID}
	state := codextui.NewState(nil)
	state.SetThreadID(threadID)
	command := interactiveTurnCommandWithRequest(context.Background(), &cli.RootOptions{}, runner, state, codextea.SubmitRequest{Prompt: "long local task"}, nil, nil, nil, interrupts)
	if message := command(); message == nil {
		t.Fatal("turn command returned no message")
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("local turn never reached the runner")
	}
	return runner
}

// Rust `App::discard_side_thread` interrupts the side thread's running turn
// before the thread is discarded (tui/src/app/side.rs:428/433 ->
// interrupt_side_thread at :549-558). The embedded TUI must do the same: its side
// turn runs on the in-process exec runner, so `thread/delete` alone deletes the
// session record while the turn keeps running against a thread that is gone.
func TestInteractiveLocalSideCloseInterruptsRunningTurn(t *testing.T) {
	store := newLocalSideCloseTestStore(t)
	coordinator := newInteractiveLocalSideCoordinator(store)
	started, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	sideThreadID := started.SideThreadID

	interrupts := newInteractiveInterruptController()
	runner := startLocalSideCloseTestTurn(t, interrupts, sideThreadID)
	interrupts.mu.Lock()
	activeThread, activeTurn := interrupts.threadID, interrupts.turnID
	interrupts.mu.Unlock()
	if activeThread != sideThreadID || activeTurn != "turn-side-1" {
		t.Fatalf("interrupt controller tracks %q/%q, want %q/turn-side-1", activeThread, activeTurn, sideThreadID)
	}

	// Exactly the handler the embedded TUI installs as Options.OnCloseSide.
	closeSide := interactiveLocalSideClose(interrupts, coordinator)
	if _, err := closeSide(codextea.SideCloseParams{ParentThreadID: "thread-parent", SideThreadID: sideThreadID}); err != nil {
		t.Fatalf("OnCloseSide() error = %v", err)
	}
	if _, err := store.Read(session.ThreadID(sideThreadID), true, true); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("side record survived close: %v", err)
	}
	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("local side close left the running turn alive: turn context was never cancelled")
	}
}

// A side close must only interrupt the side thread's own turn: when the parent
// turn is the one running, closing a side conversation has to leave it alone
// (Rust `App::active_turn_id_for_thread` is thread-scoped).
func TestInteractiveLocalSideCloseKeepsOtherRunningTurn(t *testing.T) {
	store := newLocalSideCloseTestStore(t)
	coordinator := newInteractiveLocalSideCoordinator(store)
	started, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	interrupts := newInteractiveInterruptController()
	runner := startLocalSideCloseTestTurn(t, interrupts, "thread-parent")
	closeSide := interactiveLocalSideClose(interrupts, coordinator)
	if _, err := closeSide(codextea.SideCloseParams{ParentThreadID: "thread-parent", SideThreadID: started.SideThreadID}); err != nil {
		t.Fatalf("OnCloseSide() error = %v", err)
	}
	select {
	case <-runner.done:
		t.Fatal("closing a side conversation cancelled the parent turn")
	case <-time.After(300 * time.Millisecond):
	}
	if !interrupts.interrupt() {
		t.Fatal("control: parent turn was no longer interruptible")
	}
	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("control: interrupt did not cancel the parent turn")
	}
}

func TestInteractiveInterruptControllerInterruptThreadScopesToThread(t *testing.T) {
	interrupts := newInteractiveInterruptController()
	if interrupts.interruptThread("") {
		t.Fatal("interruptThread(empty) = true, want false")
	}
	if interrupts.interruptThread("thread-side") {
		t.Fatal("interruptThread() = true without an active turn")
	}
	ctx, done := interrupts.begin(context.Background())
	defer done()
	interrupts.setActive("thread-side", "turn-side-1")
	if interrupts.interruptThread("thread-parent") {
		t.Fatal("interruptThread(other thread) = true, want false")
	}
	if ctx.Err() != nil {
		t.Fatal("interruptThread of another thread cancelled the turn")
	}
	if !interrupts.interruptThread("thread-side") {
		t.Fatal("interruptThread(tracked thread) = false, want true")
	}
	if ctx.Err() == nil {
		t.Fatal("interruptThread(tracked thread) did not cancel the turn")
	}
	interrupts.mu.Lock()
	threadID, turnID, cancel := interrupts.threadID, interrupts.turnID, interrupts.cancel
	interrupts.mu.Unlock()
	if threadID != "" || turnID != "" || cancel != nil {
		t.Fatalf("interruptThread left %q/%q/%v, want cleared tracking state", threadID, turnID, cancel != nil)
	}
}
