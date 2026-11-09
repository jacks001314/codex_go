package cloudclient

import (
	"context"

	"google.golang.org/protobuf/encoding/protowire"
)

const threadServicePrewarmMethod = "/oaiproto.codex_app_server_backend.ThreadService/Prewarm"

// PrewarmDefaultThread prepares an ordinary coding thread in the default
// managed environment. The mutation is sent once and is never retried: a
// timeout or disconnect may leave a prepared thread whose identity was not
// received. Ephemeral sessions must not use this API (Rust #59351).
func (c *Client) PrewarmDefaultThread(ctx context.Context, model string, modelProvider string) (*ThreadActivation, error) {
	if c == nil || c.conn == nil {
		return nil, ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServicePrewarmMethod, rawMessage(encodePrewarmRequest(model, modelProvider)), &response); err != nil {
		return nil, mapCallError(requestCtx, err)
	}
	activation, err := decodePrewarmResponse(response)
	if err != nil {
		return nil, err
	}
	return activation, nil
}

func pwAppendString(out []byte, number protowire.Number, value string) []byte {
	out = protowire.AppendTag(out, number, protowire.BytesType)
	return protowire.AppendString(out, value)
}

func pwAppendBytes(out []byte, number protowire.Number, value []byte) []byte {
	out = protowire.AppendTag(out, number, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}

func pwAppendVarint(out []byte, number protowire.Number, value uint64) []byte {
	out = protowire.AppendTag(out, number, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func encodePrewarmRequest(model string, modelProvider string) []byte {
	managed := []byte{}
	selection := pwAppendBytes(nil, 1, managed)
	environments := pwAppendBytes(nil, 1, selection)
	out := pwAppendBytes(nil, 10, environments)
	if model != "" {
		out = pwAppendString(out, 15, model)
	}
	if modelProvider != "" {
		out = pwAppendString(out, 16, modelProvider)
	}
	// Rust #60389: every prewarm asks the hosted service for the Codex Apps
	// integration so a prepared thread can expose its app tools immediately.
	plugins := pwAppendString(nil, 1, "codex")
	out = pwAppendBytes(out, 28, plugins)
	return out
}

func decodePrewarmResponse(data []byte) (*ThreadActivation, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	threadBytes, ok := fieldBytes(fields, 14)
	if !ok || len(threadBytes) == 0 {
		return nil, ErrInvalidResponse
	}
	// PrewarmThread omits turns. Add the required empty ThreadTurns message while
	// preserving every original byte and unknown field.
	thread := protowire.AppendTag(nil, 31, protowire.BytesType)
	thread = protowire.AppendBytes(thread, nil)
	thread = append(thread, threadBytes...)

	// The Prewarm response uses different field numbers for the same settings.
	// Retag it into the activation envelope so both APIs share strict parsing.
	var activation []byte
	for _, mapping := range []struct {
		from protowire.Number
		to   protowire.Number
	}{
		{1, 1}, {2, 2}, {3, 3}, {4, 5}, {7, 10}, {8, 11},
		{10, 13}, {11, 14}, {12, 15}, {13, 16},
	} {
		if value, ok := fieldBytes(fields, mapping.from); ok {
			activation = pwAppendBytes(activation, mapping.to, value)
		} else if field, ok := firstField(fields, mapping.from); ok && field.typ == protowire.VarintType {
			activation = pwAppendVarint(activation, mapping.to, field.value)
		}
	}
	activation = pwAppendBytes(activation, 17, thread)
	return decodeThreadActivation(activation, "")
}
