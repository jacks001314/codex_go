package codemode

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

type EncodedFrame struct {
	Payload []byte
}

func EncodeFrame(message any) (EncodedFrame, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return EncodedFrame{}, fmt.Errorf("failed to encode code-mode IPC frame: %w", err)
	}
	if len(payload) > ProtocolMaxFrameBytes {
		return EncodedFrame{}, fmt.Errorf("code-mode IPC frame length %d exceeds %d bytes", len(payload), ProtocolMaxFrameBytes)
	}
	return EncodedFrame{Payload: payload}, nil
}

func (f *EncodedFrame) Bytes() ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("encoded frame is nil")
	}
	if len(f.Payload) > ProtocolMaxFrameBytes {
		return nil, fmt.Errorf("code-mode IPC frame length %d exceeds %d bytes", len(f.Payload), ProtocolMaxFrameBytes)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(f.Payload))); err != nil {
		return nil, err
	}
	buf.Write(f.Payload)
	return buf.Bytes(), nil
}

type FramedReader struct {
	reader io.Reader
}

func NewFramedReader(reader io.Reader) *FramedReader {
	return &FramedReader{reader: reader}
}

func (r *FramedReader) Read(target any) (bool, error) {
	if r == nil || r.reader == nil {
		return false, fmt.Errorf("framed reader is nil")
	}
	var lengthBytes [4]byte
	n, err := io.ReadFull(r.reader, lengthBytes[:])
	if err == io.EOF && n == 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	length := binary.LittleEndian.Uint32(lengthBytes[:])
	if length > ProtocolMaxFrameBytes {
		return false, fmt.Errorf("code-mode IPC frame length %d exceeds %d bytes", length, ProtocolMaxFrameBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(r.reader, payload); err != nil {
		return false, err
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return false, fmt.Errorf("failed to decode code-mode IPC frame: %w", err)
	}
	return true, nil
}

type FramedWriter struct {
	writer io.Writer
}

func NewFramedWriter(writer io.Writer) *FramedWriter {
	return &FramedWriter{writer: writer}
}

func (w *FramedWriter) Write(message any) error {
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	return w.WriteFrame(frame)
}

func (w *FramedWriter) WriteFrame(frame EncodedFrame) error {
	if w == nil || w.writer == nil {
		return fmt.Errorf("framed writer is nil")
	}
	bytes, err := (&frame).Bytes()
	if err != nil {
		return err
	}
	_, err = w.writer.Write(bytes)
	return err
}
