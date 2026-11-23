package execcell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	historycell "codex_go/tui/history_cell"
)

// styledSummary flattens a row into text/style pairs so ANSI encodings that
// render identically compare equal, mirroring the Rust test's comparison of
// styled graphemes.
func styledSummary(line string) string {
	var summary strings.Builder
	for _, span := range historycell.ParseANSISpans(line) {
		fmt.Fprintf(&summary, "%q[fg=%s bg=%s bold=%t dim=%t italic=%t underline=%t]",
			span.Text, span.Style.Foreground, span.Style.Background,
			span.Style.Bold, span.Style.Dim, span.Style.Italic, span.Style.Underline)
	}
	return summary.String()
}

func TestCommandOutputLineResolvesProgressRedrawsLikeRust(t *testing.T) {
	steps := []struct {
		chunk string
		want  string
	}{
		{"abcdef\r", "abcdef"},
		{"XY", "XYcdef"},
		{"\x1b[", "XYcdef"},
		{"K", "XY"},
		{"\r界\u0301ok\r!", "! ok"},
		{"\r\x1b[0K100%", "100%"},
		{"\rDownloading 100%\r\x1b[2KDone", "Done"},
		{"\rXY\x1b[2K!", "  !"},
		{"\r\x1b[K\r\x1b[K\r\x1b[K", ""},
		{"\x1b[31m10%\r", "\x1b[31m10%"},
		{"100%\x1b[0m", "\x1b[31m100%\x1b[0m"},
		{"\r\x1b[31mabcdef\rXY\rZ", "\x1b[31mZYcdef"},
		{"\r\x1b[32m!", "\x1b[32m!\x1b[31mYcdef"},
		{"\r\x1b[1;44;31m.\x1b[39m\rFinished", "\x1b[1;44mFinished"},
	}
	for _, prefix := range []string{"", strings.Repeat("x", MaxLiveOutputBytes)} {
		pending := &PendingLine{}
		if prefix != "" {
			pending.PushLines(prefix + "\n")
		}
		for index, step := range steps {
			pending.PushLines(step.chunk)
			got := styledSummary(pending.Render())
			if want := styledSummary(step.want); got != want {
				t.Fatalf("prefix=%d step=%d chunk=%q\n got %s\nwant %s", len(prefix), index, step.chunk, got, want)
			}
		}
	}
}

func TestOverwrittenColorsDoNotConsumeThePreviewBudgetLikeRust(t *testing.T) {
	raw := strings.Repeat("\x1b[31m.\x1b[39m\r", 1100) + "Done!"
	if got := CommandOutputLine(raw); styledSummary(got) != styledSummary("Done!") {
		t.Fatalf("command output line = %q", got)
	}
	pending := &PendingLine{}
	pending.PushLines(raw)
	if got := PlainOutputText(pending.Render()); got != "Done!" {
		t.Fatalf("pending plain text = %q", got)
	}
	output := CommandOutput{ExitCode: 0, AggregatedOutput: raw}
	lines := OutputLinesFor(&output, OutputLinesParams{LineLimit: 5})
	if len(lines.Lines) != 1 || styledSummary(lines.Lines[0]) != styledSummary("Done!") {
		t.Fatalf("preview lines = %#v", lines.Lines)
	}
}

func TestCommandPreviewMatchesStreamedAndCompletedOutputLikeRust(t *testing.T) {
	cell := NewActiveExecCommand("call-preview", []string{"bash", "-lc", "echo output"}, nil, ExecSourceAgent, "", false)
	output := "first\nsecond\nthird\nupdating\r\x1b[Kfourth\r\nfifth\n"
	cell.AppendOutput("call-preview", output)
	live := cell.DisplayLines(80)
	cell.CompleteCall("call-preview", CommandOutput{ExitCode: 0, AggregatedOutput: output, FormattedOutput: output}, time.Duration(0))
	completed := cell.DisplayLines(80)
	if len(live) < 2 || len(completed) < 2 {
		t.Fatalf("live=%#v completed=%#v", live, completed)
	}
	if strings.Join(live[1:], "\n") != strings.Join(completed[1:], "\n") {
		t.Fatalf("live preview differs from completed preview\nlive=%#v\ncompleted=%#v", live, completed)
	}
	joined := strings.Join(completed, "\n")
	if strings.Contains(joined, "updating") {
		t.Fatalf("completed preview = %#v", completed)
	}
	transcript := cell.TranscriptLines(80)
	if strings.Contains(strings.Join(transcript, "\n"), "updating") ||
		!strings.Contains(strings.Join(transcript, "\n"), "fourth") ||
		!strings.Contains(strings.Join(transcript, "\n"), "fifth") {
		t.Fatalf("transcript = %#v", transcript)
	}
}

func TestSplitCommandOutputLinesResolvesEachRowLikeRust(t *testing.T) {
	lines := splitCommandOutputLines("first line\n  indented output\r\nlong stale message\r\x1b[Klast line")
	want := []string{"first line", "  indented output", "last line"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %#v", lines)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Fatalf("line %d = %q, want %q", index, lines[index], want[index])
		}
	}
}

func TestPendingLineKeepsPartialRowsAcrossChunksLikeRust(t *testing.T) {
	pending := &PendingLine{}
	if rows := pending.PushLines("starting\n\x1b]0;Building\x07Warning\n10%\r50%\r\x1b["); len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if got := PlainOutputText(pending.Render()); got != "50%" {
		t.Fatalf("pending after split erase = %q", got)
	}
	pending.PushLines("K\x1b]0;Done\x1b")
	if got := PlainOutputText(pending.Render()); got != "" {
		t.Fatalf("pending after erase-to-end = %q", got)
	}
	pending.PushLines("\\100%\r")
	if got := PlainOutputText(pending.Render()); got != "100%" {
		t.Fatalf("pending after completion = %q", got)
	}
	rows := pending.PushLines("\nDone\ncleared\r\x1b[K")
	if len(rows) != 2 || PlainOutputText(rows[0]) != "100%" || rows[1] != "Done" {
		t.Fatalf("completed rows = %#v", rows)
	}
	if got := PlainOutputText(pending.Render()); got != "" {
		t.Fatalf("pending after cleared row = %q", got)
	}
}
