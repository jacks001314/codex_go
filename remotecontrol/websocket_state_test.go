package remotecontrol

import "testing"

func TestServerEnvelopeSequencerAssignsContiguousSeqIDsPerStream(t *testing.T) {
	seq := NewServerEnvelopeSequencer()
	clientID := ClientID("client-1")
	first := StreamID("stream-1")
	second := StreamID("stream-2")
	if got := seq.NextSeqID(clientID, first); got != 1 {
		t.Fatalf("first stream first seq = %d, want 1", got)
	}
	if got := seq.NextSeqID(clientID, second); got != 1 {
		t.Fatalf("second stream first seq = %d, want 1", got)
	}
	if got := seq.NextSeqID(clientID, first); got != 2 {
		t.Fatalf("first stream second seq = %d, want 2", got)
	}
}

func TestServerEnvelopeBufferAcksByStreamID(t *testing.T) {
	buffer := NewServerEnvelopeBuffer()
	clientID := ClientID("client-1")
	streamID := StreamID("stream-1")
	otherStreamID := StreamID("stream-2")
	buffer.Insert(testPongEnvelope(clientID, streamID, 1))
	buffer.Insert(testPongEnvelope(clientID, streamID, 2))
	buffer.Insert(testPongEnvelope(clientID, streamID, 3))
	buffer.Insert(testPongEnvelope(clientID, otherStreamID, 1))

	buffer.Ack(clientID, streamID, 2, nil)
	got := buffer.ForStream(clientID, streamID)
	if len(got) != 1 || got[0].SeqID != 3 {
		t.Fatalf("acked stream envelopes = %+v, want seq 3", got)
	}
	other := buffer.ForStream(clientID, otherStreamID)
	if len(other) != 1 || other[0].SeqID != 1 {
		t.Fatalf("other stream envelopes = %+v, want untouched", other)
	}
}

func TestServerEnvelopeBufferAcksChunkSegments(t *testing.T) {
	buffer := NewServerEnvelopeBuffer()
	clientID := ClientID("client-1")
	streamID := StreamID("stream-1")
	buffer.Insert(testChunkEnvelope(clientID, streamID, 1, 0))
	buffer.Insert(testChunkEnvelope(clientID, streamID, 1, 1))
	buffer.Insert(testPongEnvelope(clientID, streamID, 1))
	buffer.Insert(testChunkEnvelope(clientID, streamID, 2, 0))
	ackedSegment := 0

	buffer.Ack(clientID, streamID, 1, &ackedSegment)
	got := buffer.ForStream(clientID, streamID)
	if len(got) != 3 {
		t.Fatalf("remaining envelopes = %+v, want 3", got)
	}
	if got[0].SeqID != 1 || got[0].SegmentID == nil || *got[0].SegmentID != 1 {
		t.Fatalf("first remaining = %+v, want seq 1 segment 1", got[0])
	}
	if got[1].SeqID != 1 || got[1].Type != ServerEventPong {
		t.Fatalf("second remaining = %+v, want seq 1 non-chunk retained", got[1])
	}
	if got[2].SeqID != 2 {
		t.Fatalf("third remaining = %+v, want seq 2", got[2])
	}
}

func TestWebsocketStateObservesClientChunksAndDedupesAfterDelivery(t *testing.T) {
	state := NewWebsocketState()
	raw := []byte(`{"jsonrpc":"2.0","method":"initialized"}`)
	split := len(raw) / 2

	first := clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split])
	if got := state.ObserveClientMessage(first, len(*first.MessageChunkBase64)); got.Type != ClientSegmentPending {
		t.Fatalf("first observation = %+v, want pending", got)
	}
	second := clientChunkEnvelope("client-1", "stream-1", 7, 1, 2, len(raw), raw[split:])
	got := state.ObserveClientMessage(second, len(raw[split:]))
	if got.Type != ClientSegmentForward || got.Envelope == nil {
		t.Fatalf("second observation = %+v, want forward", got)
	}
	state.RecordClientMessageDelivery(got.Envelope)

	duplicate := state.ObserveClientMessage(clientChunkEnvelope("client-1", "stream-1", 7, 0, 2, len(raw), raw[:split]), len(raw[:split]))
	if duplicate.Type != ClientSegmentDropped {
		t.Fatalf("duplicate observation = %+v, want dropped", duplicate)
	}
}

