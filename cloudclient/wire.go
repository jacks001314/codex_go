package cloudclient

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// rawMessage carries an already-encoded protobuf envelope. The wire contract is
// intentionally minimal: nested notification/server-request messages stay
// opaque so unknown fields survive until payload typing is added.
type rawMessage []byte

const (
	threadServiceResumeMethod = "/oaiproto.codex_app_server_backend.ThreadService/Resume"
	threadServiceAttachMethod = "/oaiproto.codex_app_server_backend.ThreadService/Attach"
)

func encodeResumeRequest(request ResumeRequest) []byte {
	var out []byte
	if request.ThreadID != "" {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendString(out, request.ThreadID)
	}
	if request.Wait {
		out = protowire.AppendTag(out, 2, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	if request.SharedAccess {
		out = protowire.AppendTag(out, 3, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	return out
}

func encodeAttachRequest(threadID string) []byte {
	var out []byte
	if threadID != "" {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendString(out, threadID)
	}
	return out
}

// decodeAttachResponse extracts the oneof payload from an AttachResponse.
// Unknown fields are skipped, matching prost's forward-compatible decoding.
func decodeAttachResponse(data []byte) (Event, error) {
	for len(data) > 0 {
		number, wireType, length := protowire.ConsumeTag(data)
		if length < 0 {
			return Event{}, ErrInvalidResponse
		}
		data = data[length:]
		switch {
		case number == 1 && wireType == protowire.BytesType:
			payload, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return Event{}, ErrInvalidResponse
			}
			return Event{Kind: EventNotification, Payload: append([]byte(nil), payload...)}, nil
		case number == 2 && wireType == protowire.BytesType:
			payload, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return Event{}, ErrInvalidResponse
			}
			return Event{Kind: EventServerRequest, Payload: append([]byte(nil), payload...)}, nil
		default:
			consumed := protowire.ConsumeFieldValue(number, wireType, data)
			if consumed < 0 {
				return Event{}, fmt.Errorf("%w: malformed envelope", ErrInvalidResponse)
			}
			data = data[consumed:]
		}
	}
	return Event{}, ErrInvalidResponse
}
