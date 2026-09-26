package historycell

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// Mirrors Rust's approved greeting list (PR #10248): the catalog is pinned so a
// dropped or edited phrase is caught.
func TestGreetingsCatalogLikeRust(t *testing.T) {
	if len(Greetings) != 88 {
		t.Fatalf("greeting count = %d, want 88", len(Greetings))
	}
	for _, phrase := range []string{
		"Whoa, fancy meeting you here!",
		"Look who\u2019s at the keyboard.",
		"Shall we turn \u201cwhat if\u201d into something?",
		"A long time ago, in a directory not so far away\u2026",
	} {
		found := false
		for _, candidate := range Greetings {
			if candidate == phrase {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("greeting %q missing from the catalog", phrase)
		}
	}
	chosen := ChooseGreeting()
	found := false
	for _, candidate := range Greetings {
		if candidate == chosen {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ChooseGreeting() = %q, want a catalog member", chosen)
	}
}

// Mirrors Rust's `history_cell/session.rs` session header: the borderless
// title/directory layout is shared by every flow, and a bound greeting appends
// its phrase beneath the directory line.
func TestSessionHeaderGreetingBannerLikeRust(t *testing.T) {
	header := NewSessionHeader("gpt-5", "high", `D:\repo`, "1.2.3").WithGreeting("Pull up a prompt.")
	lines := header.DisplayLines(80)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{">_ gcode (v1.2.3)", `D:\repo`, "Pull up a prompt."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("banner missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "model:") {
		t.Fatalf("banner must not carry the card's model line:\n%s", joined)
	}
	if strings.Contains(joined, "\u256d") {
		t.Fatalf("banner must be borderless:\n%s", joined)
	}
	for _, line := range lines {
		if tui.DisplayWidth(line) > 80 {
			t.Fatalf("banner line exceeds the width: %q", line)
		}
	}

	raw := strings.Join(header.RawLines(), "\n")
	if !strings.Contains(raw, "gcode (v1.2.3)") || !strings.Contains(raw, "Pull up a prompt.") {
		t.Fatalf("raw banner = %q", raw)
	}

	// Without a greeting the same borderless header renders, minus the greeting.
	plain := strings.Join(NewSessionHeader("gpt-5", "high", `D:\repo`, "1.2.3").DisplayLines(80), "\n")
	if !strings.Contains(plain, ">_ gcode (v1.2.3)") || strings.Contains(plain, "\u256d") || strings.Contains(plain, "model:") {
		t.Fatalf("borderless header = %q", plain)
	}
}
