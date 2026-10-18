package historycell

import (
	"strings"
	"testing"
)

// TestUnifiedExecInputRendersAllLinesLikeRust mirrors #42107: inline history
// shows the complete background-terminal input after preview limits were
// removed.
func TestUnifiedExecInputRendersAllLinesLikeRust(t *testing.T) {
	input := strings.Repeat("line\n", 40) + "tail-sentinel"
	cell := NewUnifiedExecInteraction("cat", input)
	preview := strings.Join(cell.DisplayLines(80), "\n")
	if !strings.Contains(preview, "tail-sentinel") {
		t.Fatalf("inline history lost input:\n%s", preview)
	}
	if strings.Contains(preview, "Input preview limited") {
		t.Fatalf("inline history still reports a preview limit:\n%s", preview)
	}
	if transcript := strings.Join(cell.TranscriptLines(80), "\n"); transcript != preview {
		t.Fatalf("transcript and inline history diverged:\ninline=%q\ntranscript=%q", preview, transcript)
	}
	if !strings.Contains(strings.Join(cell.RawLines(), "\n"), "tail-sentinel") {
		t.Fatal("raw lines lost the complete input")
	}
}

func TestUnifiedExecInputKeepsSharedIndentLikeRust(t *testing.T) {
	cell := NewUnifiedExecInteraction("python", "print(1)\nprint(2)")
	preview := strings.Join(cell.DisplayLines(80), "\n")
	if !strings.Contains(preview, "\u2514 print(1)") || !strings.Contains(preview, "    print(2)") {
		t.Fatalf("input indentation = %q", preview)
	}
}
