package conpty

import (
	"bytes"
	"strings"
	"testing"
)

// Ports Rust's `windows_input_tests::normalizes_terminal_input_without_changing_text_or_ctrl_c`
// (codex-rs/utils/pty/src/windows_input_tests.rs) so the pseudoconsole input
// contract cannot drift from the Rust side.
func TestInputNormalizerNormalizesTerminalInputWithoutChangingTextOrCtrlC(t *testing.T) {
	var normalizer InputNormalizer
	cases := []struct {
		input string
		want  string
	}{
		{"first\n", "first\r"},
		{"second\r", "second\r"},
		{"\nthird\r\n", "third\r"},
	}
	for _, testCase := range cases {
		if got := string(normalizer.Normalize([]byte(testCase.input))); got != testCase.want {
			t.Fatalf("Normalize(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
	controls := "cafeé 漢字\x08\x03"
	if got, want := string(normalizer.Normalize([]byte(controls))), "cafeé 漢字\x7f\x03"; got != want {
		t.Fatalf("Normalize(controls) = %q, want %q", got, want)
	}
}

// A CRLF split across two writes must still submit a single line: the second
// write sees the previous carriage return and drops its line feed.
func TestInputNormalizerCollapsesSplitCRLF(t *testing.T) {
	var normalizer InputNormalizer
	first := normalizer.Normalize([]byte("text\r"))
	second := normalizer.Normalize([]byte("\n"))
	if got := string(first) + string(second); got != "text\r" {
		t.Fatalf("split CRLF normalized to %q, want %q", got, "text\r")
	}
}

// The write closer reports the caller's byte count while writing the normalized
// form, and passes UTF-8 through untouched.
func TestNewInputWriteCloserWritesNormalizedBytes(t *testing.T) {
	var sink bytes.Buffer
	writer := NewInputWriteCloser(nopWriteCloser{&sink})
	written, err := writer.Write([]byte("echo 漢字\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len("echo 漢字\n") {
		t.Fatalf("Write() = %d, want %d", written, len("echo 漢字\n"))
	}
	if got, want := sink.String(), "echo 漢字\r"; got != want {
		t.Fatalf("sink = %q, want %q", got, want)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := writer.Write([]byte("x")); err == nil {
		t.Fatal("Write() after Close() succeeded")
	}
	if !strings.Contains(sink.String(), "漢字") {
		t.Fatalf("sink lost multi-byte text: %q", sink.String())
	}
}

type nopWriteCloser struct {
	sink *bytes.Buffer
}

func (w nopWriteCloser) Write(data []byte) (int, error) { return w.sink.Write(data) }
func (w nopWriteCloser) Close() error                   { return nil }
