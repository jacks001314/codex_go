//go:build !windows

// Package ptyio drives a Unix pseudo-terminal master through readiness instead
// of blocking reads, so a read or a write can be interrupted while a detached
// child still holds the terminal (Rust bf3eb2ec91, #40460).
package ptyio

import (
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ErrUnsupported reports that the platform has no readiness-driven PTY master.
var ErrUnsupported = errors.New("pty readiness I/O is not supported on this platform")

// Master is a pty master whose reads and writes are driven by the Go poller.
//
// Rust's unix_io::PtyIo duplicates the master, marks the duplicate O_NONBLOCK
// and awaits readiness, because a blocking read cannot be cancelled when a
// detached child retains the slave: the read stays parked in the kernel even
// after the descriptor is closed from another task. Go reaches the same state
// by handing a nonblocking descriptor to os.NewFile, which registers it with
// netpoll; SetReadDeadline and Close then wake a parked Read.
type Master struct {
	file *os.File
	raw  *os.File
	fd   int

	closeOnce sync.Once
	closeErr  error
}

// New takes ownership of the master that pty.Start returned and returns a
// readiness-driven handle for it. The original descriptor and the duplicate
// share their file description, so after New the original must not be used for
// I/O: it is nonblocking too and would return EAGAIN instead of waiting.
func New(master *os.File) (*Master, error) {
	if master == nil {
		return nil, ErrUnsupported
	}
	duplicate, err := unix.Dup(int(master.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(duplicate)
	if err := unix.SetNonblock(duplicate, true); err != nil {
		_ = unix.Close(duplicate)
		return nil, err
	}
	return &Master{
		file: os.NewFile(uintptr(duplicate), master.Name()),
		raw:  master,
		fd:   duplicate,
	}, nil
}

// Read returns the next chunk of terminal output. It parks in the poller, so it
// returns as soon as the terminal is closed or its read deadline expires.
func (m *Master) Read(buffer []byte) (int, error) {
	if m == nil || m.file == nil {
		return 0, os.ErrClosed
	}
	return m.file.Read(buffer)
}

// Write sends input to the terminal, waiting for writability rather than
// blocking the thread when the terminal input queue is full.
func (m *Master) Write(data []byte) (int, error) {
	if m == nil || m.file == nil {
		return 0, os.ErrClosed
	}
	return m.file.Write(data)
}

// InterruptRead wakes a parked Read without closing the terminal, which keeps
// the write side usable. Rust aborts the reader task instead.
func (m *Master) InterruptRead() error {
	if m == nil || m.file == nil {
		return nil
	}
	return m.file.SetReadDeadline(time.Now())
}

// Interrupt wakes both a parked Read and a parked Write.
func (m *Master) Interrupt() error {
	if m == nil || m.file == nil {
		return nil
	}
	now := time.Now()
	readErr := m.file.SetReadDeadline(now)
	writeErr := m.file.SetWriteDeadline(now)
	if readErr != nil {
		return readErr
	}
	return writeErr
}

// SetSize resizes the terminal. It goes through the raw descriptor: os.File.Fd
// would put the shared file description back into blocking mode and silently
// disable the readiness used above.
func (m *Master) SetSize(rows uint16, cols uint16) error {
	if m == nil || m.fd < 0 {
		return os.ErrClosed
	}
	return unix.IoctlSetWinsize(m.fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
}

// SendEOF ends terminal input the way portable-pty does on Unix: a newline to
// complete a partial line, then the terminal's current VEOF byte.
func (m *Master) SendEOF() error {
	if m == nil || m.file == nil {
		return os.ErrClosed
	}
	termios, err := terminalTermios(m.fd)
	if err != nil {
		return err
	}
	eof := termios.Cc[unix.VEOF]
	if eof == 0 {
		return nil
	}
	_, err = m.file.Write([]byte{'\n', eof})
	return err
}

// Close releases the terminal. Closing the pollable duplicate wakes a parked
// Read with os.ErrClosed; the original descriptor is closed with it so the child
// sees the hangup.
func (m *Master) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		if m.file != nil {
			m.closeErr = m.file.Close()
		}
		if m.raw != nil {
			if err := m.raw.Close(); err != nil && m.closeErr == nil {
				m.closeErr = err
			}
		}
	})
	return m.closeErr
}
