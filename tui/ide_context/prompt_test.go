package idecontext

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/turn"
)

func descriptor(label string, path string) FileDescriptor {
	return FileDescriptor{Label: label, Path: path}
}

func TestIdeContextDeserializesExistingShapeMatchRust(t *testing.T) {
	raw := []byte(`{
		"activeFile": {
			"label": "lib.rs",
			"path": "src/lib.rs",
			"fsPath": "/repo/src/lib.rs",
			"selection": {
				"start": { "line": 1, "character": 2 },
				"end": { "line": 3, "character": 4 }
			},
			"activeSelectionContent": "selected",
			"selections": []
		},
		"openTabs": [
			{
				"label": "main.rs",
				"path": "src/main.rs",
				"fsPath": "/repo/src/main.rs",
				"startLine": 2,
				"endLine": 10
			}
		],
		"processEnv": {
			"path": "/usr/bin"
		}
	}`)

	var context IdeContext
	if err := json.Unmarshal(raw, &context); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	want := IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor: descriptor("lib.rs", "src/lib.rs"),
			Selection: Range{
				Start: Position{Line: 1, Character: 2},
				End:   Position{Line: 3, Character: 4},
			},
			ActiveSelectionContent: "selected",
			Selections:             []Range{},
		},
		OpenTabs: []FileDescriptor{descriptor("main.rs", "src/main.rs")},
	}
	if !reflect.DeepEqual(context, want) {
		t.Fatalf("context = %#v, want %#v", context, want)
	}
}

func TestRenderPromptContextMatchesAppFormatRust(t *testing.T) {
	context := IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor: descriptor("lib.rs", "src/lib.rs"),
			Selection: Range{
				Start: Position{Line: 4, Character: 0},
				End:   Position{Line: 6, Character: 1},
			},
			ActiveSelectionContent: "fn selected() {}",
		},
		OpenTabs: []FileDescriptor{
			descriptor("lib.rs", "src/lib.rs"),
			descriptor("main.rs", "src/main.rs"),
		},
	}

	got, ok := RenderPromptContext(&context)
	want := "# Context from my IDE setup:\n\n## Active file: src/lib.rs\n\n## Active selection of the file:\nfn selected() {}\n## Open tabs:\n- lib.rs: src/lib.rs\n- main.rs: src/main.rs\n"
	if !ok || got != want {
		t.Fatalf("RenderPromptContext() = %q, %v; want %q, true", got, ok, want)
	}
}

func TestApplyIDEContextUsesPromptDelimiterAndRebasesElementsMatchRust(t *testing.T) {
	context := IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor: descriptor("lib.rs", "src/lib.rs"),
			Selection: Range{
				Start: Position{},
				End:   Position{},
			},
		},
	}
	placeholder := "$figma"
	items := []turn.TurnUserInput{
		{Type: "localImage", Path: filepath.FromSlash("/tmp/screenshot.png")},
		{
			Type: "text",
			Text: "Ask $figma",
			TextElements: []turn.TextElement{{
				ByteRange:   turn.ByteRange{Start: 4, End: 10},
				Placeholder: &placeholder,
			}},
		},
	}

	if !ApplyIDEContextToUserInput(&context, &items) {
		t.Fatal("ApplyIDEContextToUserInput() = false, want true")
	}

	expectedPrefix := "# Context from my IDE setup:\n\n## Active file: src/lib.rs\n\n" + PromptRequestBegin + "\n"
	prefixLen := len(expectedPrefix)
	if got, want := items[1].Text, expectedPrefix+"Ask $figma"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	wantElements := []turn.TextElement{{
		ByteRange:   turn.ByteRange{Start: uint(prefixLen + 4), End: uint(prefixLen + 10)},
		Placeholder: &placeholder,
	}}
	if !reflect.DeepEqual(items[1].TextElements, wantElements) {
		t.Fatalf("text elements = %#v, want %#v", items[1].TextElements, wantElements)
	}
}

func TestExtractPromptRequestReturnsTextAfterLastDelimiterMatchRust(t *testing.T) {
	message := "# Context\n" + PromptRequestBegin + "\nFirst\n" + PromptRequestBegin + "\n  Second\n"

	gotText, gotOffset := ExtractPromptRequestWithOffset(message)
	if gotText != "Second" {
		t.Fatalf("text = %q, want Second", gotText)
	}
	if wantOffset := strings.Index(message, "Second"); gotOffset != wantOffset {
		t.Fatalf("offset = %d, want %d", gotOffset, wantOffset)
	}
}

func TestRenderPromptContextRangesTruncationAndOpenTabLimitsMatchRust(t *testing.T) {
	firstRange := Range{
		Start: Position{Line: 1, Character: 2},
		End:   Position{Line: 1, Character: 5},
	}
	secondRange := Range{
		Start: Position{Line: 3, Character: 0},
		End:   Position{Line: 4, Character: 1},
	}
	context := IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor: descriptor("lib.rs", "src/lib.rs"),
			Selection:      firstRange,
			Selections:     []Range{firstRange, secondRange},
		},
	}

	rendered, ok := RenderPromptContext(&context)
	wantRanges := "# Context from my IDE setup:\n\n## Active file: src/lib.rs\n\n## Active selection ranges:\n- src/lib.rs: line 2, column 3 to line 2, column 6\n- src/lib.rs: line 4, column 1 to line 5, column 2\n"
	if !ok || rendered != wantRanges {
		t.Fatalf("ranges rendered = %q, %v; want %q, true", rendered, ok, wantRanges)
	}

	largeSelection := IdeContext{
		ActiveFile: &ActiveFile{
			FileDescriptor:         descriptor("large.txt", "large.txt"),
			Selection:              Range{Start: Position{}, End: Position{Character: 1}},
			ActiveSelectionContent: strings.Repeat("a", MaxActiveSelectionChars) + "tail",
		},
	}
	rendered, ok = RenderPromptContext(&largeSelection)
	if !ok || !strings.Contains(rendered, fmt.Sprintf("[Selection truncated to %d characters.]", MaxActiveSelectionChars)) || strings.Contains(rendered, "tail") {
		t.Fatalf("large selection rendered = %q, ok=%v", rendered, ok)
	}

	openTabs := make([]FileDescriptor, 0, MaxOpenTabs+2)
	for i := 0; i < MaxOpenTabs+2; i++ {
		openTabs = append(openTabs, descriptor(fmt.Sprintf("file-%d.rs", i), fmt.Sprintf("src/file-%d.rs", i)))
	}
	rendered, ok = RenderPromptContext(&IdeContext{OpenTabs: openTabs})
	if !ok || !strings.Contains(rendered, "- file-99.rs: src/file-99.rs\n") || strings.Contains(rendered, "- file-100.rs: src/file-100.rs\n") || !strings.Contains(rendered, "[2 open tabs omitted.]\n") {
		t.Fatalf("open tabs rendered = %q, ok=%v", rendered, ok)
	}
}
