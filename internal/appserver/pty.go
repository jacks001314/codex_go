package appserver

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"
)

var ErrPTYUnsupported = errors.New("pty is not supported on this platform")

var ptyPostExitOutputWait = func() time.Duration {
	if runtime.GOOS == "windows" {
		// ConPTY can deliver the final output after the process handle is
		// signaled. Match Rust's bounded IO drain window before closing it.
		return 2 * time.Second
	}
	return 50 * time.Millisecond
}()

var ptyOutputDrainTimeout = func() time.Duration {
	if runtime.GOOS == "windows" {
		return 2 * time.Second
	}
	return 500 * time.Millisecond
}()

type ptyProcess struct {
	wait func() error
	kill func() error
}

func (p *ptyProcess) Wait() error {
	if p == nil || p.wait == nil {
		return nil
	}
	return p.wait()
}

func (p *ptyProcess) Kill() error {
	if p == nil || p.kill == nil {
		return nil
	}
	return p.kill()
}

type ptyHandle struct {
	reader      io.Reader
	writer      io.WriteCloser
	normalize   func([]byte) []byte
	closeInput  func() error
	closePTY    func() error
	closeReader func() error
	cleanup     func() error
	resize      func(*TerminalSize) error

	closeInputOnce  sync.Once
	closeInputErr   error
	closePTYOnce    sync.Once
	closePTYErr     error
	closeReaderOnce sync.Once
	closeReaderErr  error
	cleanupOnce     sync.Once
	cleanupErr      error
}

func (h *ptyHandle) Write(p []byte) (int, error) {
	if h == nil || h.writer == nil {
		return 0, io.ErrClosedPipe
	}
	originalLen := len(p)
	if h.normalize != nil {
		p = h.normalize(p)
		if len(p) == 0 {
			return originalLen, nil
		}
	}
	n, err := h.writer.Write(p)
	if err != nil {
		return 0, err
	}
	if n != len(p) {
		return 0, io.ErrShortWrite
	}
	return originalLen, nil
}

func (h *ptyHandle) Close() error {
	if h == nil {
		return nil
	}
	h.closeInputOnce.Do(func() {
		if h.closeInput != nil {
			h.closeInputErr = h.closeInput()
			return
		}
		if h.writer != nil {
			h.closeInputErr = h.writer.Close()
		}
	})
	return h.closeInputErr
}

func (h *ptyHandle) ClosePTY() error {
	if h == nil {
		return nil
	}
	h.closePTYOnce.Do(func() {
		if h.closePTY != nil {
			h.closePTYErr = h.closePTY()
			return
		}
		h.closePTYErr = h.Close()
	})
	return h.closePTYErr
}

func (h *ptyHandle) CloseReader() error {
	if h == nil {
		return nil
	}
	h.closeReaderOnce.Do(func() {
		if h.closeReader != nil {
			h.closeReaderErr = h.closeReader()
		}
	})
	return h.closeReaderErr
}

func (h *ptyHandle) Cleanup() error {
	if h == nil {
		return nil
	}
	h.cleanupOnce.Do(func() {
		if h.cleanup != nil {
			h.cleanupErr = h.cleanup()
		}
	})
	return h.cleanupErr
}

func (h *ptyHandle) Resize(size *TerminalSize) error {
	if h == nil || h.resize == nil {
		return fmt.Errorf("%w: pty resize is unavailable", ErrInvalidRequest)
	}
	return h.resize(size)
}

func readPTYOutput(handle *ptyHandle, output *commandExecOutputBuffer, activity chan<- struct{}, done chan<- struct{}, notify func([]byte)) {
	if done != nil {
		defer close(done)
	}
	if handle == nil || handle.reader == nil {
		return
	}
	buffer := make([]byte, 32*1024)
	for {
		n, err := handle.reader.Read(buffer)
		if n > 0 {
			if activity != nil {
				select {
				case activity <- struct{}{}:
				default:
				}
			}
			chunk := buffer[:n]
			if output != nil {
				before := output.Len()
				_, _ = output.Write(chunk)
				chunk = output.BytesFrom(before)
			}
			if len(chunk) > 0 && notify != nil {
				notify(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

func waitForPTYOutputAfterExit(activity <-chan struct{}, done <-chan struct{}) {
	if activity == nil && done == nil {
		return
	}
	timer := time.NewTimer(ptyPostExitOutputWait)
	defer timer.Stop()
	select {
	case <-activity:
	case <-done:
	case <-timer.C:
	}
}

func waitForPTYOutputDone(handle *ptyHandle, done <-chan struct{}) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(ptyOutputDrainTimeout):
		if handle != nil {
			_ = handle.CloseReader()
		}
		<-done
	}
	if handle != nil {
		_ = handle.CloseReader()
	}
}

func monitorPTYContext(ctxDone <-chan struct{}, processDone <-chan struct{}, process *ptyProcess) {
	if ctxDone == nil || process == nil {
		return
	}
	select {
	case <-processDone:
		return
	default:
	}
	select {
	case <-ctxDone:
		select {
		case <-processDone:
			return
		default:
			_ = process.Kill()
		}
	case <-processDone:
	}
}

func terminalSizeOrDefault(size *TerminalSize) *TerminalSize {
	if size != nil {
		return size
	}
	return &TerminalSize{Rows: 24, Cols: 80}
}

type ptyExitError struct {
	code int
}

func (e *ptyExitError) Error() string {
	if e == nil {
		return "exit status 0"
	}
	return fmt.Sprintf("exit status %d", e.code)
}

func (e *ptyExitError) ExitCode() int {
	if e == nil {
		return 0
	}
	return e.code
}
