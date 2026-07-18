package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUsableContentWidth(t *testing.T) {
	if _, ok := UsableContentWidth(2, 2); ok {
		t.Fatal("expected exhausted width")
	}
	if got, ok := UsableContentWidth(5, 4); !ok || got != 1 {
		t.Fatalf("width = %d ok=%v, want 1 true", got, ok)
	}
}

func TestLineTruncationCountsWideRunes(t *testing.T) {
	if got := DisplayWidth("😀😀😀"); got != 6 {
		t.Fatalf("DisplayWidth = %d, want 6", got)
	}
	if got := TruncateToWidth("😀😀😀", 4); got != "😀😀" {
		t.Fatalf("TruncateToWidth = %q", got)
	}
	if got := TruncateWithEllipsis("abcdef", 4); got != "abc…" {
		t.Fatalf("TruncateWithEllipsis = %q", got)
	}
}

func TestWrappingURLDetectionMatchesRustHeuristics(t *testing.T) {
	positives := []string{
		"https://example.com/a/b",
		"ftp://host/path",
		"www.example.com/path?x=1",
		"example.test/path#frag",
		"localhost:3000/api",
		"127.0.0.1:8080/health",
		"(https://example.com/wrapped)",
	}
	for _, text := range positives {
		if !TextContainsURLLike(text) {
			t.Fatalf("expected URL-like match for %q", text)
		}
	}
	negatives := []string{"src/main.rs", "foo/bar", "key:value", "just-some-text", "hello.world"}
	for _, text := range negatives {
		if TextContainsURLLike(text) {
			t.Fatalf("did not expect URL-like match for %q", text)
		}
	}
}

func TestAdaptiveWrapKeepsLongURLIntact(t *testing.T) {
	url := "example.test/a-very-long-path-with-many-segments-and-query?x=1"
	got := AdaptiveWrapLine(url, WrapOptions{Width: 20, BreakWords: true})
	if !reflect.DeepEqual(got, []string{url}) {
		t.Fatalf("AdaptiveWrapLine = %#v", got)
	}
	plain := AdaptiveWrapLine("a_very_long_token_without_spaces", WrapOptions{Width: 8, BreakWords: true})
	if len(plain) <= 1 {
		t.Fatalf("expected non-url token to wrap, got %#v", plain)
	}
}

func TestAdaptiveWrapMovesWordBeforeBreakingIt(t *testing.T) {
	got := AdaptiveWrapLine("alpha beta resized", WrapOptions{Width: 12, BreakWords: true})
	want := []string{"alpha beta", "resized"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AdaptiveWrapLine = %#v, want %#v", got, want)
	}
}

func TestSelectionListSkipsDisabledAndRendersRows(t *testing.T) {
	list := NewSelectionList([]SelectionItem{
		{ID: "a", Label: "Alpha", Disabled: true},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma", Description: "third"},
	})
	if item, ok := list.SelectedItem(); !ok || item.ID != "b" {
		t.Fatalf("selected = %#v ok=%v", item, ok)
	}
	list.Move(1)
	if item, _ := list.SelectedItem(); item.ID != "c" {
		t.Fatalf("selected after move = %#v", item)
	}
	rows := strings.Join(list.RenderRows(80), "\n")
	if !strings.Contains(rows, NumberedSelectionPrefix(2, true)+"Gamma - third") || !strings.Contains(rows, "Alpha (disabled)") {
		t.Fatalf("rows:\n%s", rows)
	}
	if !strings.Contains(rows, "\x1b[") {
		t.Fatalf("selected row should include terminal color styling:\n%s", rows)
	}
}

func TestTokenUsageFormattingAndContextRemaining(t *testing.T) {
	usage := TokenUsage{
		InputTokens:           15000,
		CachedInputTokens:     3000,
		OutputTokens:          2000,
		ReasoningOutputTokens: 500,
		TotalTokens:           20000,
	}
	if usage.NonCachedInput() != 12000 || usage.BlendedTotal() != 14000 {
		t.Fatalf("usage math = noncached %d blended %d", usage.NonCachedInput(), usage.BlendedTotal())
	}
	if got := usage.PercentOfContextWindowRemaining(32000); got != 60 {
		t.Fatalf("remaining = %d, want 60", got)
	}
	want := "Token usage: total=14,000 input=12,000 (+ 3,000 cached) output=2,000 (reasoning 500)"
	if got := usage.String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

func TestStatusIndicatorRendering(t *testing.T) {
	start := time.Unix(0, 0)
	status := NewStatusIndicator(start)
	status.UpdateDetails("cargo test -p codex-core and then cargo test -p codex-tui", StatusDetailsPreserve, 1)
	status.InlineMessage = "running tests"
	lines := status.Render(34, start.Add(61*time.Second))
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "Working (1m 01s") {
		t.Fatalf("status line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  - cargo") || !strings.Contains(lines[1], "…") {
		t.Fatalf("details line = %q", lines[1])
	}
}

func TestFormatElapsedCompact(t *testing.T) {
	cases := map[int64]string{
		0:             "0s",
		59:            "59s",
		60:            "1m 00s",
		61:            "1m 01s",
		3600:          "1h 00m 00s",
		25*3600 + 123: "25h 02m 03s",
	}
	for input, want := range cases {
		if got := FormatElapsedCompact(input); got != want {
			t.Fatalf("FormatElapsedCompact(%d) = %q, want %q", input, got, want)
		}
	}
}
