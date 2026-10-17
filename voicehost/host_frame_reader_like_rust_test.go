package voicehost

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// Mirrors Rust #42404: pipe chunks may split a frame or coalesce several
// frames; the stateful reader decodes complete frames independently.
func TestVoiceHostFrameReaderHandlesSplitAndCoalescedFramesLikeRust(t *testing.T) {
	readerPipe, writerPipe := io.Pipe()
	reader := newVoiceHostFrameReader(readerPipe)
	hello, err := EncodeFrame(NewHello(1, "commit"))
	if err != nil {
		t.Fatal(err)
	}
	ready, err := EncodeFrame(Message{Type: TypeReady})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = writerPipe.Write(hello[:len(hello)/2])
		_, _ = writerPipe.Write(hello[len(hello)/2:])
		_, _ = writerPipe.Write(append(append([]byte(nil), ready...), ready...))
		_ = writerPipe.Close()
	}()
	want := []MessageType{TypeHello, TypeReady, TypeReady}
	for _, wantType := range want {
		result := <-reader.frames
		if result.err != nil || result.message == nil || result.message.Type != wantType {
			t.Fatalf("frame = %#v, want type %q", result, wantType)
		}
	}
}

// Mirrors Rust #42404: a frame that arrives after a timed-out read stays
// buffered for the next exchange instead of being lost with the abandoned read.
func TestVoiceHostRequestRetainsLateFrameLikeRust(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinWriter.Close()
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, stdinReader) }()
	host := &VoiceHost{stdin: stdinWriter, stdout: stdoutReader}
	host.reader = newVoiceHostFrameReader(stdoutReader)

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelFirst()
	if _, err := host.request(firstCtx, NewHello(1, "commit")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first request error = %v, want deadline exceeded", err)
	}
	ready, err := EncodeFrame(Message{Type: TypeReady})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = stdoutWriter.Write(ready) }()

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	response, err := host.request(secondCtx, Message{Type: TypeClose})
	if err != nil {
		t.Fatalf("second request error = %v", err)
	}
	if response.Type != TypeReady {
		t.Fatalf("second response = %q, want the late ready frame", response.Type)
	}
}
