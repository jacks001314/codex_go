package session

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/attachmentstore"
	"codex_go/audioutil"
	"codex_go/utils"
)

const testWAVDataURL = "data:audio/wav;base64,UklGRiYAAABXQVZFZm10IBAAAAABAAEAwF0AAIC7AAACABAAZGF0YQIAAAAAAA=="

func toolAudioItem(parts []utils.FunctionCallOutputContentItem) Item {
	return Item{
		ID:   "tool-output-turn-clip",
		Type: "function_call_output",
		Data: map[string]any{"content_items": historyContentItemsValue(parts)},
	}
}

func preparedToolAudioItems(t *testing.T, item Item, store attachmentstore.Store, defaultLimit int) Item {
	t.Helper()
	items := []Item{item}
	PrepareFileBackedToolAudio(items, store, "thread", false, defaultLimit)
	return items[0]
}

// TestFileBackedToolAudioKeepsProducerBoundedPartsLikeInlineHistory mirrors
// Rust #61394 `producer_bounded_audio_keeps_the_same_parts_as_inline_history`:
// validating and budgeting before upload must retain exactly the parts an
// inline history would, including a clip that follows a zero-cost invalid clip.
func TestFileBackedToolAudioKeepsProducerBoundedPartsLikeInlineHistory(t *testing.T) {
	clip := utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio, AudioURL: testWAVDataURL}
	caption := utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: strings.Repeat("caption ", 100)}
	malformed := utils.FunctionCallOutputContentItem{
		Kind:     utils.ContentInputAudio,
		AudioURL: "data:audio/wav;base64," + strings.Repeat("!", 8192),
	}
	empty := utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio}
	policy := utils.TokensPolicy(20)
	allowance := policy.WithSerializationAllowance()
	limit := (&allowance).TokenBudget()

	for _, testCase := range []struct {
		name     string
		parts    []utils.FunctionCallOutputContentItem
		keepClip bool
	}{
		{name: "malformed_before_clip", parts: []utils.FunctionCallOutputContentItem{malformed, clip}, keepClip: true},
		{name: "empty_before_clip", parts: []utils.FunctionCallOutputContentItem{empty, clip}, keepClip: true},
		{name: "clip_before_caption", parts: []utils.FunctionCallOutputContentItem{clip, caption}, keepClip: true},
		{name: "caption_before_clip", parts: []utils.FunctionCallOutputContentItem{caption, clip}, keepClip: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			inline := toolAudioItem(testCase.parts)
			PrepareInlineToolAudio(&inline)
			ApplyHistoryTruncationBudget(&inline, &policy, true)
			expected := inline.Data["content_items"]

			item := toolAudioItem(testCase.parts)
			PrepareInlineToolAudio(&item)
			item.FileBackedToolAudioPending = true
			item.ProducerBoundedToolAudio = true
			item.ToolAudioHistoryPolicy = &policy
			prepared := preparedToolAudioItems(t, item, attachmentstore.InlineStore{}, limit)
			if got := prepared.Data["content_items"]; !reflect.DeepEqual(got, expected) {
				t.Fatalf("prepared content = %#v, want inline history %#v", got, expected)
			}
			parts, ok := historyContentItemsFromValue(prepared.Data["content_items"])
			if !ok {
				t.Fatalf("prepared content is not content items: %#v", prepared.Data["content_items"])
			}
			hasClip := false
			for _, part := range parts {
				if part.Kind == utils.ContentInputAudio && part.AudioURL == testWAVDataURL {
					hasClip = true
				}
			}
			if hasClip != testCase.keepClip {
				t.Fatalf("clip retained = %v, want %v (%#v)", hasClip, testCase.keepClip, parts)
			}
		})
	}
}

