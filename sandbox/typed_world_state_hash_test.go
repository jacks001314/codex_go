package sandbox

import (
	"testing"

	"codex_go/eventmap"
)

// TestTypedWorldStateFragmentHashMatchesTextBranch pins that sandbox and session
// agree on the Rust v1 branch for a single text fragment.
func TestTypedWorldStateFragmentHashMatchesTextBranch(t *testing.T) {
	rendered := "<permissions instructions>\nX\n</permissions instructions>"
	if got, want := TypedWorldStateFragmentHash("developer", []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: rendered}}), WorldStateFragmentHash("developer", rendered); got != want {
		t.Fatalf("typed text hash = %q, want %q", got, want)
	}
}

// TestTypedWorldStateFragmentHashCoversNonTextContent pins the Rust #54776 v2
// digest for an audio fragment (the same literal as the session test), so both
// carriers produce the upstream digest.
func TestTypedWorldStateFragmentHashCoversNonTextContent(t *testing.T) {
	got := TypedWorldStateFragmentHash("developer", []eventmap.ContentItem{{Kind: eventmap.ContentInputAudio, AudioURL: "audio-one"}})
	if want := "59ed86b12ecd6768b5567426045866a06c313211"; got != want {
		t.Fatalf("typed audio hash = %q, want %q", got, want)
	}
	if got == WorldStateFragmentHash("developer", "") {
		t.Fatal("typed content must not collapse to the empty text digest")
	}
}
