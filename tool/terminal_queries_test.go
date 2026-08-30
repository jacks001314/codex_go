package tool

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTerminalQueryResponderRepliesToBlockingQueriesLikeRust(t *testing.T) {
	cases := []struct {
		input         string
		wantOutput    string
		wantResponses string
	}{
		{"hello\x1b[5n", "hello", "\x1b[0n"},
		{"\x1b[18t", "", "\x1b[8;24;80t"},
		{"\x1b[6n", "", "\x1b[1;1R"},
		{"\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m", ""},
		{"before\x1b[?25h\x1b[?25$p", "before\x1b[?25h", "\x1b[?25;0$y"},
	}
	for _, tt := range cases {
		var responder terminalQueryResponder
		output, responses := responder.process([]byte(tt.input))
		if string(output) != tt.wantOutput {
			t.Fatalf("process(%q) output = %q, want %q", tt.input, string(output), tt.wantOutput)
		}
		if string(responses) != tt.wantResponses {
			t.Fatalf("process(%q) responses = %q, want %q", tt.input, string(responses), tt.wantResponses)
		}
	}
}

func TestTerminalQueryResponderHandlesSplitChunksLikeRust(t *testing.T) {
	var responder terminalQueryResponder
	// A query split across two chunks is answered when its final byte arrives.
	output, responses := responder.process([]byte("\x1b[6"))
	if len(output) != 0 || len(responses) != 0 {
		t.Fatalf("partial query output=%q responses=%q, want empty", string(output), string(responses))
	}
	output, responses = responder.process([]byte("n"))
	if len(output) != 0 || string(responses) != "\x1b[1;1R" {
		t.Fatalf("completed query output=%q responses=%q", string(output), string(responses))
	}
	// A non-query ESC chunk that later resolves to text passes through.
	output, responses = responder.process([]byte("\x1b[31"))
	if len(output) != 0 || len(responses) != 0 {
		t.Fatalf("partial color output=%q responses=%q", string(output), string(responses))
	}
	output, responses = responder.process([]byte("m"))
	if string(output) != "\x1b[31m" || len(responses) != 0 {
		t.Fatalf("color output=%q responses=%q", string(output), string(responses))
	}
}

func TestTerminalQueryReaderWritesResponsesToStdinLikeRust(t *testing.T) {
	var stdin bytes.Buffer
	reader := newTerminalQueryReader(&stringReadCloser{text: "\x1b[6n"}, &stdin)
	scratch := make([]byte, 64)
	n, err := reader.Read(scratch)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("Read() = %d bytes, want 0 (query filtered)", n)
	}
	if !strings.Contains(stdin.String(), "\x1b[1;1R") {
		t.Fatalf("stdin response = %q", stdin.String())
	}
}

type stringReadCloser struct {
	text string
}

func (r *stringReadCloser) Read(p []byte) (int, error) {
	if len(r.text) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.text)
	r.text = r.text[n:]
	return n, nil
}

func (r *stringReadCloser) Close() error { return nil }
