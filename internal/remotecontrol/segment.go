package remotecontrol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const (
	RemoteControlSegmentTargetBytes       = 100 * 1024
	RemoteControlSegmentMaxBytes          = 150 * 1024
	RemoteControlReassembledMaxBytes      = 100 * 1024 * 1024
	RemoteControlSegmentCountMax          = 1024
	remoteControlSegmentAssemblyMaxCount  = 128
	remoteControlInitialSegmentBufferSize = 0
)

type ClientSegmentObservationType string

const (
	ClientSegmentForward ClientSegmentObservationType = "forward"
	ClientSegmentPending ClientSegmentObservationType = "pending"
	ClientSegmentDropped ClientSegmentObservationType = "dropped"
)

type ClientSegmentObservation struct {
	Type     ClientSegmentObservationType
	Envelope *ClientEnvelope
}

type ClientSegmentReassembler struct {
	assemblies map[ClientID]*clientSegmentAssembly
	now        func() time.Time
}

type clientSegmentAssembly struct {
	streamID        StreamID
	metadata        clientSegmentMetadata
	raw             []byte
	nextSegmentID   int
	lastChunkSeenAt time.Time
}

type clientSegmentMetadata struct {
	seqID            uint64
	segmentCount     int
	messageSizeBytes int
}

func NewClientSegmentReassembler() *ClientSegmentReassembler {
	return &ClientSegmentReassembler{assemblies: map[ClientID]*clientSegmentAssembly{}, now: time.Now}
}