func TestWebsocketStateRecordDeliveryUpdatesCursorAndAcksOutbound(t *testing.T) {
	state := NewWebsocketState()
	clientID := ClientID("client-1")
	streamID := StreamID("stream-1")
	state.QueueServerEnvelope(testPongEnvelope(clientID, streamID, 1))
	state.QueueServerEnvelope(testPongEnvelope(clientID, streamID, 2))
	cursor := "cursor-2"
	seqID := uint64(1)
	ack := &ClientEnvelope{
		Type:      ClientEventAck,
		SegmentID: nil,
		ClientID:  clientID,
		StreamID:  &streamID,
		SeqID:     &seqID,
		Cursor:    &cursor,
	}

	state.RecordClientMessageDelivery(ack)
	if state.SubscribeCursor == nil || *state.SubscribeCursor != cursor {
		t.Fatalf("cursor = %v, want %q", state.SubscribeCursor, cursor)
	}
	got := state.OutboundBuffer.ForStream(clientID, streamID)
	if len(got) != 1 || got[0].SeqID != 2 {
		t.Fatalf("remaining outbound = %+v, want seq 2", got)
	}
}

func TestWebsocketStatePlainClientMessagesAreNotDedupedByState(t *testing.T) {
	state := NewWebsocketState()
	message := clientMessageEnvelope("client-1", "stream-1", uint64Ptr(7), `{"jsonrpc":"2.0","method":"initialized"}`)
	state.RecordClientMessageDelivery(message)
	if got := state.ObserveClientMessage(message, len(message.Message)); got.Type != ClientSegmentForward {
		t.Fatalf("plain message observation = %+v, want forward", got)
	}
}

func TestWebsocketStateInvalidateStreamClearsDeliveredChunkSeq(t *testing.T) {
	state := NewWebsocketState()
	chunk := clientChunkEnvelope("client-1", "stream-1", 7, 0, 1, len(`{"jsonrpc":"2.0","method":"initialized"}`), []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	got := state.ObserveClientMessage(chunk, len(*chunk.MessageChunkBase64))
	if got.Type != ClientSegmentForward || got.Envelope == nil {
		t.Fatalf("chunk observation = %+v, want forward", got)
	}
	state.RecordClientMessageDelivery(got.Envelope)
	if duplicate := state.ObserveClientMessage(chunk, len(*chunk.MessageChunkBase64)); duplicate.Type != ClientSegmentDropped {
		t.Fatalf("before invalidate = %+v, want dropped", duplicate)
	}
	state.InvalidateClientMessageStream("client-1", "stream-1")
	if got := state.ObserveClientMessage(chunk, len(*chunk.MessageChunkBase64)); got.Type != ClientSegmentForward {
		t.Fatalf("after invalidate = %+v, want forward", got)
	}
}

func testPongEnvelope(clientID ClientID, streamID StreamID, seqID uint64) *ServerEnvelope {
	status := PongStatusActive
	return &ServerEnvelope{
		Type:     ServerEventPong,
		Status:   &status,
		ClientID: clientID,
		StreamID: streamID,
		SeqID:    seqID,
	}
}

func testChunkEnvelope(clientID ClientID, streamID StreamID, seqID uint64, segmentID int) *ServerEnvelope {
	segmentCount := 2
	messageSize := 12
	chunk := "Y2h1bms="
	return &ServerEnvelope{
		Type:               ServerEventServerMessageChunk,
		SegmentID:          &segmentID,
		SegmentCount:       &segmentCount,
		MessageSizeBytes:   &messageSize,
		MessageChunkBase64: &chunk,
		ClientID:           clientID,
		StreamID:           streamID,
		SeqID:              seqID,
	}
}