// TestFileBackedToolAudioBoundsInvalidPlaceholdersLikeRust mirrors Rust
// #61394 `zero_cost_invalid_audio_cannot_expand_into_unbounded_error_text`.
func TestFileBackedToolAudioBoundsInvalidPlaceholdersLikeRust(t *testing.T) {
	for _, producerBudgeted := range []bool{false, true} {
		parts := []utils.FunctionCallOutputContentItem{{Kind: utils.ContentInputText, Text: "keep"}}
		for i := 0; i < 100; i++ {
			parts = append(parts, utils.FunctionCallOutputContentItem{Kind: utils.ContentInputAudio})
		}
		item := toolAudioItem(parts)
		PrepareInlineToolAudio(&item)
		item.FileBackedToolAudioPending = true
		item.ProducerBoundedToolAudio = producerBudgeted
		prepared := preparedToolAudioItems(t, item, attachmentstore.InlineStore{}, 1)
		if got, want := prepared.Data["content_items"], historyContentItemsValue([]utils.FunctionCallOutputContentItem{
			{Kind: utils.ContentInputText, Text: "keep"},
			{Kind: utils.ContentInputText, Text: "[omitted 100 text items ...]"},
		}); !reflect.DeepEqual(got, want) {
			t.Fatalf("producerBudgeted=%v content = %#v, want %#v", producerBudgeted, got, want)
		}
	}
}

// TestFileBackedToolAudioKnownProducerLimitKeepsReplacementLikeRust pins the
// known producer budget path: storage repair may replace a clip with its
// placeholder without dropping the following caption.
func TestFileBackedToolAudioKnownProducerLimitKeepsReplacementLikeRust(t *testing.T) {
	caption := utils.FunctionCallOutputContentItem{Kind: utils.ContentInputText, Text: "caption"}
	item := toolAudioItem([]utils.FunctionCallOutputContentItem{{Kind: utils.ContentInputAudio}, caption})
	PrepareInlineToolAudio(&item)
	mergeHistoryHarnessMetadata(&item, map[string]any{historyTruncationTokenLimitKey: 100})
	item.FileBackedToolAudioPending = true
	item.ProducerBoundedToolAudio = true
	prepared := preparedToolAudioItems(t, item, attachmentstore.InlineStore{}, 1)
	if got, want := prepared.Data["content_items"], historyContentItemsValue([]utils.FunctionCallOutputContentItem{
		{Kind: utils.ContentInputText, Text: audioutil.Placeholder(audioutil.ErrInvalidDataURL)},
		caption,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %#v, want %#v", got, want)
	}
}

type failingToolAudioStore struct {
	kind attachmentstore.ErrorKind
}

func (s failingToolAudioStore) Upload(attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	return attachmentstore.UploadResult{}, attachmentstore.NewStoreError(s.kind, "upload failed")
}

func (s failingToolAudioStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "missing")
}

// TestFileBackedToolAudioUploadFailureKeepsInlineOrPlaceholderLikeRust pins
// the storage outcomes: a backend failure keeps the canonical inline clip,
// while an invalid attachment becomes the rejected-clip placeholder.
func TestFileBackedToolAudioUploadFailureKeepsInlineOrPlaceholderLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		kind  attachmentstore.ErrorKind
		check func(t *testing.T, items []utils.FunctionCallOutputContentItem)
	}{
		{
			name: "backend_failure_keeps_inline",
			kind: attachmentstore.ErrorKindBackend,
			check: func(t *testing.T, items []utils.FunctionCallOutputContentItem) {
				t.Helper()
				if len(items) != 1 || items[0].Kind != utils.ContentInputAudio || items[0].AudioURL != testWAVDataURL || items[0].FileID != "" {
					t.Fatalf("backend failure content = %#v", items)
				}
			},
		},
		{
			name: "invalid_attachment_becomes_placeholder",
			kind: attachmentstore.ErrorKindInvalidAttachment,
			check: func(t *testing.T, items []utils.FunctionCallOutputContentItem) {
				t.Helper()
				if len(items) != 1 || items[0].Kind != utils.ContentInputText || items[0].Text != toolAudioRejectedPlaceholder {
					t.Fatalf("invalid attachment content = %#v", items)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := toolAudioItem([]utils.FunctionCallOutputContentItem{{Kind: utils.ContentInputAudio, AudioURL: testWAVDataURL}})
			item.FileBackedToolAudioPending = true
			prepared := preparedToolAudioItems(t, item, failingToolAudioStore{kind: testCase.kind}, 1000)
			items, ok := historyContentItemsFromValue(prepared.Data["content_items"])
			if !ok {
				t.Fatalf("content items = %#v", prepared.Data["content_items"])
			}
			testCase.check(t, items)
			if applied, _ := harnessMetadata(prepared.Data)[toolAudioBudgetApplied].(bool); !applied {
				t.Fatalf("harness metadata = %#v, want %s", harnessMetadata(prepared.Data), toolAudioBudgetApplied)
			}
		})
	}
}

