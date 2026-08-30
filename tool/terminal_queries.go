package tool

import "io"

// terminalQuery pairs the bytes a TTY subprocess emits to query the terminal with
// the bounded response written back to its stdin (Rust #41436).
type terminalQuery struct {
	query    []byte
	response []byte
}

var terminalQueryResponses = []terminalQuery{
	// Device status report: report that the terminal is operating normally.
	{query: []byte("\x1b[5n"), response: []byte("\x1b[0n")},
	// Window-size query: report a 24-row, 80-column text area.
	{query: []byte("\x1b[18t"), response: []byte("\x1b[8;24;80t")},
	// Cursor-position report: return row 1, column 1.
	{query: []byte("\x1b[6n"), response: []byte("\x1b[1;1R")},
}

const maxTermQueryModeDigits = 10
const maxTermQueryBytes = maxTermQueryModeDigits + 5

// terminalQueryResponder answers a bounded set of blocking terminal queries
// without emulating a terminal (Rust terminal_queries.rs).
type terminalQueryResponder struct {
	pending []byte
}

// process scans bytes, returning the non-query output to pass through and the
// responses to write to the subprocess stdin. Queries split across chunks are
// buffered in the responder.
func (r *terminalQueryResponder) process(bytes []byte) (output []byte, responses []byte) {
	if len(r.pending) == 0 && !containsByte(bytes, '\x1b') {
		return bytes, nil
	}
	output = make([]byte, 0, len(bytes))
	for _, b := range bytes {
		if len(r.pending) == 0 && b != '\x1b' {
			output = append(output, b)
			continue
		}
		if b == '\x1b' {
			output = append(output, r.pending...)
			r.pending = r.pending[:0]
		}
		r.pending = append(r.pending, b)
		if len(r.pending) == 1 ||
			string(r.pending) == "\x1b[" ||
			(len(r.pending) >= 2 && r.pending[1] == '[' && (b < 0x40 || b > 0x7e) && len(r.pending) < maxTermQueryBytes) {
			continue
		}
		if resp, ok := terminalQueryResponse(r.pending); ok {
			responses = append(responses, resp...)
		} else if isDECPrivateModeQuery(r.pending) {
			// DEC private-mode query: report the requested mode as unrecognized.
			mode := r.pending[3 : len(r.pending)-2]
			responses = append(responses, '\x1b', '[', '?')
			responses = append(responses, mode...)
			responses = append(responses, ';', '0', '$', 'y')
		} else {
			output = append(output, r.pending...)
		}
		r.pending = r.pending[:0]
	}
	return output, responses
}

func terminalQueryResponse(pending []byte) ([]byte, bool) {
	for _, entry := range terminalQueryResponses {
		if string(pending) == string(entry.query) {
			return entry.response, true
		}
	}
	return nil, false
}

func isDECPrivateModeQuery(pending []byte) bool {
	if len(pending) < 5 || pending[0] != '\x1b' || pending[1] != '[' || pending[2] != '?' || pending[len(pending)-2] != '$' || pending[len(pending)-1] != 'p' {
		return false
	}
	mode := pending[3 : len(pending)-2]
	return len(mode) > 0 && len(mode) <= maxTermQueryModeDigits && allASCII(digits, mode)
}

func containsByte(bytes []byte, want byte) bool {
	for _, b := range bytes {
		if b == want {
			return true
		}
	}
	return false
}

func allASCII(predicate func(byte) bool, bytes []byte) bool {
	for _, b := range bytes {
		if !predicate(b) {
			return false
		}
	}
	return true
}

func digits(b byte) bool {
	return b >= '0' && b <= '9'
}

// terminalQueryReader wraps a TTY reader, writes terminal-query responses to the
// subprocess stdin, and passes through all non-query output.
type terminalQueryReader struct {
	reader    io.ReadCloser
	stdin     io.Writer
	responder terminalQueryResponder
}

func newTerminalQueryReader(reader io.ReadCloser, stdin io.Writer) io.ReadCloser {
	return &terminalQueryReader{reader: reader, stdin: stdin}
}

func (r *terminalQueryReader) Read(p []byte) (int, error) {
	scratch := make([]byte, len(p))
	n, err := r.reader.Read(scratch)
	if n == 0 {
		return 0, err
	}
	filtered, responses := r.responder.process(scratch[:n])
	if len(responses) > 0 {
		_, _ = r.stdin.Write(responses)
	}
	copy(p, filtered)
	return len(filtered), err
}

func (r *terminalQueryReader) Close() error {
	return r.reader.Close()
}
