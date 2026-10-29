package model

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestSSEParserLargeMultiFragmentData covers a data line that spans several
// reads of the 64 KiB buffer: the payload must arrive intact and must stay
// valid after the parser moves on to the next event, because the parser hands
// its buffer to the event instead of copying it.
func TestSSEParserLargeMultiFragmentData(t *testing.T) {
	first := strings.Repeat("a", 300<<10)
	second := strings.Repeat("b", 200<<10)
	stream := "data: " + first + "\n\ndata: " + second + "\n\n"
	parser := newResponsesSSEParser(strings.NewReader(stream))

	event, err := parser.Next()
	if err != nil {
		t.Fatalf("first event: %v", err)
	}
	if string(event.Data) != first {
		t.Fatalf("first data malformed: len=%d want=%d", len(event.Data), len(first))
	}
	firstData := event.Data

	event, err = parser.Next()
	if err != nil {
		t.Fatalf("second event: %v", err)
	}
	if string(event.Data) != second {
		t.Fatalf("second data malformed: len=%d want=%d", len(event.Data), len(second))
	}
	if !bytes.Equal(firstData, []byte(first)) {
		t.Fatal("first event data was mutated by the next event")
	}
	if _, err := parser.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

// TestSSEParserEventNameAndComments covers the field handling that does not go
// through the data path.
func TestSSEParserEventNameAndComments(t *testing.T) {
	stream := ": ping\nevent: response.completed\ndata: {}\n\n"
	parser := newResponsesSSEParser(strings.NewReader(stream))
	event, err := parser.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if event.Event != "response.completed" || string(event.Data) != "{}" {
		t.Fatalf("event=%q data=%q", event.Event, event.Data)
	}
}

// TestSSEParserEventTooLarge pins the guard: an event over the configured limit
// is a diagnosable error, not an unbounded allocation.
func TestSSEParserEventTooLarge(t *testing.T) {
	parser := newResponsesSSEParser(strings.NewReader("data: " + strings.Repeat("a", 4096) + "\n\n"))
	parser.maxEventBytes = 1024
	if _, err := parser.Next(); !errors.Is(err, errSSEEventTooLarge) {
		t.Fatalf("want errSSEEventTooLarge, got %v", err)
	}
}
