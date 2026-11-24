package session

import "testing"

// Rust core/src/context/user_instructions.rs classifies the AGENTS.md block as
// a contextual user fragment, so it never becomes user input (#63603).
func TestIsContextualUserMessageClassifiesAgentsInstructionsLikeRust(t *testing.T) {
	item := &Item{Role: "user", Content: []ContentPart{{
		Type: "input_text",
		Text: "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>",
	}}}
	if !IsContextualUserMessage(item) {
		t.Fatal("AGENTS.md instructions must be contextual")
	}
	if preview, ok := UserMessagePreview(item); ok {
		t.Fatalf("preview = %q, want no user preview", preview)
	}
	textOnly := &Item{Role: "user", Text: "# AGENTS.md instructions for project\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>"}
	if !IsContextualUserMessage(textOnly) {
		t.Fatal("text-only AGENTS.md instructions must be contextual")
	}
}

// Rust protocol::strip_user_message_prefix keeps the text after
// USER_MESSAGE_BEGIN and trims the rest.
func TestUserMessagePreviewStripsPriorContextLikeRust(t *testing.T) {
	item := &Item{Role: "user", Text: "<prior context> <user_message>Count to 5"}
	preview, ok := UserMessagePreview(item)
	if !ok || preview != "Count to 5" {
		t.Fatalf("preview = %q, %v", preview, ok)
	}
}

// Rust user_message_preview degrades media-only messages to placeholders.
func TestUserMessagePreviewFallsBackToMediaPlaceholdersLikeRust(t *testing.T) {
	image := &Item{Role: "user", Content: []ContentPart{{Type: "input_image", ImageURL: "data:image/png;base64,AA=="}}}
	if preview, ok := UserMessagePreview(image); !ok || preview != "[Image]" {
		t.Fatalf("image preview = %q, %v", preview, ok)
	}
	audio := &Item{Role: "user", Content: []ContentPart{{Type: "input_audio", AudioURL: "data:audio/wav;base64,AA=="}}}
	if preview, ok := UserMessagePreview(audio); !ok || preview != "[Audio]" {
		t.Fatalf("audio preview = %q, %v", preview, ok)
	}
}
