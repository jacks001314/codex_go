package execserver

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

const relayMessageFrameVersion = 1

type relayFrameKind uint8

const (
	relayFrameData relayFrameKind = iota + 1
	relayFrameAck
	relayFrameResume
	relayFrameReset
	relayFrameHeartbeat
	relayFrameHandshake
)

type relayData struct {
	Seq          uint32
	SegmentIndex uint32
	SegmentCount uint32
	Payload      []byte
}

type relayMessageFrame struct {
	Version        uint32
	StreamID       string
	Ack            uint32
	AckBits        uint32
	Kind           relayFrameKind
	Data           relayData
	ResetReason    string
	HandshakeBytes []byte
}

func newRelayDataFrame(streamID string, seq uint32, payload []byte) relayMessageFrame {
	return relayMessageFrame{
		Version:  relayMessageFrameVersion,
		StreamID: streamID,
		Kind:     relayFrameData,
		Data: relayData{
			Seq:          seq,
			SegmentCount: 1,
			Payload:      payload,
		},
	}
}

func newRelayHandshakeFrame(streamID string, payload []byte) relayMessageFrame {
	return relayMessageFrame{Version: relayMessageFrameVersion, StreamID: streamID, Kind: relayFrameHandshake, HandshakeBytes: payload}
}

func newRelayResumeFrame(streamID string) relayMessageFrame {
	return relayMessageFrame{Version: relayMessageFrameVersion, StreamID: streamID, Kind: relayFrameResume}
}

func newRelayResetFrame(streamID string) relayMessageFrame {
	return relayMessageFrame{Version: relayMessageFrameVersion, StreamID: streamID, Kind: relayFrameReset, ResetReason: "noise_relay_protocol_error"}
}

func encodeRelayMessageFrame(frame relayMessageFrame) ([]byte, error) {
	if err := frame.validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, len(frame.StreamID)+len(frame.Data.Payload)+len(frame.HandshakeBytes)+32)
	encoded = protowire.AppendTag(encoded, 1, protowire.VarintType)
	encoded = protowire.AppendVarint(encoded, uint64(frame.Version))
	encoded = protowire.AppendTag(encoded, 2, protowire.BytesType)
	encoded = protowire.AppendString(encoded, frame.StreamID)
	if frame.Ack != 0 {
		encoded = protowire.AppendTag(encoded, 3, protowire.VarintType)
		encoded = protowire.AppendVarint(encoded, uint64(frame.Ack))
	}
	if frame.AckBits != 0 {
		encoded = protowire.AppendTag(encoded, 4, protowire.VarintType)
		encoded = protowire.AppendVarint(encoded, uint64(frame.AckBits))
	}
	switch frame.Kind {
	case relayFrameData:
		body := make([]byte, 0, len(frame.Data.Payload)+16)
		if frame.Data.Seq != 0 {
			body = protowire.AppendTag(body, 1, protowire.VarintType)
			body = protowire.AppendVarint(body, uint64(frame.Data.Seq))
		}
		if frame.Data.SegmentIndex != 0 {
			body = protowire.AppendTag(body, 2, protowire.VarintType)
			body = protowire.AppendVarint(body, uint64(frame.Data.SegmentIndex))
		}
		body = protowire.AppendTag(body, 3, protowire.VarintType)
		body = protowire.AppendVarint(body, uint64(frame.Data.SegmentCount))
		body = protowire.AppendTag(body, 4, protowire.BytesType)
		body = protowire.AppendBytes(body, frame.Data.Payload)
		encoded = protowire.AppendTag(encoded, 5, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, body)
	case relayFrameAck:
		encoded = protowire.AppendTag(encoded, 6, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, nil)
	case relayFrameResume:
		encoded = protowire.AppendTag(encoded, 7, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, nil)
	case relayFrameReset:
		body := protowire.AppendTag(nil, 1, protowire.BytesType)
		body = protowire.AppendString(body, frame.ResetReason)
		encoded = protowire.AppendTag(encoded, 8, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, body)
	case relayFrameHeartbeat:
		encoded = protowire.AppendTag(encoded, 9, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, nil)
	case relayFrameHandshake:
		body := protowire.AppendTag(nil, 1, protowire.BytesType)
		body = protowire.AppendBytes(body, frame.HandshakeBytes)
		encoded = protowire.AppendTag(encoded, 10, protowire.BytesType)
		encoded = protowire.AppendBytes(encoded, body)
	default:
		return nil, errors.New("relay message frame is missing body")
	}
	return encoded, nil
}

