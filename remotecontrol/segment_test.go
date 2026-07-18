package remotecontrol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestClientSegmentReassemblerReassemblesClientMessageChunks(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	split := len(raw) / 2
	reassembler := NewClientSegmentReassembler()

	first := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split]))
	if first.Type != ClientSegmentPending {
		t.Fatalf("first observation = %+v, want pending", first)
	}
	second := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 1, 2, len(raw), raw[split:]))
	if second.Type != ClientSegmentForward || second.Envelope == nil {
		t.Fatalf("second observation = %+v, want forward", second)
	}
	if second.Envelope.Type != ClientEventClientMessage || string(second.Envelope.Message) != string(raw) || second.Envelope.SeqID == nil || *second.Envelope.SeqID != 7 {
		t.Fatalf("reassembled = %+v", second.Envelope)
	}
	if second.Envelope.StreamID == nil || *second.Envelope.StreamID != "stream-1" {
		t.Fatalf("stream = %v", second.Envelope.StreamID)
	}
}

func TestSplitServerEnvelopeForTransportSplitsLargeMessages(t *testing.T) {
	message := json.RawMessage(`{"jsonrpc":"2.0","method":"notice","params":{"summary":"` + strings.Repeat("x", RemoteControlSegmentMaxBytes) + `"}}`)
	envelope := &ServerEnvelope{
		Type:     ServerEventServerMessage,
		Message:  message,
		ClientID: "client-1",
		StreamID: "stream-1",
		SeqID:    9,
	}

	segments, err := SplitServerEnvelopeForTransport(envelope)
	if err != nil {
		t.Fatalf("SplitServerEnvelopeForTransport() error = %v", err)
	}
	if len(segments) <= 1 {
		t.Fatalf("segments len = %d, want > 1", len(segments))
	}
	for i := range segments {
		if segments[i].Type != ServerEventServerMessageChunk || segments[i].SeqID != 9 {
			t.Fatalf("segment[%d] = %+v", i, segments[i])
		}
		data, err := json.Marshal(&segments[i])
		if err != nil {
			t.Fatalf("marshal segment[%d]: %v", i, err)
		}
		if len(data) > RemoteControlSegmentMaxBytes {
			t.Fatalf("segment[%d] serialized len = %d, max %d", i, len(data), RemoteControlSegmentMaxBytes)
		}
	}
}

func TestClientSegmentReassemblerInvalidatesIncompleteStreamAssemblies(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	split := len(raw) / 2
	reassembler := NewClientSegmentReassembler()

	if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split])); got.Type != ClientSegmentPending {
		t.Fatalf("first = %+v, want pending", got)
	}
	reassembler.InvalidateStream("client-1", "stream-1")
	if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 1, 2, len(raw), raw[split:])); got.Type != ClientSegmentDropped {
		t.Fatalf("after invalidate = %+v, want dropped", got)
	}
}

func TestClientSegmentReassemblerResetsWhenStreamChanges(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	split := len(raw) / 2
	reassembler := NewClientSegmentReassembler()

	if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split])); got.Type != ClientSegmentPending {
		t.Fatalf("first = %+v, want pending", got)
	}
	if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-2", 8, 0, 2, len(raw), raw[:split])); got.Type != ClientSegmentPending {
		t.Fatalf("replacement first = %+v, want pending", got)
	}
	got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-2", 8, 1, 2, len(raw), raw[split:]))
	if got.Type != ClientSegmentForward || got.Envelope == nil || got.Envelope.StreamID == nil || *got.Envelope.StreamID != "stream-2" {
		t.Fatalf("replacement complete = %+v", got)
	}
	if stale := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 7, 1, 2, len(raw), raw[split:])); stale.Type != ClientSegmentDropped {
		t.Fatalf("stale old stream = %+v, want dropped", stale)
	}
}

func TestClientSegmentReassemblerIgnoresStaleAndDuplicateInvalidChunks(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	split := len(raw) / 2
	for _, tc := range []struct {
		name  string
		chunk *ClientEnvelope
	}{
		{
			name:  "stale seq",
			chunk: clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split]),
		},
		{
			name:  "invalid stale seq",
			chunk: clientChunkEnvelope("client-1", "stream-1", 7, 1, 2, len(raw), nil),
		},
		{
			name:  "invalid duplicate",
			chunk: clientChunkEnvelope("client-1", "stream-1", 8, 0, 2, len(raw), nil),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reassembler := NewClientSegmentReassembler()
			if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 8, 0, 2, len(raw), raw[:split])); got.Type != ClientSegmentPending {
				t.Fatalf("first = %+v, want pending", got)
			}
			if got := reassembler.Observe(tc.chunk); got.Type != ClientSegmentDropped {
				t.Fatalf("stale/duplicate = %+v, want dropped", got)
			}
			if got := reassembler.Observe(clientChunkEnvelope("client-1", "stream-1", 8, 1, 2, len(raw), raw[split:])); got.Type != ClientSegmentForward {
				t.Fatalf("completion after stale/duplicate = %+v, want forward", got)
			}
		})
	}
}

func clientChunkEnvelope(clientID string, streamID string, seqID uint64, segmentID int, segmentCount int, messageSizeBytes int, chunk []byte) *ClientEnvelope {
	encoded := base64.StdEncoding.EncodeToString(chunk)
	return &ClientEnvelope{
		Type:               ClientEventClientMessageChunk,
		SegmentID:          &segmentID,
		SegmentCount:       &segmentCount,
		MessageSizeBytes:   &messageSizeBytes,
		MessageChunkBase64: &encoded,
		ClientID:           ClientID(clientID),
		StreamID:           streamIDPtr(streamID),
		SeqID:              uint64Ptr(seqID),
	}
}
