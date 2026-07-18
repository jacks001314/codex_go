package appserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

const windowsStatusDLLInitFailed int32 = -1073741502

func TestCommandExecTTYStreamsAndResizes(t *testing.T) {
	service := NewCommandExecService()
	service.DefaultTimeoutMS = 5_000
	sink := NewNotificationBuffer()
	processID := "tty-command-1"
	params := &CommandExecParams{
		Command:   commandExecTestTTYOutputCommand("tty-output"),
		ProcessID: &processID,
		TTY:       true,
		Size:      &TerminalSize{Rows: 24, Cols: 80},
	}

	responseCh := make(chan *CommandExecResponse, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, err := service.ExecuteWithNotify(context.Background(), params, t.TempDir(), sinkNotifyFunc(sink))
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- response
	}()
	waitForActiveCommandExecPTYOrResult(t, service, processID, errorCh, responseCh)
	if _, err := service.Resize(&CommandExecResizeParams{ProcessID: processID, Size: TerminalSize{Rows: 32, Cols: 100}}); err != nil {
		skipIfPendingCommandWindowsConPTYHostFailure(t, responseCh)
		t.Fatalf("Resize() error = %v", err)
	}
	waitForCommandExecOutputDeltaContaining(t, sink, responseCh, processID, StreamStdout, "tty-output")
	select {
	case err := <-errorCh:
		t.Fatalf("ExecuteWithNotify() error = %v", err)
	case response := <-responseCh:
		skipIfWindowsConPTYHostFailure(t, response.ExitCode)
		if response.ExitCode != 0 || response.Stdout != "" || response.Stderr != "" {
			t.Fatalf("tty final response = %+v", response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteWithNotify() did not return final response")
	}
}

func TestProcessServiceSpawnTTYStreamsAndResizes(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 5_000
	sink := NewNotificationBuffer()
	params := &ProcessSpawnParams{
		Command:       processTestTTYOutputCommand("pty-process"),
		ProcessHandle: "tty-process-1",
		CWD:           t.TempDir(),
		TTY:           true,
		Size:          &TerminalSize{Rows: 24, Cols: 80},
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	if _, err := service.Resize(&ProcessResizePtyParams{ProcessHandle: "tty-process-1", Size: TerminalSize{Rows: 30, Cols: 120}}); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	waitForProcessOutputDeltaContaining(t, sink, "tty-process-1", StreamStdout, "pty-process")
	exited := waitForProcessExited(t, sink, "tty-process-1").Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 || exited.Stdout != "" || exited.Stderr != "" {
		t.Fatalf("tty process exit = %+v", exited)
	}
}

func TestProcessServiceSpawnTTYStreamsWithoutResize(t *testing.T) {
	service := NewProcessService()
	service.DefaultTimeoutMS = 5_000
	sink := NewNotificationBuffer()
	params := &ProcessSpawnParams{
		Command:       processTestTTYOutputCommand("pty-process-no-resize"),
		ProcessHandle: "tty-process-no-resize-1",
		CWD:           t.TempDir(),
		TTY:           true,
		Size:          &TerminalSize{Rows: 24, Cols: 80},
	}

	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	waitForProcessOutputDeltaContaining(t, sink, params.ProcessHandle, StreamStdout, "pty-process-no-resize")
	exited := waitForProcessExited(t, sink, params.ProcessHandle).Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 || exited.Stdout != "" || exited.Stderr != "" {
		t.Fatalf("tty process exit = %+v", exited)
	}
}

func TestWaitForPTYOutputAfterExit(t *testing.T) {
	t.Run("returns when output arrives", func(t *testing.T) {
		activity := make(chan struct{}, 1)
		activity <- struct{}{}
		started := time.Now()
		waitForPTYOutputAfterExit(activity, make(chan struct{}))
		if elapsed := time.Since(started); elapsed >= ptyPostExitOutputWait {
			t.Fatalf("waited %s despite pending PTY output", elapsed)
		}
	})

	t.Run("returns when output closes", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		started := time.Now()
		waitForPTYOutputAfterExit(make(chan struct{}), done)
		if elapsed := time.Since(started); elapsed >= ptyPostExitOutputWait {
			t.Fatalf("waited %s despite closed PTY output", elapsed)
		}
	})
}

func TestPTYOutputHelperProcess(t *testing.T) {
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	time.Sleep(1500 * time.Millisecond)
	_, _ = os.Stdout.WriteString(os.Args[separator+1])
	os.Exit(0)
}

func commandExecTestTTYOutputCommand(value string) []string {
	if runtime.GOOS == "windows" {
		return windowsPTYOutputHelperCommand(value)
	}
	return []string{"sh", "-c", "printf " + shellQuote(value) + "; sleep 0.1"}
}

func processTestTTYOutputCommand(value string) []string {
	if runtime.GOOS == "windows" {
		return windowsPTYOutputHelperCommand(value)
	}
	return []string{"sh", "-c", "printf " + shellQuote(value) + "; sleep 0.1"}
}

func windowsPTYOutputHelperCommand(value string) []string {
	return []string{os.Args[0], "-test.run=^TestPTYOutputHelperProcess$", "--", value}
}

func waitForCommandExecOutputDeltaContaining(t *testing.T, sink *NotificationBuffer, responseCh <-chan *CommandExecResponse, processID string, stream OutputStream, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationCommandExecOutputDelta {
				continue
			}
			delta, ok := notification.Params.(*CommandExecOutputDeltaNotification)
			if !ok || delta.ProcessID != processID || delta.Stream != stream {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			if strings.Contains(string(decoded), want) {
				return
			}
		}
		select {
		case response := <-responseCh:
			if response != nil {
				skipIfWindowsConPTYHostFailure(t, response.ExitCode)
			}
			t.Fatalf("command/exec completed before expected output; response=%+v notifications=%s", response, describePTYNotifications(sink))
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	pendingResponse := pendingCommandExecResponse(t, responseCh)
	t.Fatalf("command/exec/outputDelta notification for %q %s containing %q not observed; response=%+v notifications=%s", processID, stream, want, pendingResponse, describePTYNotifications(sink))
}

func waitForActiveCommandExecPTYOrResult(t *testing.T, service *CommandExecService, processID string, errorCh <-chan error, responseCh <-chan *CommandExecResponse) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errorCh:
			t.Fatalf("ExecuteWithNotify() error before PTY became active: %v", err)
		case response := <-responseCh:
			skipIfWindowsConPTYHostFailure(t, response.ExitCode)
			t.Fatalf("ExecuteWithNotify() completed before PTY became active: %+v", response)
		default:
		}
		active, err := service.activeCommandExecForConnection(defaultRequestConnectionID, processID)
		if err == nil {
			if active.ptySession() != nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("command/exec %q did not become active with PTY", processID)
}

func waitForProcessOutputDeltaContaining(t *testing.T, sink *NotificationBuffer, handle string, stream OutputStream, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method == NotificationProcessExited {
				exited, ok := notification.Params.(*ProcessExitedNotification)
				if ok && exited.ProcessHandle == handle {
					skipIfWindowsConPTYHostFailure(t, exited.ExitCode)
				}
			}
			if notification.Method != NotificationProcessOutputDelta {
				continue
			}
			delta, ok := notification.Params.(*ProcessOutputDeltaNotification)
			if !ok || delta.ProcessHandle != handle || delta.Stream != stream {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			if strings.Contains(string(decoded), want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process/outputDelta notification for %q %s containing %q not observed; notifications=%s", handle, stream, want, describePTYNotifications(sink))
}

func skipIfWindowsConPTYHostFailure(t *testing.T, exitCode int32) {
	t.Helper()
	if runtime.GOOS == "windows" && exitCode == windowsStatusDLLInitFailed {
		t.Skipf("host ConPTY child exited with STATUS_DLL_INIT_FAILED (%d), matching the Rust legacy ConPTY CI limitation", exitCode)
	}
}

func skipIfPendingCommandWindowsConPTYHostFailure(t *testing.T, responseCh <-chan *CommandExecResponse) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	_ = pendingCommandExecResponse(t, responseCh)
}

func pendingCommandExecResponse(t *testing.T, responseCh <-chan *CommandExecResponse) *CommandExecResponse {
	t.Helper()
	if runtime.GOOS != "windows" {
		return nil
	}
	select {
	case response := <-responseCh:
		if response != nil {
			skipIfWindowsConPTYHostFailure(t, response.ExitCode)
		}
		return response
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func describePTYNotifications(sink *NotificationBuffer) string {
	var parts []string
	for _, notification := range sink.List() {
		switch params := notification.Params.(type) {
		case *CommandExecOutputDeltaNotification:
			decoded, _ := base64.StdEncoding.DecodeString(params.DeltaBase64)
			parts = append(parts, string(notification.Method)+":"+params.ProcessID+":"+string(params.Stream)+":"+string(decoded))
		case *ProcessOutputDeltaNotification:
			decoded, _ := base64.StdEncoding.DecodeString(params.DeltaBase64)
			parts = append(parts, string(notification.Method)+":"+params.ProcessHandle+":"+string(params.Stream)+":"+string(decoded))
		case *ProcessExitedNotification:
			parts = append(parts, string(notification.Method)+":"+params.ProcessHandle+":"+fmt.Sprint(params.ExitCode)+":"+params.Stdout+":"+params.Stderr)
		default:
			parts = append(parts, string(notification.Method))
		}
	}
	return strings.Join(parts, " | ")
}
