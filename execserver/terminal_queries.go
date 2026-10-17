package execserver

import (
	"bytes"
	"io"
	"sync"
)

// Rust parity: codex-rs/sandboxing/src/terminal_queries.rs (#41436). Programs
// running in a PTY can block waiting for terminal replies, so the exec server
// answers a bounded set of queries and removes them from the output stream.
const (
	terminalQueryMaxModeDigits = 10
	terminalQueryMaxBytes      = terminalQueryMaxModeDigits + 5
)

var terminalQueryResponses = [][2]string{
	// Device status report: report that the terminal is operating normally.
	{"\x1b[5n", "\x1b[0n"},
	// Window-size query: report a 24-row, 80-column text area.
	{"\x1b[18t", "\x1b[8;24;80t"},
	// Cursor-position report: return row 1, column 1.
	{"\x1b[6n", "\x1b[1;1R"},
}

// processTerminalQueries mirrors Rust's TerminalQueryResponder::process. It
// returns filtered output, responses to write to the PTY, and the bytes still
// pending an incomplete escape sequence. Queries split across chunks are
// carried in pending.
func processTerminalQueries(pending, chunk []byte) (output, responses, remaining []byte) {
	if len(pending) == 0 && bytes.IndexByte(chunk, 0x1b) < 0 {
		return append([]byte(nil), chunk...), nil, nil
	}
	output = make([]byte, 0, len(chunk))
	for _, char := range chunk {
		if len(pending) == 0 && char != 0x1b {
			output = append(output, char)
			continue
		}
		if char == 0x1b {
			output = append(output, pending...)
			pending = pending[:0]
		}
		pending = append(pending, char)
		if len(pending) == 1 ||
			(len(pending) == 2 && pending[1] == '[') ||
			(pending[1] == '[' && (char < 0x40 || char > 0x7e) && len(pending) < terminalQueryMaxBytes) {
			continue
		}
		if response, ok := terminalQueryResponse(pending); ok {
			responses = append(responses, response...)
		} else if response, ok := decPrivateModeQueryResponse(pending); ok {
			responses = append(responses, response...)
		} else {
			output = append(output, pending...)
		}
		pending = pending[:0]
	}
	return output, responses, append([]byte(nil), pending...)
}

func terminalQueryResponse(pending []byte) ([]byte, bool) {
	for _, pair := range terminalQueryResponses {
		if string(pending) == pair[0] {
			return []byte(pair[1]), true
		}
	}
	return nil, false
}

// decPrivateModeQueryResponse mirrors Rust's DECRQM handling: a well-formed
// ESC [ ? <1-10 digits> $ p query reports the requested mode as unrecognized.
func decPrivateModeQueryResponse(pending []byte) ([]byte, bool) {
	if len(pending) < 5 || pending[0] != 0x1b || pending[1] != '[' || pending[2] != '?' {
		return nil, false
	}
	if pending[len(pending)-2] != '$' || pending[len(pending)-1] != 'p' {
		return nil, false
	}
	mode := pending[3 : len(pending)-2]
	if len(mode) == 0 || len(mode) > terminalQueryMaxModeDigits {
		return nil, false
	}
	for _, digit := range mode {
		if digit < '0' || digit > '9' {
			return nil, false
		}
	}
	response := make([]byte, 0, len(mode)+6)
	response = append(response, "\x1b[?"...)
	response = append(response, mode...)
	response = append(response, ";0$y"...)
	return response, true
}

// terminalQueryReader filters PTY output and writes query responses to the
// process input. It mirrors Rust's respond_to_terminal_queries wrapper.
type terminalQueryReader struct {
	source    io.Reader
	responses io.Writer

	pending []byte
	output  []byte
	err     error

	closeOnce sync.Once
	closeErr  error
}

func newTerminalQueryReader(source io.Reader, responses io.Writer) *terminalQueryReader {
	return &terminalQueryReader{source: source, responses: responses}
}

func (r *terminalQueryReader) Read(buffer []byte) (int, error) {
	if r == nil || r.source == nil {
		return 0, io.EOF
	}
	for len(r.output) == 0 && r.err == nil {
		chunk := make([]byte, 4096)
		count, err := r.source.Read(chunk)
		if count > 0 {
			output, responses, pending := processTerminalQueries(r.pending, chunk[:count])
			r.pending = pending
			if len(responses) > 0 && r.responses != nil {
				_, _ = r.responses.Write(responses)
			}
			r.output = append(r.output, output...)
		}
		if err != nil {
			r.err = err
		}
	}
	if len(r.output) == 0 && len(r.pending) > 0 {
		r.output = append(r.output, r.pending...)
		r.pending = nil
	}
	if len(r.output) > 0 {
		count := copy(buffer, r.output)
		r.output = r.output[count:]
		return count, nil
	}
	return 0, r.err
}

func (r *terminalQueryReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if closer, ok := r.source.(io.Closer); ok {
			r.closeErr = closer.Close()
		}
	})
	return r.closeErr
}
