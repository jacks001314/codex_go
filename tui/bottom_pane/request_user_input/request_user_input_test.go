package requestuserinput

import (
	"reflect"
	"testing"
)

func TestLayoutSectionsWithOptionsNotesHiddenMatchRustCore(t *testing.T) {
	sections := LayoutSectionsFor(LayoutInput{
		Area:                   Rect{Width: 40, Height: 12},
		HasOptions:             true,
		NotesVisible:           false,
		QuestionLines:          []string{"Question line 1", "Question line 2"},
		OptionsPreferredHeight: 5,
		OptionsRequiredHeight:  8,
		NotesPreferredHeight:   3,
		FooterPreferredHeight:  2,
	})
	if sections.ProgressArea != (Rect{Width: 40, Height: 1}) {
		t.Fatalf("progress area = %#v", sections.ProgressArea)
	}
	if sections.QuestionArea != (Rect{Y: 1, Width: 40, Height: 2}) {
		t.Fatalf("question area = %#v", sections.QuestionArea)
	}
	if sections.OptionsArea.Height != 5 || sections.OptionsArea.Y != 4 {
		t.Fatalf("options area = %#v", sections.OptionsArea)
	}
	if sections.NotesArea.Height != 0 || sections.FooterLines != 2 {
		t.Fatalf("notes/footer = %#v footer=%d", sections.NotesArea, sections.FooterLines)
	}
}

func TestLayoutSectionsWithoutOptionsTightAndNormalMatchRustCore(t *testing.T) {
	tight := LayoutSectionsFor(LayoutInput{
		Area:          Rect{Width: 20, Height: 2},
		QuestionLines: []string{"one", "two", "three"},
	})
	if !reflect.DeepEqual(tight.QuestionLines, []string{"one", "two"}) || tight.QuestionArea.Height != 2 || tight.FooterLines != 0 {
		t.Fatalf("tight layout = %#v", tight)
	}

	normal := LayoutSectionsFor(LayoutInput{
		Area:                  Rect{Width: 20, Height: 8},
		QuestionLines:         []string{"question"},
		NotesPreferredHeight:  2,
		FooterPreferredHeight: 2,
	})
	if normal.ProgressArea.Height != 1 || normal.QuestionArea.Y != 1 || normal.NotesArea.Height != 4 || normal.FooterLines != 2 {
		t.Fatalf("normal layout = %#v", normal)
	}
}

func TestRenderHelpersWrapBottomAlignFooterAndTruncateMatchRustCore(t *testing.T) {
	if got := RenderQuestionWrapped("Pick the deployment strategy for the next rollout", 18); !reflect.DeepEqual(got, []string{"Pick the", "deployment", "strategy for the", "next rollout"}) {
		t.Fatalf("wrapped question = %#v", got)
	}
	if got := WrapText("部署计划 下一步", 4); !reflect.DeepEqual(got, []string{"部署", "计划", "下一", "步"}) {
		t.Fatalf("wide text wrap = %#v", got)
	}
	if got := RenderRowsBottomAligned([]string{"one", "two"}, 4, "empty"); !reflect.DeepEqual(got, []string{"", "", "one", "two"}) {
		t.Fatalf("bottom aligned rows = %#v", got)
	}
	if got := RenderRowsBottomAligned(nil, 2, "No options"); !reflect.DeepEqual(got, []string{"", "No options"}) {
		t.Fatalf("empty bottom aligned rows = %#v", got)
	}
	if got := WrapFooterTips(24, []FooterTip{{Text: "enter submit", Highlight: true}, {Text: "esc close"}, {Text: "tab notes"}}); !reflect.DeepEqual(got, []string{"[enter submit]", "esc close | tab notes"}) {
		t.Fatalf("footer tips = %#v", got)
	}
	if got := WrapFooterTips(6, []FooterTip{{Text: "averylongtip"}, {Text: "ok"}}); !reflect.DeepEqual(got, []string{"averylongtip", "ok"}) {
		t.Fatalf("long footer tip should not split = %#v", got)
	}
	optionTips := FooterTipsWithOptionProgress([]FooterTip{{Text: "enter submit", Highlight: true}}, true, 1, 5)
	if got := WrapFooterTips(32, optionTips); !reflect.DeepEqual(got, []string{"option 2/5 | [enter submit]"}) {
		t.Fatalf("option progress footer tips = %#v", got)
	}
	clampedTips := FooterTipsWithOptionProgress(nil, true, 99, 2)
	if got := WrapFooterTips(32, clampedTips); !reflect.DeepEqual(got, []string{"option 2/2"}) {
		t.Fatalf("clamped option progress footer tips = %#v", got)
	}
	if got := truncateLineWordBoundaryWithEllipsis("alpha beta gamma", 12); got != "alpha beta\u2026" {
		t.Fatalf("truncated = %q", got)
	}
	if got := truncateLineWordBoundaryWithEllipsis("部署 计划 下一步", 11); got != "部署 计划\u2026" {
		t.Fatalf("wide truncated = %q", got)
	}
	if got := truncateLineWordBoundaryWithEllipsis("  padded", 4); got != "\u2026" {
		t.Fatalf("blank boundary truncated = %q", got)
	}
}

func TestRenderHelpersKeepHalfwidthAndEmojiGraphemesIntact(t *testing.T) {
	if got := truncateLineWordBoundaryWithEllipsis("ab\uff76\uff9ecd", 5); got != "ab\uff76\uff9e\u2026" {
		t.Fatalf("halfwidth truncated = %q", got)
	}
	if got, want := breakLongWord("ab\uff76\uff9ec", 3), []string{"ab", "\uff76\uff9ec"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("halfwidth breakLongWord = %#v, want %#v", got, want)
	}
	if got, want := breakLongWord("a\U0001f44d\U0001f3fbb", 2), []string{"a", "\U0001f44d\U0001f3fb", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("emoji breakLongWord = %#v, want %#v", got, want)
	}
}

func TestRenderUIPlacesSectionsAndFooter(t *testing.T) {
	sections := LayoutSectionsFor(LayoutInput{
		Area:                   Rect{Width: 32, Height: 8},
		HasOptions:             true,
		NotesVisible:           true,
		QuestionLines:          []string{"Question?"},
		OptionsPreferredHeight: 2,
		OptionsRequiredHeight:  2,
		NotesPreferredHeight:   1,
		FooterPreferredHeight:  1,
	})
	lines := RenderUI(RenderInput{
		Sections:            sections,
		Progress:            "Question 1/1",
		OptionRows:          []string{"1. Plan", "2. Ship"},
		Notes:               "Add notes",
		FooterTips:          []FooterTip{{Text: "enter submit", Highlight: true}, {Text: "esc close"}},
		OptionsHidden:       true,
		SelectedOptionIndex: 1,
		OptionsLen:          2,
	}, 32)
	if len(lines) == 0 || lines[0] != "Question 1/1" {
		t.Fatalf("render lines = %#v", lines)
	}
	foundOption := false
	foundNotes := false
	foundFooter := false
	for _, line := range lines {
		if line == "2. Ship" {
			foundOption = true
		}
		if line == "Add notes" {
			foundNotes = true
		}
		if line == "option 2/2 | [enter submit]" || line == "[enter submit] | esc close" {
			foundFooter = true
		}
	}
	if !foundOption || !foundNotes || !foundFooter {
		t.Fatalf("render lines missing sections: %#v", lines)
	}
}
