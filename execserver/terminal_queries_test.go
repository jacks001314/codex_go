package execserver

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// Mirrors Rust's driver-backed terminal-query test: queries split across
// chunks are answered once, removed from the output, and unrelated escape
// sequences survive.
func TestTerminalQueriesAreAnsweredAcrossChunksLikeRust(t *testing.T) {
	chunks := [][]byte{
		[]byte("before\x1b["),
		[]byte("5n\x1b[18t\x1b[6n\x1b[?1049$p\x1b[31mafter"),
	}
	var pending, output, responses []byte
	for _, chunk := range chunks {
		out, resp, rest := processTerminalQueries(pending, chunk)
		pending = rest
		output = append(output, out...)
		responses = append(responses, resp...)
	}
	if string(output) != "before\x1b[31mafter" {
		t.Fatalf("filtered output = %q, want the unrelated SGR sequence preserved", output)
	}
	if want := "\x1b[0n\x1b[8;24;80t\x1b[1;1R\x1b[?1049;0$y"; string(responses) != want {
		t.Fatalf("responses = %q, want %q", responses, want)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %q, want a complete stream", pending)
	}
}

func TestTerminalQueryReaderFlushesPendingAtEOF(t *testing.T) {
	var responses bytes.Buffer
	reader := newTerminalQueryReader(strings.NewReader("prefix\x1b[31"), &responses)
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "prefix\x1b[31" {
		t.Fatalf("output = %q, want the incomplete escape flushed at EOF", data)
	}
	if responses.Len() != 0 {
		t.Fatalf("unexpected responses = %q", responses.String())
	}
}

func TestTerminalQueryReaderWritesResponses(t *testing.T) {
	var responses bytes.Buffer
	reader := newTerminalQueryReader(strings.NewReader("a\x1b[6nb"), &responses)
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "ab" {
		t.Fatalf("output = %q, want the cursor query removed", data)
	}
	if responses.String() != "\x1b[1;1R" {
		t.Fatalf("responses = %q, want the cursor-position report", responses.String())
	}
}

// Malformed DEC private-mode queries pass through unchanged.
func TestTerminalQueryReaderPreservesMalformedModeQueries(t *testing.T) {
	var responses bytes.Buffer
	reader := newTerminalQueryReader(strings.NewReader("\x1b[?12x$p"), &responses)
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "\x1b[?12x$p" {
		t.Fatalf("output = %q, want the malformed query preserved", data)
	}
	if responses.Len() != 0 {
		t.Fatalf("unexpected responses = %q", responses.String())
	}
}
