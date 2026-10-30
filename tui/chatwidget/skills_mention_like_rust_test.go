package chatwidget

import "testing"

// TestSkillMentionExtractionSharesLinkedParserLikeRust mirrors Rust #41218:
// skill extraction reuses the mention codec's linked-tool parser, so UTF-8
// paths are preserved, whitespace around the path is trimmed, both sigils work,
// and malformed, environment-variable, and non-skill links are ignored.
func TestSkillMentionExtractionSharesLinkedParserLikeRust(t *testing.T) {
	text := "before 🦀 [$writer] \n\t( skill://技能/writer/SKILL.md ) " +
		"[$reader](skill://skills/reader/SKILL.md) " +
		"[$PATH](skill://env/SKILL.md) " +
		"[@broken](skill://skills/broken/SKILL.md) " +
		"[$drive](app://google_drive) $plain"
	mentions := ExtractToolMentionsFromText(text)
	if !mentions.Names["writer"] || !mentions.Names["reader"] || !mentions.Names["plain"] {
		t.Fatalf("names = %#v, want writer, reader and plain", mentions.Names)
	}
	if mentions.Names["PATH"] || mentions.Names["broken"] || mentions.Names["drive"] {
		t.Fatalf("names = %#v, want PATH, truncated links and app links ignored", mentions.Names)
	}
	if got := mentions.LinkedPaths["writer"]; got != "skill://技能/writer/SKILL.md" {
		t.Fatalf("writer linked path = %q", got)
	}
	if got := mentions.LinkedPaths["drive"]; got != "app://google_drive" {
		t.Fatalf("drive linked path = %q", got)
	}

	atMentions := ExtractToolMentionsFromTextWithSigil("Use [@search]( skill://skills/search/SKILL.md )", '@')
	if !atMentions.Names["search"] || atMentions.LinkedPaths["search"] != "skill://skills/search/SKILL.md" {
		t.Fatalf("@ mentions = %#v / %#v", atMentions.Names, atMentions.LinkedPaths)
	}
}

// TestSkillMentionExtractionKeepsQualifiedSkillSpellingLikeRust keeps the
// colon-qualified skill spelling that Rust binds through the composer
// (`home:search`) working for plaintext submissions.
func TestSkillMentionExtractionKeepsQualifiedSkillSpellingLikeRust(t *testing.T) {
	mentions := ExtractToolMentionsFromText("$Docs:review $a_b-c")
	if !mentions.Names["Docs:review"] || !mentions.Names["a_b-c"] {
		t.Fatalf("names = %#v, want Docs:review and a_b-c", mentions.Names)
	}
}