func (r *ClientSegmentReassembler) Observe(envelope *ClientEnvelope) ClientSegmentObservation {
	if envelope == nil {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	if envelope.Type != ClientEventClientMessageChunk {
		return ClientSegmentObservation{Type: ClientSegmentForward, Envelope: cloneClientEnvelope(envelope)}
	}
	metadata, ok := clientSegmentMetadataFromEnvelope(envelope)
	if !ok {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	if envelope.StreamID == nil || *envelope.StreamID == "" {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	streamID := *envelope.StreamID
	segmentID := *envelope.SegmentID
	if r.ShouldIgnoreChunk(envelope.ClientID, streamID, metadata.seqID, segmentID) {
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	if metadata.segmentCount <= 0 ||
		metadata.segmentCount > RemoteControlSegmentCountMax ||
		segmentID < 0 ||
		segmentID >= metadata.segmentCount ||
		metadata.messageSizeBytes <= 0 ||
		metadata.messageSizeBytes > RemoteControlReassembledMaxBytes ||
		envelope.MessageChunkBase64 == nil ||
		*envelope.MessageChunkBase64 == "" {
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}

	r.ensure()
	now := r.now()
	assembly := r.assemblies[envelope.ClientID]
	switch {
	case assembly == nil:
		r.evictAssembliesIfFull()
		assembly = &clientSegmentAssembly{
			streamID:        streamID,
			metadata:        metadata,
			raw:             make([]byte, 0, remoteControlInitialSegmentBufferSize),
			lastChunkSeenAt: now,
		}
		r.assemblies[envelope.ClientID] = assembly
	case assembly.streamID != streamID:
		assembly = &clientSegmentAssembly{
			streamID:        streamID,
			metadata:        metadata,
			raw:             make([]byte, 0, remoteControlInitialSegmentBufferSize),
			lastChunkSeenAt: now,
		}
		r.assemblies[envelope.ClientID] = assembly
	}

	switch {
	case metadata.seqID < assembly.metadata.seqID:
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	case assembly.metadata != metadata:
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	case segmentID < assembly.nextSegmentID:
		return ClientSegmentObservation{Type: ClientSegmentPending}
	case segmentID != assembly.nextSegmentID:
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}

	decoded, err := base64.StdEncoding.DecodeString(*envelope.MessageChunkBase64)
	if err != nil {
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	if len(assembly.raw)+len(decoded) > metadata.messageSizeBytes {
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	assembly.raw = append(assembly.raw, decoded...)
	assembly.nextSegmentID++
	assembly.lastChunkSeenAt = now
	if assembly.nextSegmentID < metadata.segmentCount {
		return ClientSegmentObservation{Type: ClientSegmentPending}
	}
	if len(assembly.raw) != metadata.messageSizeBytes || !json.Valid(assembly.raw) {
		r.removeAssembly(envelope.ClientID, streamID)
		return ClientSegmentObservation{Type: ClientSegmentDropped}
	}
	reassembled := cloneClientEnvelope(envelope)
	reassembled.Type = ClientEventClientMessage
	reassembled.Message = append(json.RawMessage(nil), assembly.raw...)
	reassembled.SegmentID = nil
	reassembled.SegmentCount = nil
	reassembled.MessageSizeBytes = nil
	reassembled.MessageChunkBase64 = nil
	reassembled.ReassembledChunk = true
	r.removeAssembly(envelope.ClientID, streamID)
	return ClientSegmentObservation{Type: ClientSegmentForward, Envelope: reassembled}
}

func (r *ClientSegmentReassembler) InvalidateStream(clientID ClientID, streamID StreamID) {
	if r == nil {
		return
	}
	r.removeAssembly(clientID, streamID)
}

func (r *ClientSegmentReassembler) InvalidateClient(clientID ClientID) {
	if r == nil || r.assemblies == nil {
		return
	}
	delete(r.assemblies, clientID)
}

func (r *ClientSegmentReassembler) ShouldIgnoreChunk(clientID ClientID, streamID StreamID, seqID uint64, segmentID int) bool {
	if r == nil || r.assemblies == nil {
		return false
	}
	assembly := r.assemblies[clientID]
	if assembly == nil || assembly.streamID != streamID {
		return false
	}
	return seqID < assembly.metadata.seqID || (seqID == assembly.metadata.seqID && segmentID < assembly.nextSegmentID)
}

func (r *ClientSegmentReassembler) removeAssembly(clientID ClientID, streamID StreamID) {
	if r == nil || r.assemblies == nil {
		return
	}
	assembly := r.assemblies[clientID]
	if assembly != nil && assembly.streamID == streamID {
		delete(r.assemblies, clientID)
	}
}

func (r *ClientSegmentReassembler) evictAssembliesIfFull() {
	r.ensure()
	for len(r.assemblies) >= remoteControlSegmentAssemblyMaxCount {
		var oldestClientID ClientID
		var oldestAt time.Time
		first := true
		for clientID, assembly := range r.assemblies {
			if first || assembly.lastChunkSeenAt.Before(oldestAt) {
				oldestClientID = clientID
				oldestAt = assembly.lastChunkSeenAt
				first = false
			}
		}
		if first {
			return
		}
		delete(r.assemblies, oldestClientID)
	}
}

func (r *ClientSegmentReassembler) ensure() {
	if r.assemblies == nil {
		r.assemblies = map[ClientID]*clientSegmentAssembly{}
	}
	if r.now == nil {
		r.now = time.Now
	}
}

func clientSegmentMetadataFromEnvelope(envelope *ClientEnvelope) (clientSegmentMetadata, bool) {
	if envelope == nil ||
		envelope.Type != ClientEventClientMessageChunk ||
		envelope.SeqID == nil ||
		envelope.SegmentID == nil ||
		envelope.SegmentCount == nil ||
		envelope.MessageSizeBytes == nil {
		return clientSegmentMetadata{}, false
	}
	return clientSegmentMetadata{
		seqID:            *envelope.SeqID,
		segmentCount:     *envelope.SegmentCount,
		messageSizeBytes: *envelope.MessageSizeBytes,
	}, true
}

func SplitServerEnvelopeForTransport(envelope *ServerEnvelope) ([]ServerEnvelope, error) {
	if envelope == nil {
		return nil, nil
	}
	if envelope.Type != ServerEventServerMessage {
		return []ServerEnvelope{cloneServerEnvelope(envelope)}, nil
	}
	envelopeSizeBytes, err := serializedJSONLen(envelope)
	if err != nil {
		return nil, err
	}
	if envelopeSizeBytes <= RemoteControlSegmentMaxBytes {
		return []ServerEnvelope{cloneServerEnvelope(envelope)}, nil
	}
	raw := []byte(envelope.Message)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	messageSizeBytes := len(raw)
	if messageSizeBytes > RemoteControlReassembledMaxBytes {
		return []ServerEnvelope{}, nil
	}
	minimalSegmentCount := minInt(maxInt(messageSizeBytes, 1), RemoteControlSegmentCountMax)
	minimalChunk := raw[:minInt(len(raw), 1)]
	minimalLen, err := serializedChunkLen(envelope, 0, minimalSegmentCount, messageSizeBytes, minimalChunk)
	if err != nil {
		return nil, err
	}
	if minimalLen > RemoteControlSegmentMaxBytes {
		return []ServerEnvelope{}, nil
	}

	segmentCount := maxInt(2, ceilDiv(messageSizeBytes, RemoteControlSegmentTargetBytes))
	for {
		chunkSize := maxInt(1, ceilDiv(messageSizeBytes, segmentCount))
		segmentCount = ceilDiv(messageSizeBytes, chunkSize)
		segmentsFit := true
		for segmentID, offset := 0, 0; offset < len(raw); segmentID, offset = segmentID+1, offset+chunkSize {
			end := minInt(len(raw), offset+chunkSize)
			size, err := serializedChunkLen(envelope, segmentID, segmentCount, messageSizeBytes, raw[offset:end])
			if err != nil {
				return nil, err
			}
			if size > RemoteControlSegmentMaxBytes {
				segmentsFit = false
				break
			}
		}
		if segmentsFit {
			segments := make([]ServerEnvelope, 0, segmentCount)
			for segmentID, offset := 0, 0; offset < len(raw); segmentID, offset = segmentID+1, offset+chunkSize {
				end := minInt(len(raw), offset+chunkSize)
				chunk, err := buildServerChunkEnvelope(envelope, segmentID, segmentCount, messageSizeBytes, raw[offset:end])
				if err != nil {
					return nil, err
				}
				segments = append(segments, chunk)
			}
			return segments, nil
		}
		if chunkSize == 1 {
			return []ServerEnvelope{}, nil
		}
		nextSegmentCount := segmentCount + 1
		nextChunkSize := maxInt(1, ceilDiv(messageSizeBytes, nextSegmentCount))
		if nextChunkSize == chunkSize {
			segmentCount = messageSizeBytes
		} else {
			segmentCount = nextSegmentCount
		}
	}
}

func serializedChunkLen(envelope *ServerEnvelope, segmentID int, segmentCount int, messageSizeBytes int, chunk []byte) (int, error) {
	chunkEnvelope, err := buildServerChunkEnvelope(envelope, segmentID, segmentCount, messageSizeBytes, chunk)
	if err != nil {
		return 0, err
	}
	return serializedJSONLen(&chunkEnvelope)
}

func buildServerChunkEnvelope(envelope *ServerEnvelope, segmentID int, segmentCount int, messageSizeBytes int, chunk []byte) (ServerEnvelope, error) {
	if segmentCount > RemoteControlSegmentCountMax {
		return ServerEnvelope{}, fmt.Errorf("remote-control segment count exceeds maximum")
	}
	encoded := base64.StdEncoding.EncodeToString(chunk)
	return ServerEnvelope{
		Type:               ServerEventServerMessageChunk,
		SegmentID:          &segmentID,
		SegmentCount:       &segmentCount,
		MessageSizeBytes:   &messageSizeBytes,
		MessageChunkBase64: &encoded,
		ClientID:           envelope.ClientID,
		StreamID:           envelope.StreamID,
		SeqID:              envelope.SeqID,
	}, nil
}

func serializedJSONLen(value any) (int, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func ceilDiv(value int, by int) int {
	if by <= 0 {
		return 0
	}
	return (value + by - 1) / by
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func cloneClientEnvelope(envelope *ClientEnvelope) *ClientEnvelope {
	if envelope == nil {
		return nil
	}
	clone := *envelope
	clone.Message = cloneRawMessage(envelope.Message)
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
	if envelope.StreamID != nil {
		value := *envelope.StreamID
		clone.StreamID = &value
	}
	if envelope.SeqID != nil {
		value := *envelope.SeqID
		clone.SeqID = &value
	}
	clone.Cursor = cloneStringPtr(envelope.Cursor)
	return &clone
}
