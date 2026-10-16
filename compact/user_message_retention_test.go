package compact

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/retainedctx"
	"codex_go/utils"
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

// A message carrying media keeps only its text parts: the retained message never
// keeps discarded media, and the text it does keep stays an ordinary content part
// (Rust #53831 rebuilds the message from the original parts).
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
	if len(got.Content) != 1 || got.Content[0].Text != "look at this" {
		t.Fatalf("retained parts = %#v, want the text part only", got.Content)
	}
	for _, part := range got.Content {
		if part.ImageURL != "" || part.AudioURL != "" || part.FileID != "" {
			t.Fatalf("media survived compaction: %#v", part)
		}
	}
	if len(got.Raw) != 0 {
		t.Fatalf("raw item survived compaction: %q", got.Raw)
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
	if tight[0].ID != "u3" {
		t.Fatalf("truncated message = %#v", tight[0])
	}
	// Rust truncates the middle: the head and the tail of the budget survive and a
	// generated notice replaces the rest (truncate_middle_with_token_budget), so
	// the retained text is no longer a prefix of the original.
	retainedBytes := utils.ApproxBytesForTokens(1000)
	if !strings.HasPrefix(tight[0].Text, long[:retainedBytes/2]) ||
		!strings.HasSuffix(tight[0].Text, long[len(long)-retainedBytes/2:]) {
		t.Fatalf("text was not head/tail truncated: %q", tight[0].Text)
	}
	if !strings.Contains(tight[0].Text, "tokens truncated") {
		t.Fatalf("truncation notice missing: %q", tight[0].Text)
	}
	if len(tight[0].Text) >= len(long) {
		t.Fatalf("text was not truncated: %d of %d bytes", len(tight[0].Text), len(long))
	}

	// A message larger than the whole budget keeps only the text that fits, and the
	// retained slice is exact: a 10-token budget is 40 bytes split 20/20, and the
	// notice charges the 15_960 removed bytes as 3_990 approximate tokens. Rust
	// counts the generated notice outside the retained-text budget, so the upper
	// bound is the budget plus the notice (truncate.rs:69-85).
	huge := retainedUserMessagesForLocalCompaction([]Item{
		{ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: long},
	}, 10)
	wantHead, wantNotice := long[:20], "\u20263990 tokens truncated\u2026"
	wantTail := long[len(long)-20:]
	if len(huge) != 1 || len(huge[0].Content) != 3 ||
		huge[0].Content[0].Text != wantHead || huge[0].Content[1].Text != wantNotice || huge[0].Content[2].Text != wantTail {
		t.Fatalf("content = %#v, want %q / %q / %q", huge, wantHead, wantNotice, wantTail)
	}
	for _, part := range huge[0].Content {
		if part.Type != "input_text" || part.ImageURL != "" || part.AudioURL != "" || part.FileID != "" {
			t.Fatalf("part = %#v, want a plain text part", part)
		}
	}
	// The text view joins the retained parts with newlines, so its bound is the
	// budget plus the notice plus the two separators.
	wantText := wantHead + "\n" + wantNotice + "\n" + wantTail
	if huge[0].Text != wantText {
		t.Fatalf("text = %q, want %q", huge[0].Text, wantText)
	}
}

