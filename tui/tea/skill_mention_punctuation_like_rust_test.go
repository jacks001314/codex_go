package tea

import (
	"strings"
	"testing"
)

// Mirrors Rust #60180: a completed mention joins the punctuation the user
// already typed after it, and both insertion paths (the skill popup draft and
// the mention popup that replaces a token in the middle of the draft) share one
// rule, so the mention boundary the turn input sees is the same either way.
func TestCompletedSkillMentionJoinsTypedPunctuationLikeRust(t *testing.T) {
	// Completing at the end of the draft is identical in both paths.
	skillPath := replaceSkillPopupToken("use $my-skill", "$my-skill")
	mentionPath := completedMentionValue("use ", "$my-skill", "")
	if skillPath != "use $my-skill " || mentionPath != skillPath {
		t.Fatalf("end-of-draft completion = %q / %q, want \"use $my-skill \" from both paths", skillPath, mentionPath)
	}

	// Punctuation typed after the token attaches without an inserted space.
	for _, punctuation := range []string{",", ".", ";", ":", "!", "?", ")", "]"} {
		got := completedMentionValue("open ", "@docs", punctuation+" then stop")
		want := "open @docs" + punctuation + " then stop"
		if got != want {
			t.Fatalf("punctuation %q joined as %q, want %q", punctuation, got, want)
		}
	}

	// A following word still gets exactly one space, and existing whitespace is
	// not doubled.
	if got := completedMentionValue("open ", "@docs", "now"); got != "open @docs now" {
		t.Fatalf("word join = %q, want one separating space", got)
	}
	if got := completedMentionValue("open ", "@docs", " now"); got != "open @docs now" {
		t.Fatalf("existing-space join = %q, want the typed space kept once", got)
	}

	// The mention boundary itself never changes with the separator: the text the
	// turn records starts with exactly the inserted mention.
	for _, after := range []string{"", ",", " and", " now"} {
		got := completedMentionValue("", "@docs", after)
		if !strings.HasPrefix(got, "@docs") {
			t.Fatalf("mention boundary moved for suffix %q: %q", after, got)
		}
	}
}
