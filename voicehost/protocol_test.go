package voicehost

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProtocolRoundTrip(t *testing.T) {
	sdp, err := NewSessionDescription("v=0\r\no=offer\r\n")
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{
		NewHello(1, "build-commit"),
		NewSimpleMessage(TypeReady),
		NewSimpleMessage(TypeInitializeRuntime),
		NewSimpleMessage(TypeRuntimeReady),
		NewSimpleMessage(TypeStartTransport),
		NewSDPMessage(TypeOffer, sdp),
		NewSDPMessage(TypeApplyAnswer, sdp),
		NewSimpleMessage(TypeTransportReady),
		NewSimpleMessage(TypeClose),
		NewSimpleMessage(TypeClosed),
	}
	for _, want := range messages {
		frame, err := EncodeFrame(want)
		if err != nil {
			t.Fatalf("encode %s: %v", want.Type, err)
		}
		got, err := DecodeFrame(frame)
		if err != nil {
			t.Fatalf("decode %s: %v", want.Type, err)
		}
		if got.Type != want.Type {
			t.Fatalf("type = %q, want %q", got.Type, want.Type)
		}
		if want.SDP != nil {
			if got.SDP == nil || got.SDP.SDP() != want.SDP.SDP() {
				t.Fatalf("sdp = %#v, want %#v", got.SDP, want.SDP)
			}
		} else if got.SDP != nil {
			t.Fatalf("%s unexpectedly carries SDP", got.Type)
		}
	}
}

func TestProtocolReadMessageAtEOF(t *testing.T) {
	message, err := ReadMessage(bytes.NewReader(nil))
	if err != nil || message != nil {
		t.Fatalf("clean EOF = %#v, %v", message, err)
	}
	message, err = ReadMessage(bytes.NewReader([]byte{0}))
	if err == nil || message != nil {
		t.Fatalf("partial first byte = %#v, %v; want an error", message, err)
	}
}

func TestProtocolRejectsUnknownFieldsAndVariants(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr error
	}{
		{name: "unknown field", payload: `{"type":"ready","extra":1}`, wantErr: ErrInvalidMessage},
		{name: "missing type", payload: `{}`, wantErr: ErrInvalidMessage},
		{name: "hello missing protocol", payload: `{"type":"hello","buildCommit":"x"}`, wantErr: ErrInvalidMessage},
		{name: "hello bad protocol", payload: `{"type":"hello","protocol":2,"buildCommit":"x"}`, wantErr: ErrInvalidMessage},
		{name: "offer missing sdp", payload: `{"type":"offer"}`, wantErr: ErrInvalidMessage},
		{name: "empty sdp", payload: `{"type":"offer","sdp":""}`, wantErr: ErrInvalidSessionDescription},
		{name: "unknown type", payload: `{"type":"unknown"}`, wantErr: ErrInvalidMessage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var message Message
			err := json.Unmarshal([]byte(test.payload), &message)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestProtocolRejectsOversizedFrame(t *testing.T) {
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, uint32(MaxFrameBytes+1))
	if _, err := DecodeFrame(frame); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("oversized frame error = %v", err)
	}

	var message Message
	if err := json.Unmarshal([]byte(`{"type":"ready","pad":"`+strings.Repeat("x", MaxFrameBytes)+`"}`), &message); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestSessionDescriptionIsRedacted(t *testing.T) {
	sdp, err := NewSessionDescription("v=0 secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sdp.String(), "secret") || strings.Contains(sdp.GoString(), "secret") {
		t.Fatal("SDP leaked through diagnostic representation")
	}
	if sdp.SDP() != "v=0 secret" {
		t.Fatalf("SDP accessor = %q", sdp.SDP())
	}
}

func TestWriteMessageHandlesShortWriter(t *testing.T) {
	message := NewSimpleMessage(TypeReady)
	if err := WriteMessage(&oneByteWriter{}, message); err != nil {
		t.Fatalf("write with short writer: %v", err)
	}
}

type oneByteWriter struct{}

func (*oneByteWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return 1, nil
}

func TestReadMessageRejectsTruncatedPayload(t *testing.T) {
	frame, err := EncodeFrame(NewSimpleMessage(TypeReady))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadMessage(bytes.NewReader(frame[:len(frame)-1]))
	if err == nil {
		t.Fatal("truncated frame was accepted")
	}
	_, err = ReadMessage(bytes.NewReader([]byte{0, 0, 0, 1}))
	if err == nil {
		t.Fatal("missing payload was accepted")
	}
}

func TestReadMessageAtEOFAfterFrames(t *testing.T) {
	frame, err := EncodeFrame(NewSimpleMessage(TypeReady))
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(frame)
	message, err := ReadMessage(reader)
	if err != nil || message == nil || message.Type != TypeReady {
		t.Fatalf("message = %#v, %v", message, err)
	}
	message, err = ReadMessage(reader)
	if err != nil || message != nil {
		t.Fatalf("trailing EOF = %#v, %v", message, err)
	}
}

func TestRuntimeEnvironmentIsDeterministic(t *testing.T) {
	first := RuntimeEnvironment()
	second := RuntimeEnvironment()
	if len(first) != 7 || len(second) != 7 {
		t.Fatalf("runtime environment lengths = %d, %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("runtime environment mismatch at %d: %#v vs %#v", i, first[i], second[i])
		}
	}
}
