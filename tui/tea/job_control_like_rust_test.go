package tea

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"
	"codex_go/tui/keymsg"
)

func TestCtrlZSendsSuspendMsgLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows keeps Ctrl-Z on composer undo (Rust #61954); the suspend
		// binding only exists where the terminal driver provides job control.
		model := newSelectionModel(t, Options{Width: 60, Height: 12})
		if handled, _ := model.handleJobControl(key(keymsg.KeyCtrlZ)); handled {
			t.Fatal("Windows Ctrl-Z was intercepted as a terminal suspend")
		}
		return
	}
	model := newSelectionModel(t, Options{Width: 60, Height: 12})
	model.setComposerPrompt("keep this draft")
	_, cmd := model.Update(key(keymsg.KeyCtrlZ))
	if cmd == nil {
		t.Fatal("Ctrl-Z did not send a suspend command")
	}
	if msg := cmd(); reflect.TypeOf(msg) != reflect.TypeOf(bubbletea.SuspendMsg{}) {
		t.Fatalf("Ctrl-Z message = %T, want SuspendMsg", msg)
	}
	if model.composerText() != "keep this draft" {
		t.Fatal("suspend changed the draft")
	}
}

type fakeJobControlTerminal struct {
	events     *[]string
	releaseErr error
	restoreErr error
}

func (f fakeJobControlTerminal) ReleaseTerminal() error {
	*f.events = append(*f.events, "release")
	return f.releaseErr
}
func (f fakeJobControlTerminal) RestoreTerminal() error {
	*f.events = append(*f.events, "restore")
	return f.restoreErr
}

type fakeJobControlSession struct {
	events     *[]string
	owners     []bool
	checks     int
	checkErr   error
	suspendErr error
	onSuspend  func()
}

func (s *fakeJobControlSession) foreground() (bool, error) {
	*s.events = append(*s.events, "owner")
	s.checks++
	if s.checkErr != nil && s.checks > 1 {
		return false, s.checkErr
	}
	index := s.checks - 1
	if index >= len(s.owners) {
		index = len(s.owners) - 1
	}
	return s.owners[index], nil
}
func (s *fakeJobControlSession) suspend(context.Context) error {
	*s.events = append(*s.events, "suspend")
	if s.onSuspend != nil {
		s.onSuspend()
	}
	return s.suspendErr
}
func (s *fakeJobControlSession) close() { *s.events = append(*s.events, "close") }

func TestJobControlWaitsForForegroundBeforeRestoreLikeRust(t *testing.T) {
	var events []string
	session := &fakeJobControlSession{events: &events, owners: []bool{true, false, false, true}}
	msg := suspendWithJobControl(context.Background(), fakeJobControlTerminal{events: &events},
		func() (jobControlSession, error) { return session, nil })
	if _, ok := msg.(bubbletea.ResumeMsg); !ok {
		t.Fatalf("resume = %T", msg)
	}
	want := []string{"owner", "release", "suspend", "owner", "owner", "owner", "restore", "close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestJobControlNoControllingTerminalFailsSafelyLikeRust(t *testing.T) {
	var events []string
	msg := suspendWithJobControl(context.Background(), fakeJobControlTerminal{events: &events},
		func() (jobControlSession, error) { return nil, errors.New("no controlling terminal") })
	if _, ok := msg.(jobControlErrorMsg); !ok {
		t.Fatalf("failure = %T", msg)
	}
	if len(events) != 0 {
		t.Fatalf("terminal touched without control: %v", events)
	}
}

func TestJobControlUnownedTerminalNeverSendsSignalLikeRust(t *testing.T) {
	var events []string
	session := &fakeJobControlSession{events: &events, owners: []bool{false}}
	msg := suspendWithJobControl(context.Background(), fakeJobControlTerminal{events: &events},
		func() (jobControlSession, error) { return session, nil })
	if _, ok := msg.(jobControlErrorMsg); !ok {
		t.Fatalf("failure = %T", msg)
	}
	if !reflect.DeepEqual(events, []string{"owner", "close"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestJobControlTerminalLossNeverRestoresLikeRust(t *testing.T) {
	var events []string
	session := &fakeJobControlSession{events: &events, owners: []bool{true}, checkErr: errors.New("tty revoked")}
	msg := suspendWithJobControl(context.Background(), fakeJobControlTerminal{events: &events},
		func() (jobControlSession, error) { return session, nil })
	if _, ok := msg.(bubbletea.InterruptMsg); !ok {
		t.Fatalf("failure = %T", msg)
	}
	if !reflect.DeepEqual(events, []string{"owner", "release", "suspend", "owner", "close"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestJobControlCancellationNeverRestoresLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []string
	session := &fakeJobControlSession{events: &events, owners: []bool{true}, onSuspend: cancel}
	msg := suspendWithJobControl(ctx, fakeJobControlTerminal{events: &events},
		func() (jobControlSession, error) { return session, nil })
	if _, ok := msg.(bubbletea.InterruptMsg); !ok {
		t.Fatalf("failure = %T", msg)
	}
	if !reflect.DeepEqual(events, []string{"owner", "release", "suspend", "close"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestJobControlReleaseFailureNeverSendsSignalLikeRust(t *testing.T) {
	var events []string
	session := &fakeJobControlSession{events: &events, owners: []bool{true}}
	msg := suspendWithJobControl(context.Background(),
		fakeJobControlTerminal{events: &events, releaseErr: errors.New("release failed")},
		func() (jobControlSession, error) { return session, nil })
	if _, ok := msg.(bubbletea.InterruptMsg); !ok {
		t.Fatalf("failure = %T", msg)
	}
	if !reflect.DeepEqual(events, []string{"owner", "release", "close"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestJobControlForegroundPollCanCancelLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := waitForJobControlForeground(ctx, func() (bool, error) { calls++; cancel(); return false, nil }, time.Hour)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("poll = %v, calls %d", err, calls)
	}
}

func TestJobControlFilterLeavesOtherMessagesAloneLikeRust(t *testing.T) {
	filter := jobControlFilter(nil, func() jobControlTerminal { t.Fatal("unexpected terminal access"); return nil })
	msg := bubbletea.WindowSizeMsg{Width: 80, Height: 24}
	if got := filter(nil, msg); got != msg {
		t.Fatalf("filtered = %#v", got)
	}
}
