package cloudclient

import (
	"context"
	"errors"
	"google.golang.org/protobuf/encoding/protowire"
	"strings"
)

// ErrInvalidThreadName reports a rename request with an empty name.
var ErrInvalidThreadName = errors.New("invalid cloud thread name")

// SetThreadName saves a conversation name without opening an attachment.
func (c *Client) SetThreadName(ctx context.Context, threadID, name string) error {
	if strings.TrimSpace(threadID) == "" {
		return ErrInvalidThreadID
	}
	if strings.TrimSpace(name) == "" {
		return ErrInvalidThreadName
	}
	if c == nil || c.conn == nil {
		return ErrClientBuild
	}
	request := encodeThreadIDRequest(threadID)
	request = protowire.AppendTag(request, 2, protowire.BytesType)
	request = protowire.AppendString(request, name)
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), "/oaiproto.codex_app_server_backend.ThreadService/SetThreadName", rawMessage(request), &response); err != nil {
		return mapCallError(requestCtx, err)
	}
	// EmptyResponse permits unknown fields but rejects malformed protobuf.
	for len(response) > 0 {
		_, _, n := protowire.ConsumeField(response)
		if n < 0 {
			return ErrInvalidResponse
		}
		response = response[n:]
	}
	return nil
}
