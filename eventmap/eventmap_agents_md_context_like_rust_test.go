package eventmap

import "testing"

// Rust core/src/context/user_instructions.rs marks the AGENTS.md block with
// ("# AGENTS.md instructions", "</INSTRUCTIONS>").
func TestContextualUserMessageRecognizesAgentsInstructionsLikeRust(t *testing.T) {
	text := "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>"
	if !IsContextualUserMessageContent([]ContentItem{{Kind: ContentInputText, Text: text}}) {
		t.Fatal("AGENTS.md instructions must classify as contextual user content")
	}
	if IsContextualUserMessageContent([]ContentItem{{Kind: ContentInputText, Text: "Count to 5"}}) {
		t.Fatal("plain user text must stay user input")
	}
}
