package agentsoverview

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

// Rust #46838 shows the active grouping in the command-center header.
func TestAgentCenterHeaderShowsGroupingLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.State.Grouping = GroupingModel
	lines := view.Render(120, 24)
	if len(lines) == 0 || !strings.Contains(lines[0], "Group: Model") {
		t.Fatalf("header missing active grouping: %#v", lines)
	}
}

// Rust #46838 places the search hint/editor above the task list.
func TestAgentCenterSearchRowAboveTaskListLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	lines := view.Render(120, 24)
	searchRow, firstTask := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "Search tasks") {
			searchRow = i
		}
		if strings.Contains(line, "alpha") && firstTask < 0 {
			firstTask = i
		}
	}
	if searchRow < 0 || firstTask < 0 || searchRow >= firstTask {
		t.Fatalf("search row=%d first task=%d lines=%#v", searchRow, firstTask, lines)
	}

	view.State.Searching = true
	view.State.Search = "alp"
	lines = view.Render(120, 24)
	editorRow, firstTask := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "Search \u203a") {
			editorRow = i
		}
		if strings.Contains(line, "alpha") && firstTask < 0 {
			firstTask = i
		}
	}
	if editorRow < 0 || firstTask < 0 || editorRow >= firstTask {
		t.Fatalf("editor row=%d first task=%d lines=%#v", editorRow, firstTask, lines)
	}
}

func TestAgentCenterEmptyAndLoadingStatesLikeRust(t *testing.T) {
	loading := New(nil, "", false)
	loading.Loading = true
	if lines := loading.Render(100, 20); !strings.Contains(strings.Join(lines, "\n"), "Loading tasks\u2026") {
		t.Fatalf("loading state missing: %#v", lines)
	}
	empty := New(nil, "", false)
	if lines := empty.Render(100, 20); !strings.Contains(strings.Join(lines, "\n"), "No tasks yet") {
		t.Fatalf("empty state missing: %#v", lines)
	}
	noMatch := New(sampleRows(), "", false)
	noMatch.State.Searching = true
	noMatch.State.Search = "does-not-match"
	if lines := noMatch.Render(100, 20); !strings.Contains(strings.Join(lines, "\n"), "No matching tasks") {
		t.Fatalf("no-match state missing: %#v", lines)
	}
}

func TestAgentCenterNoticeReplacesFooterLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Notice = "Reconnecting\u2026"
	lines := view.Render(120, 24)
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "Reconnecting\u2026") {
		t.Fatalf("footer notice missing: %#v", lines)
	}
	if strings.Contains(body, "navigate") {
		t.Fatalf("footer hints should yield to the notice: %#v", lines)
	}
}

func TestAgentCenterKeyChordHintOverridesNoticeLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Notice = "Reconnecting\u2026"
	view.KeyChordHints = []KeyChordHint{{Keys: "ctrl-x", Action: "chord\u2026"}}
	lines := view.Render(120, 24)
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "ctrl-x chord\u2026") {
		t.Fatalf("pending key chord missing: %#v", lines)
	}
	if strings.Contains(body, "Reconnecting\u2026") {
		t.Fatalf("notice should yield to the pending key chord: %#v", lines)
	}
}

// Rust #46838 clips search/rename input at grapheme boundaries.
func TestSingleLineViewportClipsGraphemesLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		width  int
		want   string
		clumps int
	}{
		{
			name:   "combining marks",
			input:  strings.Repeat("e\u0301", 6),
			width:  3,
			want:   strings.Repeat("e\u0301", 3),
			clumps: 3,
		},
		{
			name:   "joined emoji",
			input:  strings.Repeat("\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466", 3),
			width:  2,
			want:   "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466",
			clumps: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			view := New(sampleRows(), "", false)
			view.State.Input = testCase.input
			view.State.InputCursor = len([]rune(testCase.input))
			got := view.singleLineViewport(testCase.width)
			if got != testCase.want {
				t.Fatalf("viewport = %q, want %q", got, testCase.want)
			}
			graphemes := uniseg.GraphemeClusterCount(got)
			if graphemes != testCase.clumps {
				t.Fatalf("grapheme count = %d, want %d", graphemes, testCase.clumps)
			}
			if uniseg.StringWidth(got) > testCase.width {
				t.Fatalf("viewport width = %d, want <= %d", uniseg.StringWidth(got), testCase.width)
			}
		})
	}
}
