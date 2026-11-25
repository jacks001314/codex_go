package tea

import (
	"context"
	"fmt"
	"runtime"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/keymsg"
	tuiterm "codex_go/tui/tui"
)

type jobControlErrorMsg struct{ err error }

func (m *Model) handleJobControl(message bubbletea.Msg) (bool, bubbletea.Cmd) {
	switch msg := message.(type) {
	case keymsg.KeyMsg:
		if runtime.GOOS != "windows" && msg.String() == tuiterm.SuspendKey {
			return true, bubbletea.Suspend
		}
	case jobControlErrorMsg:
		m.notice = "Cannot suspend: " + msg.err.Error()
		return true, nil
	case bubbletea.ResumeMsg:
		m.configureAlternateScrollForSurface()
		return true, nil
	}
	return false, nil
}

type jobControlTerminal interface {
	ReleaseTerminal() error
	RestoreTerminal() error
}

type jobControlSession interface {
	foreground() (bool, error)
	suspend(context.Context) error
	close()
}

// Bubble Tea v2.1.1 restores raw mode before delivering ResumeMsg. Intercept
// SuspendMsg before its built-in handler so even a background SIGCONT leaves
// input and rendering released until the shell gives this process the tty.
func jobControlFilter(ctx context.Context, terminal func() jobControlTerminal) func(bubbletea.Model, bubbletea.Msg) bubbletea.Msg {
	if ctx == nil {
		ctx = context.Background()
	}
	return func(_ bubbletea.Model, msg bubbletea.Msg) bubbletea.Msg {
		if _, ok := msg.(bubbletea.SuspendMsg); !ok {
			return msg
		}
		return suspendWithJobControl(ctx, terminal(), openJobControlSession)
	}
}

func suspendWithJobControl(ctx context.Context, terminal jobControlTerminal, open func() (jobControlSession, error)) bubbletea.Msg {
	session, err := open()
	if err != nil {
		return jobControlErrorMsg{err}
	}
	defer session.close()
	foreground, err := session.foreground()
	if err != nil {
		return jobControlErrorMsg{err}
	}
	if !foreground {
		return jobControlErrorMsg{fmt.Errorf("process does not own the foreground terminal")}
	}
	if err := terminal.ReleaseTerminal(); err != nil {
		// Release may have only partly succeeded. Stop rather than re-entering
		// raw mode or delivering a frame on an uncertain terminal.
		return bubbletea.InterruptMsg{}
	}
	suspendErr := session.suspend(ctx)
	if err := waitForJobControlForeground(ctx, session.foreground, 10*time.Millisecond); err != nil {
		// In particular, never call RestoreTerminal after tty loss or cancel.
		return bubbletea.InterruptMsg{}
	}
	if err := terminal.RestoreTerminal(); err != nil {
		return bubbletea.InterruptMsg{}
	}
	if suspendErr != nil {
		return jobControlErrorMsg{suspendErr}
	}
	return bubbletea.ResumeMsg{}
}

func waitForJobControlForeground(ctx context.Context, foreground func() (bool, error), interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		owned, err := foreground()
		if err != nil {
			return err
		}
		if owned {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
