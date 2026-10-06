package tcptunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
)

// Opt-in, bounded diagnostics contain only classifications, never error-chain
// or wire values (Rust tcp-tunnel/src/diagnostics.rs, #50131). Nonterminal
// records describe one stream or a recoverable shared-transport event.

// Diagnostics selects human-readable or credential-safe JSON diagnostics.
type Diagnostics int

const (
	// DiagnosticsHuman is the default human-readable stderr output.
	DiagnosticsHuman Diagnostics = iota
	// DiagnosticsJSON emits versioned newline-delimited JSON on stderr.
	DiagnosticsJSON
)

// Phase names where a failure occurred.
type Phase int

const (
	PhaseStartup Phase = iota
	PhaseConnect
	PhaseTransport
	PhaseControl
)

func (p Phase) String() string {
	switch p {
	case PhaseConnect:
		return "connect"
	case PhaseTransport:
		return "transport"
	case PhaseControl:
		return "control"
	default:
		return "startup"
	}
}

// Code classifies a failure for JSON diagnostics.
type Code int

const (
	CodeFailed Code = iota
	CodeTimeout
	CodeHandshakeTimeout
	CodeRejected
	CodeClosed
	CodeDraining
	CodeInvalidInput
)

func (c Code) recordCode() string {
	switch c {
	case CodeTimeout, CodeHandshakeTimeout:
		return "timeout"
	case CodeRejected:
		return "rejected"
	case CodeClosed:
		return "closed"
	case CodeDraining:
		return "draining"
	case CodeInvalidInput:
		return "invalid_input"
	default:
		return "failed"
	}
}

// phaseError tags the phase in which a failure occurred.
type phaseError struct {
	phase Phase
	err   error
}

func (e *phaseError) Error() string { return e.err.Error() }
func (e *phaseError) Unwrap() error { return e.err }

// codeError classifies a failure without exposing the underlying chain.
type codeError struct {
	code   Code
	status int
	err    error
}

func (e *codeError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	switch e.code {
	case CodeTimeout:
		return "connecting to MASQUE proxy timed out"
	case CodeHandshakeTimeout:
		return "QUIC handshake failed: QUIC handshake timed out"
	case CodeRejected:
		return fmt.Sprintf("MASQUE CONNECT rejected with status %d", e.status)
	case CodeClosed:
		return "closed"
	case CodeDraining:
		return "draining"
	case CodeInvalidInput:
		return "invalid control input"
	default:
		return "failed"
	}
}

func (e *codeError) Unwrap() error { return e.err }

// classify attaches a diagnostic code to err (or creates a code-only error).
func classify(err error, code Code) error {
	if err == nil {
		return &codeError{code: code}
	}
	return &codeError{code: code, err: err}
}

// rejectedError classifies a CONNECT rejection with its (untrusted) status.
func rejectedError(status int, err error) error {
	return &codeError{code: CodeRejected, status: status, err: err}
}

// withPhase tags err with the phase it failed in, but only for JSON diagnostics.
func (d Diagnostics) withPhase(err error, phase Phase) error {
	if err == nil || d != DiagnosticsJSON {
		return err
	}
	return &phaseError{phase: phase, err: err}
}

// finish flushes one terminal JSON record before returning the generic error
// the CLI's top-level handler is allowed to print.
func (d Diagnostics) finish(result error, phase Phase, output io.Writer) error {
	if d == DiagnosticsJSON && result != nil {
		d.writeDiagnostic(output, phase, result, true)
		return errors.New("TCP tunnel failed")
	}
	return result
}

// report emits a nonterminal record (JSON) or the human-readable message.
func (d Diagnostics) report(phase Phase, err error, human string, output io.Writer) {
	if d == DiagnosticsJSON {
		d.writeDiagnostic(output, phase, err, false)
		return
	}
	fmt.Fprintln(output, human)
}

// writeDiagnostic emits one bounded, flushed newline-delimited JSON record.
func (d Diagnostics) writeDiagnostic(output io.Writer, fallbackPhase Phase, err error, terminal bool) {
	phase := fallbackPhase
	var phaseTag *phaseError
	if errors.As(err, &phaseTag) {
		phase = phaseTag.phase
	}
	var codeTag *codeError
	status := 0
	var code Code
	if errors.As(err, &codeTag) {
		code = codeTag.code
		status = codeTag.status
	} else if phase == PhaseControl && !isIOError(err) {
		code = CodeInvalidInput
	} else {
		code = CodeFailed
	}
	record := map[string]any{
		"v":        1,
		"phase":    phase.String(),
		"code":     code.recordCode(),
		"terminal": terminal,
	}
	if phase == PhaseConnect && code == CodeRejected &&
		status >= 100 && status < 600 && (status < 200 || status >= 300) {
		record["http_status"] = status
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = output.Write(append(encoded, '\n'))
	if flusher, ok := output.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}
}

// isIOError reports whether err's chain contains a filesystem or network error,
// used to keep control-input failures classified as `failed` rather than
// `invalid_input` (matches Rust's `std::io::Error` downcast).
func isIOError(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *fs.PathError
	var netErr net.Error
	return errors.As(err, &pathErr) || errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe)
}
