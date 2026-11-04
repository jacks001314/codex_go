package cloudclient

import (
	"context"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	threadServiceListTurnsMethod = "/oaiproto.codex_app_server_backend.ThreadService/ListTurns"
	threadServiceListItemsMethod = "/oaiproto.codex_app_server_backend.ThreadService/ListItems"
)

type HistoryOrder int

const (
	HistoryAscending  HistoryOrder = 1
	HistoryDescending HistoryOrder = 2
)

type HistoryAccess int

const (
	HistoryAccessDefault HistoryAccess = 0
	HistoryAccessShared  HistoryAccess = 1
)

// HistoryQuery is a server cursor. Keep the order for next_cursor and reverse
// it for backwards_cursor.
type HistoryQuery struct {
	Cursor *string
	Limit  *uint32
	Order  HistoryOrder
	Access HistoryAccess
}

type ListTurnsRequest struct {
	ThreadID string
	Page     HistoryQuery
}

type ListItemsRequest struct {
	ThreadID string
	TurnID   *string
	Page     HistoryQuery
}

type HistoryPage[T any] struct {
	ThreadID        string
	Data            []T
	NextCursor      *string
	BackwardsCursor *string
	Payload         []byte
}

type HistoryTurn struct {
	Summary TurnSummary
	Payload []byte
}

type HistoryItem struct {
	TurnID        string
	Summary       *ItemSummary
	StartedAtMS   *int64
	CompletedAtMS *int64
	Payload       []byte
}

// ListTurns reads stored turn metadata. It performs no automatic pagination or retry.
func (c *Client) ListTurns(ctx context.Context, request ListTurnsRequest) (HistoryPage[HistoryTurn], error) {
	var page HistoryPage[HistoryTurn]
	if c == nil || c.conn == nil {
		return page, ErrClientBuild
	}
	if request.ThreadID == "" {
		return page, ErrInvalidResponse
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceListTurnsMethod, rawMessage(encodeListTurnsRequest(request)), &response); err != nil {
		return page, mapCallError(requestCtx, err)
	}
	rows, next, backwards, err := decodeHistoryPage(response)
	if err != nil {
		return page, err
	}
	turns := make([]HistoryTurn, 0, len(rows))
	for _, payload := range rows {
		summary, err := decodeTurn(payload)
		if err != nil {
			return page, ErrInvalidResponse
		}
		turns = append(turns, HistoryTurn{Summary: summary, Payload: payload})
	}
	return HistoryPage[HistoryTurn]{ThreadID: request.ThreadID, Data: turns, NextCursor: next, BackwardsCursor: backwards, Payload: append([]byte(nil), response...)}, nil
}

// ListItems reads one page of stored items, optionally restricted to one turn.
func (c *Client) ListItems(ctx context.Context, request ListItemsRequest) (HistoryPage[HistoryItem], error) {
	var page HistoryPage[HistoryItem]
	if c == nil || c.conn == nil {
		return page, ErrClientBuild
	}
	if request.ThreadID == "" {
		return page, ErrInvalidResponse
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceListItemsMethod, rawMessage(encodeListItemsRequest(request)), &response); err != nil {
		return page, mapCallError(requestCtx, err)
	}
	rows, next, backwards, err := decodeHistoryPage(response)
	if err != nil {
		return page, err
	}
	items := make([]HistoryItem, 0, len(rows))
	for _, payload := range rows {
		turnID, ok := stringField(payload, 1)
		if !ok || (request.TurnID != nil && *request.TurnID != turnID) {
			return page, ErrInvalidResponse
		}
		itemBytes, ok := bytesField(payload, 2)
		if !ok {
			return page, ErrInvalidResponse
		}
		summary, supported, err := decodeItem(itemBytes)
		if err != nil {
			return page, err
		}
		var summaryPtr *ItemSummary
		if supported {
			summaryPtr = &summary
		}
		items = append(items, HistoryItem{
			TurnID: turnID, Summary: summaryPtr,
			StartedAtMS: optionalInt64Field(payload, 3), CompletedAtMS: optionalInt64Field(payload, 4),
			Payload: payload,
		})
	}
	return HistoryPage[HistoryItem]{ThreadID: request.ThreadID, Data: items, NextCursor: next, BackwardsCursor: backwards, Payload: append([]byte(nil), response...)}, nil
}

func encodeListTurnsRequest(request ListTurnsRequest) []byte {
	out := protowire.AppendTag(nil, 1, protowire.BytesType)
	out = protowire.AppendString(out, request.ThreadID)
	if request.Page.Cursor != nil && *request.Page.Cursor != "" {
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendString(out, *request.Page.Cursor)
	}
	if request.Page.Limit != nil {
		out = protowire.AppendTag(out, 3, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(*request.Page.Limit))
	}
	out = protowire.AppendTag(out, 4, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(historyOrderValue(request.Page.Order)))
	if request.Page.Access == HistoryAccessShared {
		out = protowire.AppendTag(out, 6, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	return out
}

func encodeListItemsRequest(request ListItemsRequest) []byte {
	out := protowire.AppendTag(nil, 1, protowire.BytesType)
	out = protowire.AppendString(out, request.ThreadID)
	if request.TurnID != nil && *request.TurnID != "" {
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendString(out, *request.TurnID)
	}
	if request.Page.Cursor != nil && *request.Page.Cursor != "" {
		out = protowire.AppendTag(out, 3, protowire.BytesType)
		out = protowire.AppendString(out, *request.Page.Cursor)
	}
	if request.Page.Limit != nil {
		out = protowire.AppendTag(out, 4, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(*request.Page.Limit))
	}
	out = protowire.AppendTag(out, 5, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(historyOrderValue(request.Page.Order)))
	if request.Page.Access == HistoryAccessShared {
		out = protowire.AppendTag(out, 6, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	return out
}

func historyOrderValue(order HistoryOrder) int {
	if order == HistoryAscending {
		return 1
	}
	return 2
}

func decodeHistoryPage(data []byte) ([][]byte, *string, *string, error) {
	rowsBytes, ok := bytesField(data, 1)
	if !ok {
		return nil, nil, nil, ErrInvalidResponse
	}
	rows := repeatedBytes(rowsBytes, 1)
	next, _ := optionalStringField(data, 2)
	backwards, _ := optionalStringField(data, 3)
	var nextPtr, backwardsPtr *string
	if next != "" {
		nextPtr = &next
	}
	if backwards != "" {
		backwardsPtr = &backwards
	}
	return rows, nextPtr, backwardsPtr, nil
}
