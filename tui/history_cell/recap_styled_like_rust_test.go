package historycell

import (
	"strings"
	"testing"
)

// Rust #46332 "Dim conversation recaps in the TUI": every rendered recap line
// carries the dim modifier (on top of the italic recap style), the
// "Recap:"/"Next:" labels stay bold, and the previous cyan label colour is gone.
func TestThreadRecapHistoryCellDimsRenderedLinesLikeRust(t *testing.T) {
	next := "Run focused tests and check the empty-input case."
	cell := NewThreadRecapHistoryCell("Finished the parser.\nTwelve tests pass.").WithNextAction(&next)
	lines := cell.StyledDisplayLines(48)
	if len(lines) < 3 {
		t.Fatalf("styled lines = %#v", lines)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, recapLineSGR) {
			t.Fatalf("line is not dimmed: %q", line)
		}
		if !strings.HasSuffix(line, recapResetSGR) {
			t.Fatalf("line does not reset its style: %q", line)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, recapLabelSGR+"Recap: "+recapNormalSGR) {
		t.Fatalf("recap label is not bold: %q", joined)
	}
	if !strings.Contains(joined, recapLabelSGR+"Next: "+recapNormalSGR) {
		t.Fatalf("next-action label is not bold: %q", joined)
	}
	// Rust removed the cyan from `Next:`; the recap never paints a colour.
	for _, cyan := range []string{"\x1b[36m", "38;5;14", "38;2;0;255;255"} {
		if strings.Contains(joined, cyan) {
			t.Fatalf("recap still uses the cyan label colour: %q", joined)
		}
	}
	// The plain projection (DisplayLines/RawLines) stays free of escapes.
	if plain := strings.Join(cell.DisplayLines(48), "\n"); strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain display lines carry escapes: %q", plain)
	}
	if raw := strings.Join(cell.RawLines(), "\n"); strings.Contains(raw, "\x1b[") {
		t.Fatalf("raw lines carry escapes: %q", raw)
	}
}

// The dim style never changes the rendered text or its wrapping.
func TestThreadRecapHistoryCellStyledLinesKeepPlainTextLikeRust(t *testing.T) {
	cell := NewThreadRecapHistoryCell("Keep conversation recaps readable in narrow terminals.")
	plain := cell.DisplayLines(32)
	styled := cell.StyledDisplayLines(32)
	if len(plain) != len(styled) {
		t.Fatalf("styled lines = %d, plain = %d", len(styled), len(plain))
	}
	for index, line := range styled {
		stripped := strings.NewReplacer(recapLineSGR, "", recapLabelSGR, "", recapNormalSGR, "", recapResetSGR, "").Replace(line)
		if stripped != plain[index] {
			t.Fatalf("line %d = %q, want %q", index, stripped, plain[index])
		}
	}
}
