package execcell

import (
	"strings"
	"testing"
	"time"
)

func TestExecCellModelLifecycle(t *testing.T) {
	start := time.Unix(0, 0)
	cell := NewExecCell(ExecCall{
		CallID:    "read-1",
		Command:   []string{"cat", "README.md"},
		Parsed:    []ParsedCommand{{Kind: ParsedRead, Name: "README.md"}},
		Source:    ExecSourceAgent,
		StartTime: &start,
	}, false)
	if !cell.IsExploringCell() || !cell.IsActive() || cell.ShouldFlush() {
		t.Fatalf("initial cell = %#v", cell)
	}

	next, ok := cell.WithAddedCall("search-1", []string{"rg", "TODO"}, []ParsedCommand{{Kind: ParsedSearch, Query: "TODO"}}, ExecSourceAgent, "")
	if !ok || len(next.Calls) != 2 {
		t.Fatalf("WithAddedCall = %#v ok=%v", next, ok)
	}
	if _, ok := next.WithAddedCall("run-1", []string{"go", "test"}, []ParsedCommand{{Kind: ParsedUnknown, Cmd: "go test"}}, ExecSourceAgent, ""); ok {
		t.Fatal("non-exploring call joined exploring cell")
	}

	if !next.AppendOutput("search-1", "chunk") || next.Calls[1].Output.AggregatedOutput != "chunk" {
		t.Fatalf("append output = %#v", next.Calls[1].Output)
	}
	duration := 2 * time.Second
	if !next.CompleteCall("search-1", CommandOutput{ExitCode: 0}, duration) {
		t.Fatal("CompleteCall returned false")
	}
	if next.Calls[1].Output == nil || next.Calls[1].Duration == nil || next.Calls[1].StartTime != nil {
		t.Fatalf("completed call = %#v", next.Calls[1])
	}
	if next.CompleteCall("missing", CommandOutput{}, duration) {
		t.Fatal("missing CompleteCall returned true")
	}

	next.MarkFailed()
	if next.Calls[0].Output == nil || next.Calls[0].Output.ExitCode != 1 {
		t.Fatalf("MarkFailed = %#v", next.Calls[0])
	}
}

func TestOutputLinesTruncatesWithTranscriptHint(t *testing.T) {
	output := &CommandOutput{
		ExitCode:         0,
		AggregatedOutput: "1\n2\n3\n4\n5\n6\n7\n",
	}
	lines := OutputLinesFor(output, OutputLinesParams{LineLimit: 2})
	if lines.Omitted == nil || *lines.Omitted != 3 {
		t.Fatalf("omitted = %#v", lines.Omitted)
	}
	joined := strings.Join(lines.Lines, "\n")
	if !strings.Contains(joined, "… +3 lines (ctrl + t to view transcript)") {
		t.Fatalf("output lines:\n%s", joined)
	}
	if got := OutputLinesFor(&CommandOutput{ExitCode: 0, AggregatedOutput: "ok"}, OutputLinesParams{OnlyErr: true}); len(got.Lines) != 0 {
		t.Fatalf("OnlyErr success output = %#v", got)
	}
}

func TestExecCellDisplayAndTranscript(t *testing.T) {
	duration := time.Second
	cell := NewExecCell(ExecCall{
		CallID:  "call-1",
		Command: []string{"bash", "-lc", "echo hello"},
		Output: &CommandOutput{
			ExitCode:         0,
			AggregatedOutput: "hello\n",
			FormattedOutput:  "hello\n",
		},
		Source:   ExecSourceUserShell,
		Duration: &duration,
	}, false)
	display := strings.Join(cell.DisplayLines(80), "\n")
	if !strings.Contains(display, "✓ You ran echo hello") || !strings.Contains(display, "└ hello") {
		t.Fatalf("display:\n%s", display)
	}
	transcript := strings.Join(cell.TranscriptLines(80), "\n")
	if !strings.Contains(transcript, "$ echo hello") || !strings.Contains(transcript, "✓ - 1s") {
		t.Fatalf("transcript:\n%s", transcript)
	}
	if !cell.ShouldFlush() {
		t.Fatal("completed non-exploring cell should flush")
	}
}

func TestExecCellExploringDisplayKeepsLongURLIntact(t *testing.T) {
	urlLike := "example.test/api/v1/projects/alpha-team/releases/2026-02-17/builds/1234567890/artifacts/reports/performance/summary/detail/with/a/very/long/path"
	cell := NewExecCell(ExecCall{
		CallID: "search",
		Parsed: []ParsedCommand{{
			Kind:  ParsedSearch,
			Query: urlLike,
		}},
		Source: ExecSourceAgent,
	}, false)
	rendered := cell.DisplayLines(36)
	count := 0
	for _, line := range rendered {
		if strings.Contains(line, urlLike) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected full URL-like query once, got %d lines=%#v", count, rendered)
	}
}

func TestUnifiedExecInteractionFormatting(t *testing.T) {
	input := strings.Repeat("x", 100) + "\n`"
	summary := SummarizeInteractionInput(input)
	if !strings.HasSuffix(summary, "...") || strings.Contains(summary, "\n") || strings.Contains(summary, "`") {
		t.Fatalf("summary = %q", summary)
	}
	waited := FormatUnifiedExecInteraction([]string{"bash", "-lc", "python server.py"}, "")
	if waited != "Waited for `python server.py`" {
		t.Fatalf("waited = %q", waited)
	}
	interacted := FormatUnifiedExecInteraction([]string{"bash", "-lc", "python server.py"}, "q")
	if interacted != "Interacted with `python server.py`, sent `q`" {
		t.Fatalf("interacted = %q", interacted)
	}
}
