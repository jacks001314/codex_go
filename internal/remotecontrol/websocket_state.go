package remotecontrol

import (
	"sort"
	"sync"
)

type envelopeStreamKey struct {
	clientID ClientID
	streamID StreamID
}

type clientMessageKey struct {
	clientID  ClientID
	streamID  StreamID
	hasStream bool
}

type WebsocketState struct {
	OutboundBuffer                       *ServerEnvelopeBuffer
	Sequencer                            *ServerEnvelopeSequencer
	SubscribeCursor                      *string
	lastCompletedClientChunkSeqByMessage map[clientMessageKey]uint64
	ClientSegmentReassembler             *ClientSegmentReassembler
}

func NewWebsocketState() *WebsocketState {
	return &WebsocketState{
		OutboundBuffer:                       NewServerEnvelopeBuffer(),
		Sequencer:                            NewServerEnvelopeSequencer(),
		lastCompletedClientChunkSeqByMessage: map[clientMessageKey]uint64{},
		ClientSegmentReassembler:             NewClientSegmentReassembler(),
	}
}

func (s *WebsocketState) ObserveClientMessage(envelope *ClientEnvelope, wireSizeBytes int) ClientSegmentObservation {
	if envelope == nil {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	s.ensure()
	key, seqID, hasKey := websocketClientMessageKey(envelope)
	if hasKey {
		if lastSeqID, ok := s.lastCompletedClientChunkSeqByMessage[key]; ok && lastSeqID >= seqID {
			return ClientSegmentObservation{Type: ClientSegmentDropped}
		}
	}
	if envelope.Type == ClientEventClientMessageChunk &&
		envelope.SeqID != nil &&
		envelope.SegmentID != nil &&
		envelope.StreamID != nil &&
		s.ClientSegmentReassembler.ShouldIgnoreChunk(envelope.ClientID, *envelope.StreamID, *envelope.SeqID, *envelope.SegmentID) {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	if hasKey && wireSizeBytes > RemoteControlSegmentMaxBytes {
		if envelope.StreamID != nil {
			s.ClientSegmentReassembler.InvalidateStream(envelope.ClientID, *envelope.StreamID)
		}
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	return s.ClientSegmentReassembler.Observe(envelope)
}

func (s *WebsocketState) RecordClientMessageDelivery(envelope *ClientEnvelope) {
	if envelope == nil {
		return
	}
	s.ensure()
	if envelope.Cursor != nil && *envelope.Cursor != "" {
		s.SubscribeCursor = cloneStringPtr(envelope.Cursor)
	}
	if key, seqID, ok := websocketClientMessageKey(envelope); ok {
		s.lastCompletedClientChunkSeqByMessage[key] = seqID
	}
	if envelope.Type == ClientEventAck && envelope.SeqID != nil && envelope.StreamID != nil {
		s.OutboundBuffer.Ack(envelope.ClientID, *envelope.StreamID, *envelope.SeqID, envelope.SegmentID)
	}
}

func (s *WebsocketState) InvalidateClientMessageStream(clientID ClientID, streamID StreamID) {
	if s == nil {
		return
	}
	s.ensure()
	delete(s.lastCompletedClientChunkSeqByMessage, clientMessageKey{clientID: clientID, streamID: streamID, hasStream: true})
	s.ClientSegmentReassembler.InvalidateStream(clientID, streamID)
}

func (s *WebsocketState) InvalidateClientMessageClient(clientID ClientID) {
	if s == nil {
		return
	}
	s.ensure()
	for key := range s.lastCompletedClientChunkSeqByMessage {
		if key.clientID == clientID {
			delete(s.lastCompletedClientChunkSeqByMessage, key)
		}
	}
	s.ClientSegmentReassembler.InvalidateClient(clientID)
}

func (s *WebsocketState) NextServerEnvelopeSeqID(clientID ClientID, streamID StreamID) uint64 {
	s.ensure()
	return s.Sequencer.NextSeqID(clientID, streamID)
}

func (s *WebsocketState) QueueServerEnvelope(envelope *ServerEnvelope) {
	s.ensure()
	s.OutboundBuffer.Insert(envelope)
}

func (s *WebsocketState) ensure() {
	if s.OutboundBuffer == nil {
		s.OutboundBuffer = NewServerEnvelopeBuffer()
	}
	if s.Sequencer == nil {
		s.Sequencer = NewServerEnvelopeSequencer()
	}
	if s.lastCompletedClientChunkSeqByMessage == nil {
		s.lastCompletedClientChunkSeqByMessage = map[clientMessageKey]uint64{}
	}
	if s.ClientSegmentReassembler == nil {
		s.ClientSegmentReassembler = NewClientSegmentReassembler()
	}
}

func websocketClientMessageKey(envelope *ClientEnvelope) (clientMessageKey, uint64, bool) {
	if envelope == nil || envelope.SeqID == nil {
		return clientMessageKey{}, 0, false
	}
	if envelope.Type != ClientEventClientMessageChunk && !(envelope.Type == ClientEventClientMessage && envelope.ReassembledChunk) {
		return clientMessageKey{}, 0, false
	}
	key := clientMessageKey{clientID: envelope.ClientID}
	if envelope.StreamID != nil {
		key.streamID = *envelope.StreamID
		key.hasStream = true
	}
	return key, *envelope.SeqID, true
}

type ServerEnvelopeBuffer struct {
	mu             sync.RWMutex
	bufferByStream map[envelopeStreamKey][]ServerEnvelope
}

func NewServerEnvelopeBuffer() *ServerEnvelopeBuffer {
	return &ServerEnvelopeBuffer{bufferByStream: map[envelopeStreamKey][]ServerEnvelope{}}
}

func (b *ServerEnvelopeBuffer) Insert(envelope *ServerEnvelope) {
	if envelope == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bufferByStream == nil {
		b.bufferByStream = map[envelopeStreamKey][]ServerEnvelope{}
	}
	key := envelopeStreamKey{clientID: envelope.ClientID, streamID: envelope.StreamID}
	b.bufferByStream[key] = append(b.bufferByStream[key], cloneServerEnvelope(envelope))
}

func (b *ServerEnvelopeBuffer) Ack(clientID ClientID, streamID StreamID, ackedSeqID uint64, ackedSegmentID *int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bufferByStream == nil {
		return
	}
	key := envelopeStreamKey{clientID: clientID, streamID: streamID}
	envelopes := b.bufferByStream[key]
	if len(envelopes) == 0 {
		return
	}
	ackedCursor := envelopeAckCursor{seqID: ackedSeqID, segmentID: ackSegmentCursor(ackedSegmentID)}
	out := envelopes[:0]
	for _, envelope := range envelopes {
		cursor := envelopeAckCursor{seqID: envelope.SeqID, segmentID: serverEnvelopeSegmentCursor(&envelope)}
		if cursor.after(ackedCursor) {
			out = append(out, envelope)
		}
	}
	if len(out) == 0 {
		delete(b.bufferByStream, key)
		return
	}
	b.bufferByStream[key] = append([]ServerEnvelope(nil), out...)
}

func (b *ServerEnvelopeBuffer) ForStream(clientID ClientID, streamID StreamID) []ServerEnvelope {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.bufferByStream == nil {
		return nil
	}
	key := envelopeStreamKey{clientID: clientID, streamID: streamID}
	return cloneServerEnvelopes(b.bufferByStream[key])
}

func (b *ServerEnvelopeBuffer) Envelopes() []ServerEnvelope {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.bufferByStream == nil {
		return nil
	}
	keys := make([]envelopeStreamKey, 0, len(b.bufferByStream))
	for key := range b.bufferByStream {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].clientID != keys[j].clientID {
			return keys[i].clientID < keys[j].clientID
		}
		return keys[i].streamID < keys[j].streamID
	})
	var out []ServerEnvelope
	for _, key := range keys {
		out = append(out, cloneServerEnvelopes(b.bufferByStream[key])...)
	}
	return out
}

type ServerEnvelopeSequencer struct {
	nextSeqIDByStream map[envelopeStreamKey]uint64
}

func NewServerEnvelopeSequencer() *ServerEnvelopeSequencer {
	return &ServerEnvelopeSequencer{nextSeqIDByStream: map[envelopeStreamKey]uint64{}}
}

func (s *ServerEnvelopeSequencer) NextSeqID(clientID ClientID, streamID StreamID) uint64 {
	if s.nextSeqIDByStream == nil {
		s.nextSeqIDByStream = map[envelopeStreamKey]uint64{}
	}
	key := envelopeStreamKey{clientID: clientID, streamID: streamID}
	next := s.nextSeqIDByStream[key]
	if next == 0 {
		next = 1
	}
	s.nextSeqIDByStream[key] = next + 1
	return next
}

type envelopeAckCursor struct {
	seqID     uint64
	segmentID int
}

func (c envelopeAckCursor) after(other envelopeAckCursor) bool {
	if c.seqID != other.seqID {
		return c.seqID > other.seqID
	}
	return c.segmentID > other.segmentID
}

func serverEnvelopeSegmentCursor(envelope *ServerEnvelope) int {
	if envelope == nil || envelope.Type != ServerEventServerMessageChunk || envelope.SegmentID == nil {
		return int(^uint(0) >> 1)
	}
	return *envelope.SegmentID
}

func ackSegmentCursor(segmentID *int) int {
	if segmentID == nil {
		return int(^uint(0) >> 1)
	}
	return *segmentID
}

func cloneServerEnvelope(envelope *ServerEnvelope) ServerEnvelope {
	if envelope == nil {
		return ServerEnvelope{}
	}
	clone := *envelope
	if envelope.Message != nil {
		clone.Message = append([]byte(nil), envelope.Message...)
	}
	if envelope.SegmentID != nil {
		value := *envelope.SegmentID
		clone.SegmentID = &value
	}
	if envelope.SegmentCount != nil {
		value := *envelope.SegmentCount
		clone.SegmentCount = &value
	}
	if envelope.MessageSizeBytes != nil {
		value := *envelope.MessageSizeBytes
		clone.MessageSizeBytes = &value
	}
	clone.MessageChunkBase64 = cloneStringPtr(envelope.MessageChunkBase64)
	if envelope.Status != nil {
		value := *envelope.Status
		clone.Status = &value
	}
	return clone
}

func cloneServerEnvelopes(envelopes []ServerEnvelope) []ServerEnvelope {
	if len(envelopes) == 0 {
		return nil
	}
	out := make([]ServerEnvelope, 0, len(envelopes))
	for i := range envelopes {
		out = append(out, cloneServerEnvelope(&envelopes[i]))
	}
	return out
}
