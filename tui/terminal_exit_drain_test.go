package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// Rust #57391: a DA1 reply is `ESC [ ? digits/semicolons c`. Terminal reports
// and keyboard-enhancement replies may precede it and must not be mistaken for
// the reply.
func TestFindPrimaryDeviceAttributesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		buffer string
		want   bool
	}{
		{name: "standard reply", buffer: "\x1b[?1;2c", want: true},
		{name: "empty payload", buffer: "\x1b[?c", want: false},
		{name: "unterminated", buffer: "\x1b[?1;2", want: false},
		{name: "letters in payload", buffer: "\x1b[?1;xc", want: false},
		{name: "after a mouse report", buffer: "\x1b[<64;1;1M\x1b[?62;22c", want: true},
		{name: "after keyboard flags", buffer: "\x1b[?1u\x1b[?1;2c", want: true},
		{name: "split reply", buffer: "\x1b[?1;", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := FindPrimaryDeviceAttributes([]byte(testCase.buffer)); got != testCase.want {
				t.Fatalf("FindPrimaryDeviceAttributes(%q) = %v, want %v", testCase.buffer, got, testCase.want)
			}
		})
	}
}

// Rust #57391: `wait_for_exit_reports` writes DA1 and discards everything the
// terminal queued until the reply proves the reports arrived.
func TestReadTerminalReportsUntilDA1LikeRust(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	var output bytes.Buffer
	result := make(chan bool, 1)
	go func() {
		result <- readTerminalReportsUntilDA1(reader, &output, time.Second)
	}()
	report := "\x1b[<64;10;5M\x1b[<65;10;5M\x1b[?1;2c"
	if _, err := writer.Write([]byte(report)); err != nil {
		t.Fatalf("write reports: %v", err)
	}
	select {
	case drained := <-result:
		if !drained {
			t.Fatal("drain did not recognize the DA1 reply")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not return after the DA1 reply")
	}
	if got := output.String(); got != primaryDeviceAttributesQuery {
		t.Fatalf("drain wrote %q, want %q", got, primaryDeviceAttributesQuery)
	}
}

// wouldBlockReader stands in for the non-blocking terminal duplicate: an empty
// queue reports a temporary error instead of blocking.
type wouldBlockReader struct{}

func (wouldBlockReader) Read([]byte) (int, error) { return 0, errors.New("would block") }

// Rust #57391: a terminal that never answers DA1 must still bound the wait.
func TestReadTerminalReportsUntilDA1TimesOutLikeRust(t *testing.T) {
	var output bytes.Buffer
	started := time.Now()
	if readTerminalReportsUntilDA1(wouldBlockReader{}, &output, 40*time.Millisecond) {
		t.Fatal("drain reported success without a DA1 reply")
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("drain waited %v, want the bounded deadline", elapsed)
	}
	if got := output.String(); got != primaryDeviceAttributesQuery {
		t.Fatalf("drain wrote %q, want the DA1 query", got)
	}
}

// Rust #57391: non-terminal input (pipes, buffers, embedders) never writes a
// probe or blocks the shutdown.
func TestDrainPendingTerminalReportsSkipsNonTerminalInputLikeRust(t *testing.T) {
	var output bytes.Buffer
	started := time.Now()
	if DrainPendingTerminalReports(bytes.NewReader(nil), &output, true) {
		t.Fatal("drain reported success for non-terminal input")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("drain took %v for non-terminal input", elapsed)
	}
	if strings.Contains(output.String(), primaryDeviceAttributesQuery) {
		t.Fatalf("drain wrote a DA1 query to a non-terminal: %q", output.String())
	}
}
