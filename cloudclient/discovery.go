package cloudclient

import (
	"context"

	"google.golang.org/protobuf/encoding/protowire"
)

const threadServiceListThreadsMethod = "/oaiproto.codex_app_server_backend.ThreadService/ListThreads"

// ThreadSortKey selects the cloud thread list sort field.
type ThreadSortKey int

const (
	// ThreadSortCreatedAt sorts by thread creation time.
	ThreadSortCreatedAt ThreadSortKey = iota
	// ThreadSortUpdatedAt sorts by the last thread update.
	ThreadSortUpdatedAt
	// ThreadSortRecencyAt sorts by the thread's recency timestamp.
	ThreadSortRecencyAt
)

// ThreadSortDirection selects ascending or descending cloud thread order.
type ThreadSortDirection int

const (
	// ThreadSortDescending orders the newest or highest value first. It is the
	// zero-value default, matching the native query contract.
	ThreadSortDescending ThreadSortDirection = iota
	// ThreadSortAscending orders the oldest or lowest value first.
	ThreadSortAscending
)

// ThreadListQuery is one bounded native page request. Reuse the same
// originators and sort order when following an opaque cursor.
type ThreadListQuery struct {
	Cursor        *string
	Limit         *uint32
	SortKey       ThreadSortKey
	SortDirection ThreadSortDirection
	// Archived lists archived threads instead of the active set (Rust #60635).
	Archived bool
	// Originators restricts discovery to the listed clients. An empty list
	// requests every originator, including threads created by other clients.
	Originators []string
}

// ThreadPage is one page in server order. It is never deduplicated or
// automatically extended with another page.
type ThreadPage struct {
	Data            []ThreadSnapshot           `json:"data"`
	ReadStates      map[string]ThreadReadState `json:"readStates"`
	NextCursor      *string                    `json:"nextCursor"`
	BackwardsCursor *string                    `json:"backwardsCursor"`
}

// ThreadReadState carries the durable unread position and revision for one
// thread.
type ThreadReadState struct {
	FirstUnread *ThreadUnreadPosition `json:"firstUnread"`
	Revision    string                `json:"revision"`
}

// ThreadUnreadPosition identifies where a thread's unread range starts. Type
// is ThreadUnreadThreadStart or ThreadUnreadTurn; TurnID is set only for the
// latter.
type ThreadUnreadPosition struct {
	Type   string `json:"type"`
	TurnID string `json:"turnId,omitempty"`
}

const (
	// ThreadUnreadThreadStart marks the complete thread as unread.
	ThreadUnreadThreadStart = "threadStart"
	// ThreadUnreadTurn marks a turn as the first unread position.
	ThreadUnreadTurn = "turn"
)

// ListThreads reads one unarchived cloud thread page. Read failures are not
// retried automatically.
func (c *Client) ListThreads(ctx context.Context, query ThreadListQuery) (ThreadPage, error) {
	var page ThreadPage
	if c == nil || c.conn == nil {
		return page, ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceListThreadsMethod, rawMessage(encodeThreadListRequest(query)), &response); err != nil {
		return page, mapCallError(requestCtx, err)
	}
	return decodeThreadListPage(response)
}

func encodeThreadListRequest(query ThreadListQuery) []byte {
	var out []byte
	if query.Cursor != nil {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendString(out, *query.Cursor)
	}
	if query.Limit != nil {
		out = protowire.AppendTag(out, 2, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(*query.Limit))
	}
	out = protowire.AppendTag(out, 3, protowire.VarintType)
	out = protowire.AppendVarint(out, threadSortKeyValue(query.SortKey))
	out = protowire.AppendTag(out, 4, protowire.VarintType)
	out = protowire.AppendVarint(out, threadSortDirectionValue(query.SortDirection))
	out = protowire.AppendTag(out, 5, protowire.VarintType)
	if query.Archived {
		out = protowire.AppendVarint(out, 1)
	} else {
		out = protowire.AppendVarint(out, 0)
	}
	var originators []byte
	for _, originator := range query.Originators {
		originators = protowire.AppendTag(originators, 1, protowire.BytesType)
		originators = protowire.AppendString(originators, originator)
	}
	out = protowire.AppendTag(out, 6, protowire.BytesType)
	return protowire.AppendBytes(out, originators)
}

