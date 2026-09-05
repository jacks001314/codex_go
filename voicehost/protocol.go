package voicehost

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
)

// MaxFrameBytes is the largest control frame accepted by the voice host. It
// matches the Rust voice helper's frame bound and never carries audio.
const MaxFrameBytes = 128 * 1024

// maxSessionDescriptionBytes bounds SDP, which contains ICE credentials. The
// value is kept below the frame limit so an SDP-bearing frame can never exceed
// MaxFrameBytes.
const maxSessionDescriptionBytes = 64 * 1024

// MessageType identifies a control message exchanged over the inherited stdio
// pipe. Ordering and validation mirror codex-voice-host's protocol.
type MessageType string

const (
	TypeHello             MessageType = "hello"
	TypeReady             MessageType = "ready"
	TypeInitializeRuntime MessageType = "initializeRuntime"
	TypeRuntimeReady      MessageType = "runtimeReady"
	TypeStartTransport    MessageType = "startTransport"
	TypeOffer             MessageType = "offer"
	TypeApplyAnswer       MessageType = "applyAnswer"
	TypeTransportReady    MessageType = "transportReady"
	TypeClose             MessageType = "close"
	TypeClosed            MessageType = "closed"
)

var (
	// ErrInvalidMessage indicates a malformed or out-of-order control message.
	ErrInvalidMessage = errors.New("invalid voice host message")
	// ErrInvalidSessionDescription indicates an empty or oversized SDP value.
	ErrInvalidSessionDescription = errors.New("invalid voice session description length")
)

// SessionDescription is an opaque SDP value. Its string representation is
// redacted because SDP contains ICE credentials.
type SessionDescription struct {
	sdp string
}

// NewSessionDescription validates and wraps an SDP string.
func NewSessionDescription(sdp string) (SessionDescription, error) {
	if sdp == "" || len(sdp) > maxSessionDescriptionBytes {
		return SessionDescription{}, ErrInvalidSessionDescription
	}
	return SessionDescription{sdp: sdp}, nil
}

// SDP returns the wrapped SDP value.
func (s SessionDescription) SDP() string {
	return s.sdp
}

// String returns a redacted representation so credentials never appear in
// diagnostics or logs.
func (s SessionDescription) String() string {
	return "SessionDescription([REDACTED])"
}

// GoString returns the same redacted representation used by String.
func (s SessionDescription) GoString() string {
	return s.String()
}

// MarshalJSON encodes the SDP as a normal JSON string.
func (s SessionDescription) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.sdp)
}

// UnmarshalJSON validates SDP length while decoding.
func (s *SessionDescription) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value == "" || len(value) > maxSessionDescriptionBytes {
		return ErrInvalidSessionDescription
	}
	s.sdp = value
	return nil
}

// Message is a bounded same-build control message. Unknown fields and invalid
// sequences fail closed without echoing input.
type Message struct {
	Type        MessageType         `json:"type"`
	Protocol    *uint32             `json:"protocol,omitempty"`
	BuildCommit string              `json:"buildCommit,omitempty"`
	SDP         *SessionDescription `json:"sdp,omitempty"`
}

// NewHello returns a hello message carrying the protocol version and exact
// build commit.
func NewHello(protocol uint32, buildCommit string) Message {
	return Message{Type: TypeHello, Protocol: &protocol, BuildCommit: buildCommit}
}

// NewSimpleMessage returns a message with no fields.
func NewSimpleMessage(messageType MessageType) Message {
	return Message{Type: messageType}
}

// NewSDPMessage returns a message carrying SDP.
func NewSDPMessage(messageType MessageType, sdp SessionDescription) Message {
	return Message{Type: messageType, SDP: &sdp}
}

// UnmarshalJSON rejects unknown fields and validates each message variant.
func (m *Message) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("%w: message is required", ErrInvalidMessage)
	}
	for key := range fields {
		switch key {
		case "type", "protocol", "buildCommit", "sdp":
		default:
			return fmt.Errorf("%w: unknown field %q", ErrInvalidMessage, key)
		}
	}
	typeRaw, ok := fields["type"]
	if !ok {
		return fmt.Errorf("%w: missing field type", ErrInvalidMessage)
	}
	var messageType MessageType
	if err := json.Unmarshal(typeRaw, &messageType); err != nil {
		return err
	}
	message := Message{Type: messageType}
	switch messageType {
	case TypeHello:
		var wire struct {
			Protocol    *uint32 `json:"protocol"`
			BuildCommit string  `json:"buildCommit"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			return err
		}
		if wire.Protocol == nil {
			return fmt.Errorf("%w: missing field protocol", ErrInvalidMessage)
		}
		message.Protocol = wire.Protocol
		message.BuildCommit = wire.BuildCommit
	case TypeOffer, TypeApplyAnswer:
		var wire struct {
			SDP *SessionDescription `json:"sdp"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			return err
		}
		if wire.SDP == nil {
			return fmt.Errorf("%w: missing field sdp", ErrInvalidMessage)
		}
		message.SDP = wire.SDP
	case TypeReady, TypeInitializeRuntime, TypeRuntimeReady, TypeStartTransport, TypeTransportReady, TypeClose, TypeClosed:
	default:
		return fmt.Errorf("%w: unknown message type %q", ErrInvalidMessage, messageType)
	}
	if err := message.Validate(); err != nil {
		return err
	}
	*m = message
	return nil
}