// Rust #53831 `build_token_limited_compacted_history_truncates_overlong_user_messages`:
// an over-budget user message keeps the head of its first text part, a generated
// truncation notice, and the tail of its last text part - every retained slice
// keeps the classification recorded for its source index, and a missing or short
// classification list defaults to unknown.
func TestLocalCompactionTruncatesOverlongUserMessagesLikeRust(t *testing.T) {
	big := strings.Repeat("word ", 200)
	for _, partCount := range []int{1, 2} {
		item := Item{ID: "u1", Type: "message", Role: "user", Kind: "user_message"}
		kinds := make([]string, 0, partCount)
		for index := 0; index < partCount; index++ {
			item.Content = append(item.Content, ContentPart{Type: "input_text", Text: big})
			kinds = append(kinds, "user.text")
		}
		item.Data = map[string]any{"content_item_kinds": kinds}

		retained := retainedUserMessagesForLocalCompaction([]Item{item}, 16)
		if len(retained) != 1 {
			t.Fatalf("parts=%d retained = %#v", partCount, retained)
		}
		got := retained[0]
		wantMarker := fmt.Sprintf("\u2026%d tokens truncated\u2026", 250*partCount-16)
		if len(got.Content) != 3 ||
			got.Content[0].Text != big[:32] ||
			got.Content[1].Text != wantMarker ||
			got.Content[2].Text != big[len(big)-32:] {
			t.Fatalf("parts=%d content = %#v", partCount, got.Content)
		}
		gotKinds, _ := got.Data["content_item_kinds"].([]string)
		if len(gotKinds) != 3 || gotKinds[0] != "user.text" || gotKinds[1] != "unknown" || gotKinds[2] != "user.text" {
			t.Fatalf("parts=%d kinds = %#v", partCount, gotKinds)
		}

		item.Data = map[string]any{}
		unannotated := retainedUserMessagesForLocalCompaction([]Item{item}, 16)[0]
		missingKinds, _ := unannotated.Data["content_item_kinds"].([]string)
		if len(missingKinds) != 3 || missingKinds[0] != "unknown" || missingKinds[1] != "unknown" || missingKinds[2] != "unknown" {
			t.Fatalf("parts=%d missing kinds = %#v", partCount, missingKinds)
		}
	}
}

// Rust `compact.rs:757-788`: a retained message is rebuilt from its surviving
// text parts, and a message whose parts were all media keeps an empty text part
// with an unknown classification instead of its wrapper labels. Only the legacy
// flattened shape - text and no content parts - still contributes `ItemText`.
func TestLocalCompactionKeepsMediaOnlyMessagesTextEmptyLikeRust(t *testing.T) {
	imageTriple := []ContentPart{
		{Type: "input_text", Text: `<image name=[Image #1] path="C:\shots\a.png">`},
		{Type: "input_image", ImageURL: "data:image/png;base64,AAAA"},
		{Type: "input_text", Text: "</image>"},
	}
	audioTriple := []ContentPart{
		{Type: "input_text", Text: `<audio name=[Audio #1] path="C:\clips\a.wav">`},
		{Type: "input_audio", AudioURL: "data:audio/wav;base64,AAAA"},
		{Type: "input_text", Text: "</audio>"},
	}
	mixed := append(append([]ContentPart{}, imageTriple...), audioTriple...)
	for _, tc := range []struct {
		name    string
		content []ContentPart
	}{
		{name: "image only", content: imageTriple},
		{name: "audio only", content: audioTriple},
		{name: "image and audio", content: mixed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := Item{
				ID: "u1", Type: "message", Role: "user", Kind: "user_message",
				// The flattened text view of the same item: a media-only message
				// must not fall back to it as user text.
				Text:    `<image name=[Image #1] path="C:\shots\a.png">` + "\n" + "</image>",
				Content: tc.content,
			}
			retained := retainedUserMessagesForLocalCompaction([]Item{message}, CompactionUserMessageBudget)
			if len(retained) != 1 {
				t.Fatalf("retained = %#v", retained)
			}
			got := retained[0]
			if got.Text != "" {
				t.Fatalf("text = %q, want empty for a media-only message", got.Text)
			}
			if len(got.Content) != 1 || got.Content[0].Type != "input_text" || got.Content[0].Text != "" {
				t.Fatalf("content = %#v, want a single empty text part", got.Content)
			}
			if kinds, _ := got.Data["content_item_kinds"].([]string); len(kinds) != 1 || kinds[0] != "unknown" {
				t.Fatalf("kinds = %#v, want [unknown]", got.Data["content_item_kinds"])
			}
		})
	}

	// The legacy flattened rollout item - text and no content parts - is the one
	// shape that still contributes ItemText.
	flattened := retainedUserMessagesForLocalCompaction([]Item{{
		ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: "flat text",
	}}, CompactionUserMessageBudget)[0]
	if flattened.Text != "flat text" {
		t.Fatalf("text = %q, want the flat item text", flattened.Text)
	}
}

