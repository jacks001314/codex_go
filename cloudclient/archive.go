package cloudclient

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// ErrInvalidThreadID reports an archive or unarchive request without a thread
// id (Rust MutationError::InvalidRequest).
var ErrInvalidThreadID = errors.New("invalid cloud thread id")

// ArchiveThread archives a conversation without opening an attachment.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ErrInvalidThreadID
	}
	if c == nil || c.conn == nil {
		return ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceArchiveMethod, rawMessage(encodeThreadIDRequest(threadID)), &response); err != nil {
		return mapCallError(requestCtx, err)
	}
	return nil
}

// UnarchiveThread restores an archived conversation without resuming it and
// returns its snapshot.
func (c *Client) UnarchiveThread(ctx context.Context, threadID string) (ThreadSnapshot, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ThreadSnapshot{}, ErrInvalidThreadID
	}
	if c == nil || c.conn == nil {
		return ThreadSnapshot{}, ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceUnarchiveMethod, rawMessage(encodeThreadIDRequest(threadID)), &response); err != nil {
		return ThreadSnapshot{}, mapCallError(requestCtx, err)
	}
	thread, ok := decodeUnarchiveResponse(response)
	if !ok || strings.TrimSpace(thread.ID) != threadID {
		return ThreadSnapshot{}, ErrInvalidResponse
	}
	return thread, nil
}

// decodeUnarchiveResponse reads UnarchiveResponse.thread (field 1) and passes
// the nested ThreadSnapshot through the same decoder as list results.
func decodeUnarchiveResponse(data []byte) (ThreadSnapshot, bool) {
	for len(data) > 0 {
		number, wireType, length := protowire.ConsumeTag(data)
		if length < 0 {
			return ThreadSnapshot{}, false
		}
		data = data[length:]
		if number == 1 && wireType == protowire.BytesType {
			payload, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return ThreadSnapshot{}, false
			}
			thread, err := decodeThreadSnapshot(payload)
			if err != nil {
				return ThreadSnapshot{}, false
			}
			return *thread, true
		}
		consumed := protowire.ConsumeFieldValue(number, wireType, data)
		if consumed < 0 {
			return ThreadSnapshot{}, false
		}
		data = data[consumed:]
	}
	return ThreadSnapshot{}, false
}
