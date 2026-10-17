package session

import (
	"reflect"
	"testing"

	"codex_go/eventmap"
)

// TestMergeWorldStateUpdatesCarriesTypedContentLikeRust mirrors Rust #54776's
// world-state contribution carrier: a typed fragment keeps its image reference
// instead of being flattened to text.
func TestMergeWorldStateUpdatesCarriesTypedContentLikeRust(t *testing.T) {
	items := MergeWorldStateUpdates([]WorldStateUpdate{{
		Placement: WorldStateMergeable,
		Role:      "developer",
		ContentItems: []eventmap.ContentItem{
			{Kind: eventmap.ContentInputText, Text: "\ncaption\n"},
			{Kind: eventmap.ContentInputImage, FileID: "extension-image"},
		},
		Kind: "extension_test.instructions",
	}})
	if len(items) != 1 {
		t.Fatalf("items = %#v, want one message", items)
	}
	message, _ := items[0].(map[string]any)
	content, _ := message["content"].([]map[string]any)
	want := []map[string]any{
		{"type": "input_text", "text": "caption"},
		{"type": "input_image", "file_id": "extension-image"},
	}
	if !reflect.DeepEqual(content, want) {
		t.Fatalf("content = %#v, want %#v", content, want)
	}
	metadata, _ := message["internal_chat_message_metadata_passthrough"].(map[string]any)
	kinds, _ := metadata["content_item_kinds"].([]string)
	if !reflect.DeepEqual(kinds, []string{"extension_test.instructions"}) {
		t.Fatalf("content kinds = %#v", metadata)
	}
}

// TestMergeWorldStateUpdatesSkipsEmptyFragments pins that a contribution with
// neither text nor typed content emits nothing (Rust skips empty sections).
func TestMergeWorldStateUpdatesSkipsEmptyFragments(t *testing.T) {
	items := MergeWorldStateUpdates([]WorldStateUpdate{{Placement: WorldStateMergeable, Text: " \n "}})
	if len(items) != 0 {
		t.Fatalf("items = %#v, want none", items)
	}
}

// TestTypedWorldStateFragmentHashTextBranchLikeRust pins that a single text
// fragment keeps the Rust v1 digest, so existing snapshots stay valid.
func TestTypedWorldStateFragmentHashTextBranchLikeRust(t *testing.T) {
	text := "<base>\nX\n</base>"
	if got, want := TypedWorldStateFragmentHash("developer", []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}}), BaseInstructionsHash(text); got != want {
		t.Fatalf("typed text hash = %q, want the v1 digest %q", got, want)
	}
}

// TestTypedWorldStateFragmentHashV2MatchesRustAlgorithm pins the v2 digest
// (#54776): SHA-1 over the v2 domain, the length-prefixed role, the 8-byte
// big-endian content length and the serde bytes of the content. The expected
// values are computed independently from the documented Rust algorithm over the
// canonical `ContentItem` JSON.
func TestTypedWorldStateFragmentHashV2MatchesRustAlgorithm(t *testing.T) {
	audio := func(url string) []eventmap.ContentItem {
		return []eventmap.ContentItem{{Kind: eventmap.ContentInputAudio, AudioURL: url}}
	}
	if got, want := TypedWorldStateFragmentHash("developer", audio("audio-one")), "59ed86b12ecd6768b5567426045866a06c313211"; got != want {
		t.Fatalf("audio-one hash = %q, want %q", got, want)
	}
	if got, want := TypedWorldStateFragmentHash("developer", audio("audio-two")), "811a17a9096e54663a5cda31debca1a2300f2a94"; got != want {
		t.Fatalf("audio-two hash = %q, want %q", got, want)
	}
	if got, want := TypedWorldStateFragmentHash("user", audio("audio-one")), "9a648382f452c1606fbd85d89135ad9129a4dbcd"; got != want {
		t.Fatalf("user hash = %q, want %q", got, want)
	}
	image := []eventmap.ContentItem{{Kind: eventmap.ContentInputImage, FileID: "extension-image"}}
	if got, want := TypedWorldStateFragmentHash("developer", image), "208a4f0f4b0d5ca3137776ea9476f2c40a0e04ed"; got != want {
		t.Fatalf("image hash = %q, want %q", got, want)
	}
	if TypedWorldStateFragmentHash("developer", audio("audio-one")) == TypedWorldStateFragmentHash("user", audio("audio-one")) {
		t.Fatal("the typed hash must cover the role")
	}
}