type countingToolAudioStore struct {
	uploads int
}

func (s *countingToolAudioStore) Upload(attachmentstore.UploadRequest) (attachmentstore.UploadResult, error) {
	s.uploads++
	return attachmentstore.FileUploadResult("file_tool_audio"), nil
}

func (s *countingToolAudioStore) Resolve(attachmentstore.ResolveRequest) (attachmentstore.AttachmentMetadata, error) {
	return attachmentstore.AttachmentMetadata{}, attachmentstore.NewStoreError(attachmentstore.ErrorKindNotFound, "missing")
}

// TestFileBackedToolAudioUploadsOnceAndRecordsClassificationLikeRust pins the
// persisted facts: the clip becomes a file reference, the audio-aware budget is
// recorded, transient markers are cleared, and a replay of the same item does
// not upload again.
func TestFileBackedToolAudioUploadsOnceAndRecordsClassificationLikeRust(t *testing.T) {
	store := &countingToolAudioStore{}
	item := toolAudioItem([]utils.FunctionCallOutputContentItem{{Kind: utils.ContentInputAudio, AudioURL: testWAVDataURL}})
	PrepareInlineToolAudio(&item)
	item.FileBackedToolAudioPending = true
	item.ProducerBoundedToolAudio = true
	policy := utils.TokensPolicy(50)
	item.ToolAudioHistoryPolicy = &policy
	allowance := policy.WithSerializationAllowance()
	limit := (&allowance).TokenBudget()

	prepared := preparedToolAudioItems(t, item, store, limit)
	if store.uploads != 1 {
		t.Fatalf("uploads = %d, want 1", store.uploads)
	}
	parts, ok := historyContentItemsFromValue(prepared.Data["content_items"])
	if !ok || len(parts) != 1 || parts[0].Kind != utils.ContentInputAudio || parts[0].FileID != "file_tool_audio" {
		t.Fatalf("content items = %#v", prepared.Data["content_items"])
	}
	metadata := harnessMetadata(prepared.Data)
	if applied, _ := metadata[toolAudioBudgetApplied].(bool); !applied {
		t.Fatalf("metadata = %#v, want %s", metadata, toolAudioBudgetApplied)
	}
	// Rust's record step skips its truncation (and its limit stamp) once the
	// audio-aware budget already ran, so a fresh clip leaves no saved limit.
	if got, ok := historyTruncationTokenLimit(&prepared); ok {
		t.Fatalf("history limit = %d, want none after an applied audio budget", got)
	}
	if prepared.FileBackedToolAudioPending || prepared.ProducerBoundedToolAudio || prepared.ToolAudioHistoryPolicy != nil {
		t.Fatalf("transient markers = %#v", prepared)
	}

	items := []Item{prepared}
	PrepareFileBackedToolAudio(items, store, "thread", false, limit)
	if store.uploads != 1 {
		t.Fatalf("replayed item uploaded again: %d uploads", store.uploads)
	}
}