func threadSortKeyValue(key ThreadSortKey) uint64 {
	switch key {
	case ThreadSortUpdatedAt:
		return 2
	case ThreadSortRecencyAt:
		return 3
	default:
		return 1
	}
}

func threadSortDirectionValue(direction ThreadSortDirection) uint64 {
	if direction == ThreadSortAscending {
		return 1
	}
	return 2
}

func decodeThreadListPage(data []byte) (ThreadPage, error) {
	var page ThreadPage
	fields, err := parseWireFields(data)
	if err != nil {
		return page, err
	}
	dataBytes, ok := fieldBytes(fields, 1)
	if !ok {
		return page, ErrInvalidResponse
	}
	rows := repeatedBytes(dataBytes, 1)
	page.Data = make([]ThreadSnapshot, 0, len(rows))
	for _, row := range rows {
		thread, err := decodeThreadSnapshot(row)
		if err != nil {
			return ThreadPage{}, ErrInvalidResponse
		}
		page.Data = append(page.Data, *thread)
	}
	page.NextCursor, _ = fieldString(fields, 2)
	page.BackwardsCursor, _ = fieldString(fields, 3)
	if statesBytes, ok := fieldBytes(fields, 4); ok {
		page.ReadStates, err = decodeThreadReadStates(statesBytes)
		if err != nil {
			return ThreadPage{}, err
		}
	}
	return page, nil
}

func decodeThreadReadStates(data []byte) (map[string]ThreadReadState, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	states := map[string]ThreadReadState{}
	for _, field := range fields {
		if field.number != 1 {
			continue
		}
		if field.typ != protowire.BytesType {
			return nil, ErrInvalidResponse
		}
		entryFields, err := parseWireFields(field.bytes)
		if err != nil {
			return nil, err
		}
		keyField, hasKey := lastField(entryFields, 1)
		if hasKey && keyField.typ != protowire.BytesType {
			return nil, ErrInvalidResponse
		}
		valueField, ok := lastField(entryFields, 2)
		if !ok || valueField.typ != protowire.BytesType {
			return nil, ErrInvalidResponse
		}
		state, err := decodeThreadReadState(valueField.bytes)
		if err != nil {
			return nil, err
		}
		key := ""
		if hasKey {
			key = string(keyField.bytes)
		}
		states[key] = state
	}
	return states, nil
}

func decodeThreadReadState(data []byte) (ThreadReadState, error) {
	var state ThreadReadState
	fields, err := parseWireFields(data)
	if err != nil {
		return state, err
	}
	revisionField, ok := lastField(fields, 2)
	if !ok || revisionField.typ != protowire.BytesType || len(revisionField.bytes) == 0 {
		return state, ErrInvalidResponse
	}
	state.Revision = string(revisionField.bytes)
	positionField, ok := lastField(fields, 1)
	if !ok {
		return state, nil
	}
	if positionField.typ != protowire.BytesType {
		return ThreadReadState{}, ErrInvalidResponse
	}
	position, err := decodeThreadUnreadPosition(positionField.bytes)
	if err != nil {
		return ThreadReadState{}, err
	}
	state.FirstUnread = position
	return state, nil
}

func decodeThreadUnreadPosition(data []byte) (*ThreadUnreadPosition, error) {
	fields, err := parseWireFields(data)
	if err != nil {
		return nil, err
	}
	selected := protowire.Number(0)
	var selectedBytes []byte
	for _, field := range fields {
		if field.number != 1 && field.number != 2 {
			continue
		}
		if field.typ != protowire.BytesType {
			return nil, ErrInvalidResponse
		}
		selected = field.number
		selectedBytes = field.bytes
	}
	switch selected {
	case 1:
		return &ThreadUnreadPosition{Type: ThreadUnreadThreadStart}, nil
	case 2:
		turnFields, err := parseWireFields(selectedBytes)
		if err != nil {
			return nil, err
		}
		turnField, ok := lastField(turnFields, 1)
		if !ok || turnField.typ != protowire.BytesType || len(turnField.bytes) == 0 {
			return nil, ErrInvalidResponse
		}
		return &ThreadUnreadPosition{Type: ThreadUnreadTurn, TurnID: string(turnField.bytes)}, nil
	default:
		return nil, ErrInvalidResponse
	}
}
