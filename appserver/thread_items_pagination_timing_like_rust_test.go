package appserver

import "testing"

// Mirrors Rust #59497: page timing names the cursor kind of a thread/items/list
// request.
func TestThreadItemsListCursorKindLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		params *ThreadItemsListParams
		want   string
	}{
		{name: "no cursor", params: &ThreadItemsListParams{}, want: "none"},
		{name: "nil params", params: nil, want: "none"},
		{name: "opaque", params: &ThreadItemsListParams{Cursor: &ThreadItemsListCursor{Opaque: "opaque-1"}}, want: "opaque"},
		{name: "anchor", params: &ThreadItemsListParams{Cursor: &ThreadItemsListCursor{Anchor: &ThreadItemsListAnchor{Type: "item", ItemID: "item-1"}}}, want: "item_anchor"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := threadItemsListCursorKind(testCase.params); got != testCase.want {
				t.Fatalf("threadItemsListCursorKind() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// Mirrors Rust #59497: only thread/items/list responses are marked for
// transport write timing.
func TestRemoteControlItemPageResponseLikeRust(t *testing.T) {
	if !remoteControlItemPageResponse(OK(RequestID{}, &ThreadItemsListResponse{})) {
		t.Fatal("thread/items/list response was not marked as an item page")
	}
	if remoteControlItemPageResponse(OK(RequestID{}, &ThreadItemsReadResponse{})) {
		t.Fatal("thread/items/read response was marked as an item page")
	}
	if remoteControlItemPageResponse(NewRemoteControlStatusChangedNotification(nil)) {
		t.Fatal("notification was marked as an item page")
	}
	if remoteControlItemPageResponse(nil) {
		t.Fatal("nil value was marked as an item page")
	}
}
