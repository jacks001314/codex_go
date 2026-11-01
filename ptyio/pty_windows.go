//go:build windows

// Package ptyio drives a Unix pseudo-terminal master through readiness instead
// of blocking reads (Rust bf3eb2ec91, #40460). Windows uses ConPTY, whose
// blocking pipe handles os/exec already closes when a session ends, so this
// package is Unix-only.
package ptyio

import (
	"errors"
	"os"
)

// ErrUnsupported reports that the platform has no readiness-driven PTY master.
var ErrUnsupported = errors.New("pty readiness I/O is not supported on this platform")

// Master is the Unix readiness-driven PTY handle; Windows has no equivalent.
type Master struct{}

// New reports that the platform has no readiness-driven PTY master.
func New(*os.File) (*Master, error) {
	return nil, ErrUnsupported
}

func (m *Master) Read([]byte) (int, error)     { return 0, ErrUnsupported }
func (m *Master) Write([]byte) (int, error)    { return 0, ErrUnsupported }
func (m *Master) InterruptRead() error         { return ErrUnsupported }
func (m *Master) Interrupt() error             { return ErrUnsupported }
func (m *Master) SetSize(uint16, uint16) error { return ErrUnsupported }
func (m *Master) SendEOF() error               { return ErrUnsupported }
func (m *Master) Close() error                 { return nil }
