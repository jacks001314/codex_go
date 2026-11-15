package cloudclient

import (
	"context"
	"strings"
)

// ReadThreadName reads the saved conversation name without opening or
// replacing an attachment (Rust cloud-client read_thread_name, #61600). An
// empty name means the conversation has none.
func (c *Client) ReadThreadName(ctx context.Context, threadID string) (string, error) {
	if strings.TrimSpace(threadID) == "" {
		return "", ErrInvalidThreadID
	}
	if c == nil || c.conn == nil {
		return "", ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceReadThreadMethod, rawMessage(encodeThreadIDRequest(threadID)), &response); err != nil {
		return "", mapCallError(requestCtx, err)
	}
	return decodeReadThreadName(response, threadID)
}

// decodeReadThreadName extracts the saved name from a ReadThreadResponse. The
// reply is rejected unless it carries the requested conversation id, matching
// Rust's filter on thread.id.
func decodeReadThreadName(data []byte, threadID string) (string, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return "", ErrInvalidResponse
	}
	threadBytes, ok := fieldBytes(fields, 1)
	if !ok {
		return "", ErrInvalidResponse
	}
	threadFields, err := parseWireFields(threadBytes)
	if err != nil {
		return "", ErrInvalidResponse
	}
	id, _ := fieldString(threadFields, 14)
	if id == nil || *id != threadID {
		return "", ErrInvalidResponse
	}
	name, _ := fieldString(threadFields, 17)
	if name == nil {
		return "", nil
	}
	return *name, nil
}