// Validate enforces per-variant field requirements.
func (m Message) Validate() error {
	switch m.Type {
	case TypeHello:
		if m.Protocol == nil {
			return fmt.Errorf("%w: protocol is required", ErrInvalidMessage)
		}
		if *m.Protocol != 1 {
			return fmt.Errorf("%w: unsupported protocol %d", ErrInvalidMessage, *m.Protocol)
		}
		if m.BuildCommit == "" {
			return fmt.Errorf("%w: buildCommit is required", ErrInvalidMessage)
		}
	case TypeOffer, TypeApplyAnswer:
		if m.SDP == nil || m.SDP.sdp == "" {
			return fmt.Errorf("%w: sdp is required", ErrInvalidMessage)
		}
	case TypeReady, TypeInitializeRuntime, TypeRuntimeReady, TypeStartTransport, TypeTransportReady, TypeClose, TypeClosed:
	default:
		return fmt.Errorf("%w: unknown message type %q", ErrInvalidMessage, m.Type)
	}
	return nil
}

// MarshalJSON emits the wire shape used by the Rust protocol.
func (m Message) MarshalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	switch m.Type {
	case TypeHello:
		return json.Marshal(struct {
			Type        MessageType `json:"type"`
			Protocol    uint32      `json:"protocol"`
			BuildCommit string      `json:"buildCommit"`
		}{Type: m.Type, Protocol: *m.Protocol, BuildCommit: m.BuildCommit})
	case TypeOffer, TypeApplyAnswer:
		return json.Marshal(struct {
			Type MessageType         `json:"type"`
			SDP  *SessionDescription `json:"sdp"`
		}{Type: m.Type, SDP: m.SDP})
	default:
		return json.Marshal(struct {
			Type MessageType `json:"type"`
		}{Type: m.Type})
	}
}

// RuntimeEnvironment returns fixed child settings that prevent native audio
// initialization from scanning system plugins or caches.
func RuntimeEnvironment() [][2]string {
	registry := "/dev/null"
	if runtime.GOOS == "windows" {
		registry = "NUL"
	}
	return [][2]string{
		{"GST_PLUGIN_PATH", ""},
		{"GST_PLUGIN_PATH_1_0", ""},
		{"GST_PLUGIN_SYSTEM_PATH", ""},
		{"GST_PLUGIN_SYSTEM_PATH_1_0", ""},
		{"GST_REGISTRY", registry},
		{"GST_REGISTRY_UPDATE", "no"},
		{"GST_REGISTRY_FORK", "no"},
	}
}

// EncodeFrame encodes a control message as a big-endian u32 length followed by
// JSON. Audio never crosses this pipe.
func EncodeFrame(message Message) ([]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxFrameBytes {
		return nil, fmt.Errorf("voice frame exceeds %d bytes", MaxFrameBytes)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

// DecodeFrame decodes one complete frame. It rejects truncated, oversized, and
// unknown-field payloads.
func DecodeFrame(frame []byte) (Message, error) {
	if len(frame) < 4 {
		return Message{}, fmt.Errorf("%w: truncated frame header", ErrInvalidMessage)
	}
	length := binary.BigEndian.Uint32(frame[:4])
	if int(length) > MaxFrameBytes {
		return Message{}, fmt.Errorf("%w: frame length %d exceeds limit", ErrInvalidMessage, length)
	}
	if len(frame) != int(length)+4 {
		return Message{}, fmt.Errorf("%w: frame length does not match payload", ErrInvalidMessage)
	}
	var message Message
	if err := json.Unmarshal(frame[4:], &message); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	return message, nil
}

// WriteMessage encodes and writes one frame. The returned error is always a
// complete write or a validation failure.
func WriteMessage(writer io.Writer, message Message) error {
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	for len(frame) > 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

// ReadMessage reads one framed message from reader. It returns (nil, nil) when
// the stream reaches EOF at a frame boundary, matching the Rust helper's
// Option return value.
func ReadMessage(reader io.Reader) (*Message, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:1]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := io.ReadFull(reader, header[1:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if int(length) > MaxFrameBytes {
		return nil, fmt.Errorf("%w: frame length %d exceeds limit", ErrInvalidMessage, length)
	}
	frame := make([]byte, 4+int(length))
	copy(frame[:4], header[:])
	if _, err := io.ReadFull(reader, frame[4:]); err != nil {
		return nil, err
	}
	message, err := DecodeFrame(frame)
	if err != nil {
		return nil, err
	}
	return &message, nil
}
