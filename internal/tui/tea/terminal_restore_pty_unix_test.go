//go:build !windows

package tea

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	codextui "codex_go/internal/tui"
)

const realPTYTerminalRestoreEnv = "CODEX_GO_TUI_PTY_SMOKE"

func TestRealPTYTerminalRestoreSmoke(t *testing.T) {
	if os.Getenv(realPTYTerminalRestoreEnv) != "1" {
		t.Skipf("set %s=1 to run the real PTY terminal restore smoke", realPTYTerminalRestoreEnv)
	}

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()

	if err := pty.Setsize(slave, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

	outputDone := make(chan string, 1)
	go func() {
		var output bytes.Buffer
		_, _ = io.Copy(&output, master)
		outputDone <- output.String()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		_, runErr := Run(ctx, codextui.NewState(nil), Options{Width: 80, Height: 24}, slave, slave)
		runDone <- runErr
	}()

	time.Sleep(150 * time.Millisecond)
	if _, err := master.Write([]byte{0x03}); err != nil {
		t.Fatalf("send Ctrl+C to pty: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("run TUI in pty: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("TUI did not exit after Ctrl+C: %v", ctx.Err())
	}

	_ = slave.Close()
	_ = master.Close()

	var output string
	select {
	case output = <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PTY output drain")
	}

	if !strings.Contains(output, "\x1b[?1049h") {
		t.Fatalf("PTY output did not enter alternate screen; output=%q", output)
	}
	if !strings.Contains(output, "\x1b[?1049l") {
		t.Fatalf("PTY output did not leave alternate screen; output=%q", output)
	}
}
