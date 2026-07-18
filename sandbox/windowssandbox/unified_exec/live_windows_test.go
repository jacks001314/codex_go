//go:build windows

package unified_exec

import (
	"bytes"
	"io"
	"testing"

	"codex_go/sandbox/windowssandbox/elevated"
)

type recordingWriteCloser struct {
	bytes.Buffer
}

func (w *recordingWriteCloser) Close() error { return nil }

func TestWindowsTTYWriteCloserMatchesRustInputNormalization(t *testing.T) {
	inner := &recordingWriteCloser{}
	writer := &windowsTTYWriteCloser{inner: inner}
	if _, err := writer.Write([]byte("line\nnext\r")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := writer.Write([]byte("\nX\b\n")); err != nil {
		t.Fatalf("Write(split) error = %v", err)
	}
	if got := inner.String(); got != "line\rnext\rX\x7f\r" {
		t.Fatalf("normalized input = %q", got)
	}
}

func TestElevatedFrameWriterNormalizesTTYInputBeforeFraming(t *testing.T) {
	var framed bytes.Buffer
	writer := &elevatedFrameWriter{writer: &framed, tty: true}
	if _, err := writer.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	message, err := elevated.ReadFrame(bytes.NewReader(framed.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if message == nil || message.Message.Stdin == nil {
		t.Fatalf("message = %#v", message)
	}
	data, err := elevated.DecodeBytes(message.Message.Stdin.DataB64)
	if err != nil || string(data) != "hello\r" {
		t.Fatalf("stdin payload = %q, %v", data, err)
	}
}

var _ io.WriteCloser = (*windowsTTYWriteCloser)(nil)