// Rust `compact.rs:790-794` marks the retained sources of a message the budget
// shortened (`CodexHarnessMetadata::mark_retained_sources_incomplete`): the
// shortened message no longer proves delivery of a complete original.
func TestLocalCompactionMarksRetainedSourcesIncompleteLikeRust(t *testing.T) {
	completeMetadata := func() json.RawMessage {
		raw, err := json.Marshal(retainedctx.HarnessMetadata{
			GuardianSourceOrderGuidance: true,
			RetainedSource: &retainedctx.RetainedSource{
				ID:       retainedctx.RetainedSourceID{MessageID: "m1", TurnID: "t1", Role: retainedctx.RetainedSourceRoleUser},
				Revision: "r1",
				Complete: true,
			},
		})
		if err != nil {
			t.Fatalf("marshal harness metadata: %v", err)
		}
		return raw
	}
	readMetadata := func(item Item) retainedctx.HarnessMetadata {
		raw, ok := item.Data["harness_metadata"].(json.RawMessage)
		if !ok {
			t.Fatalf("harness_metadata = %#v, want raw JSON", item.Data["harness_metadata"])
		}
		var metadata retainedctx.HarnessMetadata
		if err := json.Unmarshal(raw, &metadata); err != nil {
			t.Fatalf("unmarshal harness metadata: %v", err)
		}
		return metadata
	}

	// A message the budget shortened loses the delivery proof.
	overBudget := strings.Repeat("x", 4_000)
	shortened := retainedUserMessagesForLocalCompaction([]Item{{
		ID: "u1", Type: "message", Role: "user", Kind: "user_message", Text: overBudget,
		Data: map[string]any{"harness_metadata": completeMetadata()},
	}}, 16)[0]
	marked := readMetadata(shortened)
	if marked.RetainedSource == nil || marked.RetainedSource.Complete {
		t.Fatalf("retained source = %#v, want the proof dropped", marked.RetainedSource)
	}
	if marked.GuardianSourceOrderGuidance {
		t.Fatalf("order guidance = true, want false after a shortening")
	}

	// Media that fits the budget is not a shortening, so the proof survives.
	fits := retainedUserMessagesForLocalCompaction([]Item{{
		ID: "u2", Type: "message", Role: "user", Kind: "user_message", Text: "look at this",
		Content: []ContentPart{
			{Type: "input_text", Text: "look at this"},
			{Type: "input_image", ImageURL: "data:image/png;base64,AAAA"},
		},
		Data: map[string]any{"harness_metadata": completeMetadata()},
	}}, CompactionUserMessageBudget)[0]
	intact := readMetadata(fits)
	if intact.RetainedSource == nil || !intact.RetainedSource.Complete || !intact.GuardianSourceOrderGuidance {
		t.Fatalf("retained source = %#v / guidance = %v, want the untouched proof", intact.RetainedSource, intact.GuardianSourceOrderGuidance)
	}

	// An item without harness metadata is kept silently and mints no key.
	bare := retainedUserMessagesForLocalCompaction([]Item{{
		ID: "u3", Type: "message", Role: "user", Kind: "user_message", Text: overBudget,
	}}, 16)[0]
	if _, ok := bare.Data["harness_metadata"]; ok {
		t.Fatalf("harness_metadata = %#v, want no key", bare.Data["harness_metadata"])
	}
	if bare.Text == "" {
		t.Fatalf("text = %q, want the shortened text", bare.Text)
	}
}
