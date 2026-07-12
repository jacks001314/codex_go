package execserver

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func TTYOutputAvailable(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	const marker = "CODEX_EXEC_SERVER_PTY_PROBE"
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("ComSpec")
		if strings.TrimSpace(comspec) == "" {
			comspec = "cmd.exe"
		}
		command = exec.CommandContext(ctx, comspec, "/d", "/c", "echo "+marker)
	} else {
		command = exec.CommandContext(ctx, "sh", "-c", "printf "+marker)
	}
	started, supported, err := startExecServerTTY(command)
	if err != nil || !supported {
		return false, err
	}
	readDone := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(started.reader)
		readDone <- data
	}()
	_, waitErr := started.wait()
	if started.closePTY != nil {
		_ = started.closePTY()
	}
	var output []byte
	select {
	case output = <-readDone:
	case <-time.After(time.Second):
		_ = started.reader.Close()
		output = <-readDone
	}
	if started.cleanup != nil {
		_ = started.cleanup()
	}
	if waitErr != nil {
		return false, waitErr
	}
	return strings.Contains(string(output), marker), nil
}
