package session

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Mirrors Rust #59312: forked history keeps existing item ids and backfills
// missing ones through the shared UUIDv7 generator, so the child's replayed
// model context carries the same ids that are persisted on the fork.
func TestForkBackfillsMissingResponseItemIDsLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	source := &Record{
		ID: "thread-parent", SessionID: "thread-parent",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Items: []Item{
			{Type: "user_message", Role: "user", Text: "inherited question", CreatedAt: now},
			{Type: "function_call", CallID: "call-1", Name: "shell", CreatedAt: now},
			{Type: "function_call_output", CallID: "call-1", Text: "ok", CreatedAt: now},
			{ResponseItemID: "msg_input_kept", Type: "user_message", Role: "user", Text: "kept id", CreatedAt: now},
		},
	}
	forked, err := store.ForkRecord(source, ForkOptions{NewID: "thread-child", Mode: ForkAll})
	if err != nil {
		t.Fatalf("ForkRecord() error = %v", err)
	}
	if got := forked.Items[0].ResponseItemID; !isUUIDv7WithPrefix(got, "msg_") {
		t.Fatalf("backfilled user id = %q, want msg_<uuidv7>", got)
	}
	if forked.Items[0].ID != "" {
		t.Fatalf("thread item id = %q, want the message id kept on ResponseItemID", forked.Items[0].ID)
	}
	if got := forked.Items[1].ID; !isUUIDv7WithPrefix(got, "fc_") {
		t.Fatalf("backfilled function call id = %q, want fc_<uuidv7>", got)
	}
	if got := forked.Items[2].ID; !isUUIDv7WithPrefix(got, "fco_") {
		t.Fatalf("backfilled function output id = %q, want fco_<uuidv7>", got)
	}
	if forked.Items[3].ResponseItemID != "msg_input_kept" {
		t.Fatalf("existing id was rewritten: %q", forked.Items[3].ResponseItemID)
	}
	// The replayed model context reports the same backfilled ids.
	inputs := InputItemsFromItems(forked.Items, nil)
	if len(inputs) != 4 {
		t.Fatalf("replayed inputs = %d, want 4", len(inputs))
	}
	first, _ := inputs[0].(map[string]any)
	if first["id"] != forked.Items[0].ResponseItemID {
		t.Fatalf("message input id = %#v, persisted = %q", first["id"], forked.Items[0].ResponseItemID)
	}
	call, _ := inputs[1].(map[string]any)
	if call["id"] != forked.Items[1].ID {
		t.Fatalf("call input id = %#v, persisted = %q", call["id"], forked.Items[1].ID)
	}
	output, _ := inputs[2].(map[string]any)
	if output["id"] != forked.Items[2].ID {
		t.Fatalf("output input id = %#v, persisted = %q", output["id"], forked.Items[2].ID)
	}
	// The parent record is untouched.
	if source.Items[0].ResponseItemID != "" || source.Items[1].ID != "" {
		t.Fatalf("parent items were mutated: %#v", source.Items[:2])
	}
}

func isUUIDv7WithPrefix(value string, prefix string) bool {
	suffix, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return false
	}
	parsed, err := uuid.Parse(suffix)
	return err == nil && parsed.Version() == 7
}
