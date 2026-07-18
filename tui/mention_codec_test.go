package tui

import (
	"reflect"
	"testing"
)

func TestDecodeHistoryMentionsRestoresVisibleTokens(t *testing.T) {
	decoded := DecodeHistoryMentionsWithAtMentions(
		"Use [$figma](app://figma-1), [$sample](plugin://sample@test), and [$figma](/tmp/figma/SKILL.md).",
		true,
	)
	if decoded.Text != "Use $figma, $sample, and $figma." {
		t.Fatalf("decoded text = %q", decoded.Text)
	}
	want := []LinkedMention{
		{Sigil: '$', Mention: "figma", Path: "app://figma-1"},
		{Sigil: '$', Mention: "sample", Path: "plugin://sample@test"},
		{Sigil: '$', Mention: "figma", Path: "/tmp/figma/SKILL.md"},
	}
	if !reflect.DeepEqual(decoded.Mentions, want) {
		t.Fatalf("mentions = %#v, want %#v", decoded.Mentions, want)
	}
}

func TestDecodeHistoryMentionsAtSigilModesMatchRust(t *testing.T) {
	decoded := DecodeHistoryMentionsWithAtMentions(
		"Use [@sample](plugin://sample@test) and [$figma](app://figma-1).",
		true,
	)
	if decoded.Text != "Use @sample and $figma." || len(decoded.Mentions) != 2 || decoded.Mentions[0].Sigil != '@' {
		t.Fatalf("enabled decoded = %#v", decoded)
	}

	decoded = DecodeHistoryMentionsWithAtMentions(
		"Use [@sample](plugin://sample@test) and [$figma](app://figma-1).",
		false,
	)
	if decoded.Text != "Use $sample and $figma." || len(decoded.Mentions) != 2 || decoded.Mentions[0].Sigil != '$' {
		t.Fatalf("disabled decoded = %#v", decoded)
	}

	decoded = DecodeHistoryMentionsWithAtMentions("Use [@figma](app://figma-1).", false)
	if decoded.Text != "Use [@figma](app://figma-1)." || len(decoded.Mentions) != 0 {
		t.Fatalf("non-plugin disabled decoded = %#v", decoded)
	}
}

func TestDecodeHistoryMentionsSkipsCommonEnvVars(t *testing.T) {
	decoded := DecodeHistoryMentions("Use [$PATH](app://path-tool) and [$figma](app://figma).")
	if decoded.Text != "Use [$PATH](app://path-tool) and $figma." || len(decoded.Mentions) != 1 || decoded.Mentions[0].Mention != "figma" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestEncodeHistoryMentionsLinksBoundMentionsInOrder(t *testing.T) {
	encoded := EncodeHistoryMentions(
		"$figma then $sample then $figma then $other",
		[]LinkedMention{
			{Sigil: '$', Mention: "figma", Path: "app://figma-app"},
			{Sigil: '$', Mention: "sample", Path: "plugin://sample@test"},
			{Sigil: '$', Mention: "figma", Path: "/tmp/figma/SKILL.md"},
		},
	)
	want := "[$figma](app://figma-app) then [$sample](plugin://sample@test) then [$figma](/tmp/figma/SKILL.md) then $other"
	if encoded != want {
		t.Fatalf("encoded = %q, want %q", encoded, want)
	}
}

func TestEncodeHistoryMentionsAtSigilBoundariesMatchRust(t *testing.T) {
	mention := LinkedMention{Sigil: '@', Mention: "sample", Path: "plugin://sample@test"}
	cases := map[string]string{
		"foo\u3000@sample":                            "foo\u3000[@sample](plugin://sample@test)",
		"Please ask @sample.":                         "Please ask [@sample](plugin://sample@test).",
		"Please ask (@sample)":                        "Please ask ([@sample](plugin://sample@test))",
		"foo@sample.com npx @sample/pkg then @sample": "foo@sample.com npx @sample/pkg then [@sample](plugin://sample@test)",
	}
	for input, want := range cases {
		if got := EncodeHistoryMentions(input, []LinkedMention{mention}); got != want {
			t.Fatalf("EncodeHistoryMentions(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEncodeHistoryMentionsDollarSuffixesMatchRust(t *testing.T) {
	mention := LinkedMention{Sigil: '$', Mention: "figma", Path: "app://figma"}
	cases := map[string]string{
		"($figma)":      "([$figma](app://figma))",
		"$figma/docs":   "[$figma](app://figma)/docs",
		"$figma.suffix": "[$figma](app://figma).suffix",
		"$figma\\docs":  "[$figma](app://figma)\\docs",
	}
	for input, want := range cases {
		if got := EncodeHistoryMentions(input, []LinkedMention{mention}); got != want {
			t.Fatalf("EncodeHistoryMentions(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEncodeHistoryMentionsKeepsSigilSpecificQueues(t *testing.T) {
	encoded := EncodeHistoryMentions(
		"@figma then $figma",
		[]LinkedMention{
			{Sigil: '@', Mention: "figma", Path: "plugin://figma@test"},
			{Sigil: '$', Mention: "figma", Path: "app://figma"},
		},
	)
	if encoded != "[@figma](plugin://figma@test) then [$figma](app://figma)" {
		t.Fatalf("encoded = %q", encoded)
	}

	encoded = EncodeHistoryMentions(
		"@figma then $figma",
		[]LinkedMention{{Sigil: '$', Mention: "figma", Path: "app://figma-app"}},
	)
	if encoded != "@figma then [$figma](app://figma-app)" {
		t.Fatalf("encoded = %q", encoded)
	}
}
