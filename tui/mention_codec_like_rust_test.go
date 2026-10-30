package tui

import (
	"fmt"
	"testing"
)

// TestParseLinkedToolMentionPreservesByteOffsetsAndTrimsPathsLikeRust mirrors
// Rust #41218 mention_codec tests: the shared parser works on UTF-8 byte
// offsets, trims whitespace around the path, and accepts both sigils.
func TestParseLinkedToolMentionPreservesByteOffsetsAndTrimsPathsLikeRust(t *testing.T) {
	prefix := "before 🦀 "
	for _, sigil := range []rune{ToolMentionSigil, PluginTextMentionSigil} {
		text := fmt.Sprintf("%s[%ctool_1-name] \n\t( skill://路径/SKILL.md ) trailing", prefix, sigil)
		name, path, end, ok := ParseLinkedToolMention(text, len(prefix), sigil)
		if !ok {
			t.Fatalf("ParseLinkedToolMention(%q, %d, %q) rejected a valid link", text, len(prefix), sigil)
		}
		if name != "tool_1-name" || path != "skill://路径/SKILL.md" {
			t.Fatalf("name/path = %q/%q", name, path)
		}
		if want := len(text) - len(" trailing"); end != want {
			t.Fatalf("end = %d, want %d", end, want)
		}
	}
}

// TestParseLinkedToolMentionRejectsMalformedLinksLikeRust mirrors the Rust
// malformed-link table for the shared parser.
func TestParseLinkedToolMentionRejectsMalformedLinksLikeRust(t *testing.T) {
	for _, text := range []string{
		"[",
		"[$",
		"[$](app://tool)",
		"[$tool.name](app://tool)",
		"[$tool(app://tool)",
		"[$tool]app://tool)",
		"[$tool](app://tool",
		"[$tool]()",
		"[$tool]( \t )",
		"[@tool](app://tool)",
	} {
		if _, _, _, ok := ParseLinkedToolMention(text, 0, ToolMentionSigil); ok {
			t.Fatalf("ParseLinkedToolMention(%q) accepted a malformed link", text)
		}
	}
}
