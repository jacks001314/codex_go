package provisionservice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	json "github.com/goccy/go-json"
)

// WriteFrame writes one length-prefixed JSON provisioning frame.
func WriteFrame(w io.Writer, frame *Frame) error {
	if w == nil {
		return errors.New("writer is nil")
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameLen {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(payload)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// ReadFrame reads one length-prefixed JSON provisioning frame. A clean EOF at
// the length prefix yields (nil, nil), matching Rust read_provisioning_frame.
func ReadFrame(r io.Reader) (*Frame, error) {
	if r == nil {
		return nil, errors.New("reader is nil")
	}
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil
		}
		return nil, err
	}
	frameLen := binary.LittleEndian.Uint32(length[:])
	if frameLen > MaxFrameLen {
		return nil, fmt.Errorf("frame too large: %d", frameLen)
	}
	payload := make([]byte, frameLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	var frame Frame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return nil, err
	}
	return &frame, nil
}
