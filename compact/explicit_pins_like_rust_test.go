package compact

import (
	"sort"
	"testing"
)

func explicitPinTexts(selected map[int]Item) []string {
	positions := make([]int, 0, len(selected))
	for position := range selected {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	texts := make([]string, 0, len(positions))
	for _, position := range positions {
		texts = append(texts, ItemText(&Item{Text: selected[position].Text, Content: selected[position].Content}))
	}
	return texts
}

// Rust #62677: explicit pins survive an exhausted budget only in the
// destination their policy names, while feature-retained developer messages keep
// their historical pricing.
func TestExplicitPinsSurviveExhaustedBudgetLikeRust(t *testing.T) {
	pin := func(text string, role string, metadata map[string]any) Item {
		return Item{Type: "message", Role: role, Text: text, Data: metadata}
	}
	shared := pin("shared", "developer", retentionScopes(true, true))
	thread := pin("thread", "developer", retentionScopes(true, false))
	child := pin("child", "developer", retentionScopes(false, true))
	configured := pin("feature-retained developer log", "developer", retentionScopes(true, nil))
	items := []Item{shared, thread, child, configured}

	if got, want := explicitPinTexts(selectRetainedItems(items, IsRetainedInChildren, IsRetainedInChildren, 0, false, FileAudioDurations{})), []string{"shared", "child"}; !equalStrings(got, want) {
		t.Fatalf("child destination pins = %#v, want %#v", got, want)
	}
	if got, want := explicitPinTexts(selectRetainedItems(items, IsAlwaysRetained, IsAlwaysRetained, 0, false, FileAudioDurations{})), []string{"shared", "thread"}; !equalStrings(got, want) {
		t.Fatalf("thread destination pins = %#v, want %#v", got, want)
	}
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// Rust #62677: an ordinary user message beside a pinned user keeps its place
// because the pin no longer spends the optional user-message budget.
func TestLocalCompactionKeepsOrdinaryUserBesidePinnedUserLikeRust(t *testing.T) {
	ordinary := Item{Type: "message", Role: "user", Kind: "user_message", Text: "abcd"}
	pinned := Item{Type: "message", Role: "user", Kind: "user_message", Text: "larger than the optional budget", Data: retentionScopes(true, true)}
	items := []Item{ordinary, pinned}
	// The local compaction path first retains explicit pins, then spends the
	// user-message budget on everything else.
	selected := selectRetainedItems(items, IsAlwaysRetained, IsAlwaysRetained, 1, false, FileAudioDurations{})
	selected = selectLocalUserMessages(items, 1, selected)
	got := itemsFromPositions(items, selected)
	texts := make([]string, 0, len(got))
	for i := range got {
		texts = append(texts, ItemText(&got[i]))
	}
	if want := []string{"abcd", "larger than the optional budget"}; !equalStrings(texts, want) {
		t.Fatalf("local compaction retained %#v, want %#v", texts, want)
	}
}
