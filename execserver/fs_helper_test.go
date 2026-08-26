package execserver

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedBufferRetainsBoundPrefix(t *testing.T) {
	var buf boundedBuffer
	// Writing more than the cap must not grow the retained prefix.
	for i := 0; i < 4; i++ {
		if _, err := buf.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); len(got) > maxFSHelperStderrBytes {
		t.Fatalf("bounded buffer len = %d, want <= %d", len(got), maxFSHelperStderrBytes)
	}
	if !strings.HasPrefix(buf.String(), "0123456789") {
		t.Fatalf("bounded buffer dropped leading stderr: %q", buf.String())
	}
}

func TestReadHelperResponseReadsNewlineDelimited(t *testing.T) {
	line, err := readHelperResponse(strings.NewReader(`{"status":"ok"}` + "\n"))
	if err != nil {
		t.Fatalf("readHelperResponse() error = %v", err)
	}
	if got := string(line); got != `{"status":"ok"}` {
		t.Fatalf("line = %q", got)
	}

	// A helper that closes stdout without responding must surface an error.
	if _, err := readHelperResponse(bytes.NewReader(nil)); err == nil {
		t.Fatal("expected error for closed stdout without a response")
	}
}