func decodeRelayMessageFrame(encoded []byte) (relayMessageFrame, error) {
	var frame relayMessageFrame
	for len(encoded) > 0 {
		number, wireType, n := protowire.ConsumeTag(encoded)
		if n < 0 {
			return relayMessageFrame{}, fmt.Errorf("invalid relay message frame: %v", protowire.ParseError(n))
		}
		encoded = encoded[n:]
		switch number {
		case 1, 3, 4:
			if wireType != protowire.VarintType {
				return relayMessageFrame{}, fmt.Errorf("invalid relay message frame field %d", number)
			}
			value, consumed := protowire.ConsumeVarint(encoded)
			if consumed < 0 {
				return relayMessageFrame{}, protowire.ParseError(consumed)
			}
			encoded = encoded[consumed:]
			switch number {
			case 1:
				frame.Version = uint32(value)
			case 3:
				frame.Ack = uint32(value)
			case 4:
				frame.AckBits = uint32(value)
			}
		case 2, 5, 6, 7, 8, 9, 10:
			if wireType != protowire.BytesType {
				return relayMessageFrame{}, fmt.Errorf("invalid relay message frame field %d", number)
			}
			value, consumed := protowire.ConsumeBytes(encoded)
			if consumed < 0 {
				return relayMessageFrame{}, protowire.ParseError(consumed)
			}
			encoded = encoded[consumed:]
			switch number {
			case 2:
				frame.StreamID = string(value)
			case 5:
				data, err := decodeRelayData(value)
				if err != nil {
					return relayMessageFrame{}, err
				}
				frame.Kind, frame.Data = relayFrameData, data
			case 6:
				frame.Kind = relayFrameAck
			case 7:
				frame.Kind = relayFrameResume
			case 8:
				reason, err := decodeSingleStringField(value, 1)
				if err != nil {
					return relayMessageFrame{}, err
				}
				frame.Kind, frame.ResetReason = relayFrameReset, reason
			case 9:
				frame.Kind = relayFrameHeartbeat
			case 10:
				payload, err := decodeSingleBytesField(value, 1)
				if err != nil {
					return relayMessageFrame{}, err
				}
				frame.Kind, frame.HandshakeBytes = relayFrameHandshake, payload
			}
		default:
			consumed := protowire.ConsumeFieldValue(number, wireType, encoded)
			if consumed < 0 {
				return relayMessageFrame{}, protowire.ParseError(consumed)
			}
			encoded = encoded[consumed:]
		}
	}
	if err := frame.validate(); err != nil {
		return relayMessageFrame{}, err
	}
	return frame, nil
}

func decodeRelayData(encoded []byte) (relayData, error) {
	var data relayData
	for len(encoded) > 0 {
		number, wireType, n := protowire.ConsumeTag(encoded)
		if n < 0 {
			return relayData{}, protowire.ParseError(n)
		}
		encoded = encoded[n:]
		switch number {
		case 1, 2, 3:
			if wireType != protowire.VarintType {
				return relayData{}, fmt.Errorf("invalid relay data field %d", number)
			}
			value, consumed := protowire.ConsumeVarint(encoded)
			if consumed < 0 {
				return relayData{}, protowire.ParseError(consumed)
			}
			encoded = encoded[consumed:]
			switch number {
			case 1:
				data.Seq = uint32(value)
			case 2:
				data.SegmentIndex = uint32(value)
			case 3:
				data.SegmentCount = uint32(value)
			}
		case 4:
			if wireType != protowire.BytesType {
				return relayData{}, errors.New("invalid relay data payload")
			}
			value, consumed := protowire.ConsumeBytes(encoded)
			if consumed < 0 {
				return relayData{}, protowire.ParseError(consumed)
			}
			data.Payload = append([]byte(nil), value...)
			encoded = encoded[consumed:]
		default:
			consumed := protowire.ConsumeFieldValue(number, wireType, encoded)
			if consumed < 0 {
				return relayData{}, protowire.ParseError(consumed)
			}
			encoded = encoded[consumed:]
		}
	}
	return data, nil
}

func decodeSingleStringField(encoded []byte, wanted protowire.Number) (string, error) {
	value, err := decodeSingleBytesField(encoded, wanted)
	return string(value), err
}

func decodeSingleBytesField(encoded []byte, wanted protowire.Number) ([]byte, error) {
	var result []byte
	for len(encoded) > 0 {
		number, wireType, n := protowire.ConsumeTag(encoded)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		encoded = encoded[n:]
		if number == wanted {
			if wireType != protowire.BytesType {
				return nil, errors.New("invalid relay nested message")
			}
			value, consumed := protowire.ConsumeBytes(encoded)
			if consumed < 0 {
				return nil, protowire.ParseError(consumed)
			}
			result = append(result[:0], value...)
			encoded = encoded[consumed:]
			continue
		}
		consumed := protowire.ConsumeFieldValue(number, wireType, encoded)
		if consumed < 0 {
			return nil, protowire.ParseError(consumed)
		}
		encoded = encoded[consumed:]
	}
	return result, nil
}

func (frame relayMessageFrame) validate() error {
	if frame.Version != relayMessageFrameVersion {
		return fmt.Errorf("unsupported relay message frame version %d", frame.Version)
	}
	if strings.TrimSpace(frame.StreamID) == "" {
		return errors.New("relay message frame is missing stream_id")
	}
	switch frame.Kind {
	case relayFrameData:
		if frame.Data.SegmentIndex != 0 || frame.Data.SegmentCount != 1 || len(frame.Data.Payload) == 0 {
			return errors.New("relay data message frame is missing required fields")
		}
	case relayFrameReset:
		if frame.ResetReason == "" {
			return errors.New("relay reset message frame is missing reason")
		}
	case relayFrameHandshake:
		if len(frame.HandshakeBytes) == 0 {
			return errors.New("relay handshake message frame is missing payload")
		}
	case relayFrameAck, relayFrameResume, relayFrameHeartbeat:
	default:
		return errors.New("relay message frame is missing body")
	}
	return nil
}
