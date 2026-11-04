package cloudclient

import (
	"bytes"
	"testing"
)

func TestHistoryRequestEncodingPreservesCursorOrderAndAccess(t *testing.T) {
	cursor := "cursor"
	limit := uint32(25)
	encoded := encodeListTurnsRequest(ListTurnsRequest{
		ThreadID: "thread",
		Page:     HistoryQuery{Cursor: &cursor, Limit: &limit, Order: HistoryAscending, Access: HistoryAccessShared},
	})
	want := []byte{}
	want = append(want, protoMessage(1, []byte("thread"))...)
	want = append(want, protoMessage(2, []byte("cursor"))...)
	want = append(want, protoVarint(3, 25)...)
	want = append(want, protoVarint(4, 1)...)
	want = append(want, protoVarint(6, 1)...)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("turns request = %x, want %x", encoded, want)
	}
	items := encodeListItemsRequest(ListItemsRequest{ThreadID: "thread", Page: HistoryQuery{}})
	if !bytes.Equal(items, append(protoMessage(1, []byte("thread")), protoVarint(5, 2)...)) {
		t.Fatalf("default items request = %x", items)
	}
}

func TestHistoryPageDecodesCursorsAndTurns(t *testing.T) {
	turn := []byte{}
	turn = append(turn, protoMessage(4, []byte("turn-1"))...)
	turn = append(turn, protoVarint(8, 2)...)
	rows := append(protoMessage(1, turn), protoMessage(1, turn)...)
	page := append(protoMessage(1, rows), protoMessage(2, []byte("next"))...)
	page = append(page, protoMessage(3, []byte("back"))...)
	decoded, next, backwards, err := decodeHistoryPage(page)
	if err != nil || len(decoded) != 2 || next == nil || *next != "next" || backwards == nil || *backwards != "back" {
		t.Fatalf("page = %d next=%v back=%v err=%v", len(decoded), next, backwards, err)
	}
	summary, err := decodeTurn(decoded[0])
	if err != nil || summary.ID != "turn-1" || summary.Status != TurnInterrupted {
		t.Fatalf("turn summary = %#v err=%v", summary, err)
	}
}

func TestHistoryItemPagePreservesUnsupportedPayload(t *testing.T) {
	item := protoMessage(9, nil)
	entry := append(protoMessage(1, []byte("turn-1")), protoMessage(2, item)...)
	entry = append(entry, protoVarint(3, 100)...)
	entry = append(entry, protoVarint(4, 200)...)
	rows := protoMessage(1, entry)
	page := protoMessage(1, rows)
	decoded, _, _, err := decodeHistoryPage(page)
	if err != nil || len(decoded) != 1 {
		t.Fatalf("page rows = %d err=%v", len(decoded), err)
	}
	turnID, ok := stringField(decoded[0], 1)
	if !ok || turnID != "turn-1" {
		t.Fatalf("item turn id = %q ok=%v", turnID, ok)
	}
	started := optionalInt64Field(decoded[0], 3)
	completed := optionalInt64Field(decoded[0], 4)
	if started == nil || *started != 100 || completed == nil || *completed != 200 {
		t.Fatalf("timestamps = %v/%v", started, completed)
	}
}
