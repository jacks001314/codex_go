package compact

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #48115: a text-only multipart user message that fits the shared budget is
// kept exactly as captured, including an empty part and its annotations, so the
// next request carries the same content parts.
func TestLocalCompactionKeepsTextOnlyMultipartMessagesLikeRust(t *testing.T) {
	raw := json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Example from our runbook:"},{"type":"input_text","text":""},{"type":"input_text","text":"Do not deploy."}],"content_item_kinds":["user.text","user.text","user.text"]}`)
	message := Item{
		ID:   "u1",
		Type: "message",
		Role: "user",
		Kind: "user_message",
		Raw:  raw,
		Content: []ContentPart{
			{Type: "input_text", Text: "Example from our runbook:"},
			{Type: "input_text", Text: ""},
			{Type: "input_text", Text: "Do not deploy."},
		},
		Data: map[string]any{"content_item_kinds": []string{"user.text", "user.text", "user.text"}},
	}
	retained := retainedUserMessagesForLocalCompaction([]Item{message}, CompactionUserMessageBudget)
	if len(retained) != 1 {
		t.Fatalf("retained = %#v", retained)
	}
	got := retained[0]
	if len(got.Content) != 3 || got.Content[1].Text != "" || got.Content[2].Text != "Do not deploy." {
		t.Fatalf("content parts = %#v", got.Content)
	}
	if string(got.Raw) != string(raw) {
		t.Fatalf("raw item = %q, want the captured item", got.Raw)
	}
	if kinds, _ := got.Data["content_item_kinds"].([]string); len(kinds) != 3 {
		t.Fatalf("annotations = %#v", got.Data)
	}
}

// A message carrying media is materialized as its flattened text: the retained
// message never keeps discarded media.
func TestLocalCompactionOmitsRetainedMediaLikeRust(t *testing.T) {
	message := Item{
		ID:   "u1",
		Type: "message",
		Role: "user",
		Kind: "user_message",
		Text: "look at this",
		Content: []ContentPart{
			{Type: "input_text", Text: "look at this"},
			{Type: "input_image", ImageURL: "data:image/png;base64,AAAA"},
		},
		Raw:  json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"look at this"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}`),
		Data: map[string]any{"content_item_kinds": []string{"user.text", "user.image"}},
	}
	retained := retainedUserMessagesForLocalCompaction([]Item{message}, CompactionUserMessageBudget)
	if len(retained) != 1 {
		t.Fatalf("retained = %#v", retained)
	}
	got := retained[0]
	if len(got.Content) != 0 || len(got.Raw) != 0 {
		t.Fatalf("media survived compaction: content=%#v raw=%q", got.Content, got.Raw)
	}
	if got.Text != "look at this" {
		t.Fatalf("text = %q", got.Text)
	}
	if kinds, _ := got.Data["content_item_kinds"].([]string); len(kinds) != 1 || kinds[0] != "user.text" {
		t.Fatalf("annotations = %#v, want the text-only classification", got.Data)
	}
}

// The budget is shared newest first: messages that fit together are kept, an
// over-budget message is truncated to the remaining budget, and older messages
// are dropped instead of truncated.
func TestLocalCompactionSharesTheUserMessageBudgetLikeRust(t *testing.T) {
	long := strings.Repeat("x", 16_000)
	history := []Item{
		{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: "oldest"},
		{ID: "u2", Type: "message", Role: "user", Kind: "user_message", Text: "middle"},
		{ID: "u3", Type: "message", Role: "user", Kind: "user_message", Text: long},
	}
	retained := retainedUserMessagesForLocalCompaction(history, CompactionUserMessageBudget)
	if len(retained) != 3 {
		t.Fatalf("retained = %d messages, want all three", len(retained))
	}
	for index, want := range []string{"u1", "u2", "u3"} {
		if retained[index].ID != want {
			t.Fatalf("retained[%d] = %q, want %q", index, retained[index].ID, want)
		}
	}

	// A message that does not fit is truncated to the budget and ends the
	// selection, so neither it nor older messages contribute their full text.
	tight := retainedUserMessagesForLocalCompaction(history, 1000)
	if len(tight) != 1 {
		t.Fatalf("retained = %d messages, want the newest one only", len(tight))
	}
	if tight[0].ID != "u3" || EstimateTextTokens(tight[0].Text) > 1000 {
		t.Fatalf("truncated message = %#v", tight[0])
	}
	if !strings.HasPrefix(long, tight[0].Text) || len(tight[0].Text) == len(long) {
		t.Fatalf("text was not truncated: %d of %d bytes", len(tight[0].Text), len(long))
	}

	// A message larger than the whole budget keeps only the text that fits.
	huge := retainedUserMessagesForLocalCompaction([]Item{
		{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: long},
	}, 10)
	if len(huge) != 1 || EstimateTextTokens(huge[0].Text) > 10 || len(huge[0].Text) >= len(long) {
		t.Fatalf("retained = %#v", huge)
	}
}
