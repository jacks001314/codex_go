package execcell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/utils"
)

func TestAppendOutputKeepsBoundedTail(t *testing.T) {
	cell := NewExecCell(ExecCall{CallID: "call"}, false)
	chunk := strings.Repeat("x", MaxLiveOutputBytes+32)
	if !cell.AppendOutput("call", chunk) {
		t.Fatal("append failed")
	}
	output := cell.Calls[0].Output
	if len(output.AggregatedOutput) != MaxLiveOutputBytes || !output.LiveOutputTruncated {
		t.Fatalf("output len=%d truncated=%v", len(output.AggregatedOutput), output.LiveOutputTruncated)
	}
}

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

func TestOutputLinesNormalizesPowerShellCRLF(t *testing.T) {
	lines := OutputLinesFor(&CommandOutput{AggregatedOutput: "alpha\r\nbeta\r\n"}, OutputLinesParams{LineLimit: 5})
	if got := strings.Join(lines.Lines, "|"); got != "alpha|beta" {
		t.Fatalf("CRLF output = %q, want alpha|beta", got)
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
	if !strings.Contains(display, "• You ran echo hello") || !strings.Contains(display, "└ hello") {
		t.Fatalf("display:\n%s", display)
	}
	transcript := strings.Join(cell.TranscriptLines(80), "\n")
	if !strings.Contains(transcript, "$ echo hello") || !strings.Contains(transcript, "✓ - 1s") {
		t.Fatalf("transcript:\n%s", transcript)
	}
	if !cell.ShouldFlush() {
		t.Fatal("completed non-exploring cell should flush")
	}

	if got := StripShellCommand([]string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem"}); got != "Get-ChildItem" {
		t.Fatalf("powershell strip = %q", got)
	}
	if got := StripShellCommand([]string{"fish", "-lc", "echo hello"}); got != "fish -lc 'echo hello'" {
		t.Fatalf("fish should not be stripped like Rust tui exec display: %q", got)
	}
	if got := StripShellCommand([]string{"foo", "bar baz", "weird&stuff"}); got != "foo 'bar baz' 'weird&stuff'" {
		t.Fatalf("fallback quoting = %q", got)
	}
}

func TestExecCellDisplayMatchesRustContinuationAndOutputLayout(t *testing.T) {
	cell := NewExecCell(ExecCall{
		CallID:  "call-1",
		Command: []string{"pwsh", "-NoProfile", "-Command", "Get-NetIPConfiguration | Format-List InterfaceAlias,InterfaceIndex,NetProfile.Name,IPv4Address,IPv6Address,IPv4DefaultGateway,IPv6DefaultGateway,DNSServer; Get-NetAdapter | Format-Table -AutoSize Name,InterfaceDescription,Status,MacAddress,LinkSpeed"},
		Output: &CommandOutput{
			ExitCode:         0,
			AggregatedOutput: "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\n",
		},
		Source: ExecSourceAgent,
	}, false)

	clean := strings.Split(utils.StripANSI(strings.Join(cell.DisplayLinesWithTheme(72, "dracula"), "\n")), "\n")
	if len(clean) == 0 || !strings.HasPrefix(clean[0], "• Ran ") {
		t.Fatalf("Rust-style command header missing: %#v", clean)
	}
	continuations := 0
	outputRows := 0
	for _, line := range clean[1:] {
		if strings.HasPrefix(line, "  │ ") {
			continuations++
		}
		if strings.HasPrefix(line, "  └ ") || strings.HasPrefix(line, "    ") {
			outputRows++
		}
	}
	if continuations != 3 {
		t.Fatalf("expected two command continuations plus one ellipsis, got %d: %#v", continuations, clean)
	}
	if outputRows > ToolCallMaxLines {
		t.Fatalf("output rows = %d, want at most %d: %#v", outputRows, ToolCallMaxLines, clean)
	}
	if !strings.Contains(strings.Join(clean, "\n"), transcriptHint) {
		t.Fatalf("truncated output should advertise transcript: %#v", clean)
	}
}

func TestExecCellDisplayWithThemeHighlightsCommandAndKeepsRawPlain(t *testing.T) {
	duration := time.Second
	cell := NewExecCell(ExecCall{
		CallID:  "call-1",
		Command: []string{"bash", "-lc", "echo 'hello'"},
		Output: &CommandOutput{
			ExitCode:         0,
			AggregatedOutput: "hello\n",
			FormattedOutput:  "hello\n",
		},
		Source:   ExecSourceAgent,
		Duration: &duration,
	}, false)

	display := strings.Join(cell.DisplayLinesWithTheme(80, "dracula"), "\n")
	if !strings.Contains(display, "\x1b[") {
		t.Fatalf("themed display should include ANSI styling:\n%q", display)
	}
	clean := utils.StripANSI(display)
	if !strings.Contains(clean, "Ran echo 'hello'") || !strings.Contains(clean, "hello") {
		t.Fatalf("stripped themed display lost content:\n%s", clean)
	}
	raw := strings.Join(cell.RawLines(), "\n")
	if strings.Contains(raw, "\x1b[") {
		t.Fatalf("raw lines should stay unstyled:\n%q", raw)
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
	zsh := FormatUnifiedExecInteraction([]string{"zsh", "-c", "echo hi"}, "")
	if zsh != "Waited for `echo hi`" {
		t.Fatalf("zsh interaction = %q", zsh)
	}
	powershell := FormatUnifiedExecInteraction([]string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem"}, "")
	if powershell != "Waited for `pwsh -NoProfile -Command Get-ChildItem`" {
		t.Fatalf("powershell interaction should use Rust join fallback: %q", powershell)
	}
}

func TestExecCellCompactGroupGroupsSuccessesAndPreservesTranscript(t *testing.T) {
	cell := NewExecCell(ExecCall{
		CallID:  "call-first",
		Command: []string{"bash", "-lc", "printf first"},
		Source:  ExecSourceAgent,
	}, false)
	if !cell.CompleteCall("call-first", CommandOutput{ExitCode: 0, AggregatedOutput: "first\n", FormattedOutput: "first\n"}, time.Second) {
		t.Fatal("first CompleteCall failed")
	}
	if cell.ShouldFlush() {
		t.Fatal("a single completed groupable command should stay un-flushed")
	}

	next, ok := cell.WithAddedCall("call-second", []string{"bash", "-lc", "printf second"}, nil, ExecSourceAgent, "")
	if !ok {
		t.Fatal("second groupable call should join the compact group")
	}
	display := strings.Join(next.DisplayLines(80), "\n")
	if !strings.Contains(display, "• Ran 1 command · ctrl + t to view transcript") || !strings.Contains(display, "Running printf second") {
		t.Fatalf("active compact group display:\n%s", display)
	}
	if !next.CompleteCall("call-second", CommandOutput{ExitCode: 0, AggregatedOutput: "second\n", FormattedOutput: "second\n"}, time.Second) {
		t.Fatal("second CompleteCall failed")
	}
	if next.ShouldFlush() {
		t.Fatal("compact group with all successes should stay un-flushed")
	}
	display = strings.Join(next.DisplayLines(80), "\n")
	if !strings.Contains(display, "• Ran 2 commands · ctrl + t to view transcript") || strings.Contains(display, "printf second") {
		t.Fatalf("completed compact group display:\n%s", display)
	}
	transcript := strings.Join(next.TranscriptLines(80), "\n")
	if !strings.Contains(transcript, "$ printf first") || !strings.Contains(transcript, "$ printf second") {
		t.Fatalf("compact group must preserve the full transcript:\n%s", transcript)
	}
}

func TestExecCellCompactGroupIncludesUnifiedExecStartup(t *testing.T) {
	cell := NewExecCell(ExecCall{
		CallID:  "call-agent",
		Command: []string{"bash", "-lc", "echo agent"},
		Source:  ExecSourceAgent,
	}, false)
	if !cell.CompleteCall("call-agent", CommandOutput{ExitCode: 0}, time.Second) {
		t.Fatal("agent CompleteCall failed")
	}
	next, ok := cell.WithAddedCall("call-startup", []string{"bash", "-lc", "echo startup"}, nil, ExecSourceUnifiedExecStartup, "")
	if !ok {
		t.Fatal("unified exec startup should join the compact group")
	}
	if !next.CompleteCall("call-startup", CommandOutput{ExitCode: 0}, time.Second) {
		t.Fatal("startup CompleteCall failed")
	}
	if next.ShouldFlush() {
		t.Fatal("agent + unified exec startup successes should stay un-flushed")
	}
	display := strings.Join(next.DisplayLines(80), "\n")
	if !strings.Contains(display, "• Ran 2 commands · ctrl + t to view transcript") {
		t.Fatalf("unified exec startup not grouped:\n%s", display)
	}
}

func TestExecCellCompactGroupKeepsFailuresAndManualCommandsVisible(t *testing.T) {
	cell := NewExecCell(ExecCall{
		CallID:  "call-first",
		Command: []string{"bash", "-lc", "printf first"},
		Source:  ExecSourceAgent,
	}, false)
	if !cell.CompleteCall("call-first", CommandOutput{ExitCode: 0, AggregatedOutput: "first\n"}, time.Second) {
		t.Fatal("first CompleteCall failed")
	}
	next, ok := cell.WithAddedCall("call-broken", []string{"bash", "-lc", "printf broken"}, nil, ExecSourceAgent, "")
	if !ok {
		t.Fatal("groupable call should join even when it later fails")
	}
	if !next.CompleteCall("call-broken", CommandOutput{ExitCode: 1, AggregatedOutput: "broken\n", FormattedOutput: "broken\n"}, time.Second) {
		t.Fatal("broken CompleteCall failed")
	}
	if !next.ShouldFlush() {
		t.Fatal("compact group with a failure must flush")
	}
	display := strings.Join(next.DisplayLines(80), "\n")
	if !strings.Contains(display, "• Ran 1 command · ctrl + t to view transcript") || !strings.Contains(display, "Ran printf broken") {
		t.Fatalf("failed command must stay visible:\n%s", display)
	}

	// A manual shell command never joins an inactive compact group.
	if _, ok := cell.WithAddedCall("call-manual", []string{"bash", "-lc", "printf manual"}, nil, ExecSourceUserShell, ""); ok {
		t.Fatal("manual shell command must not join an inactive compact group")
	}
}

func TestExecCellCompactGroupFlushesAtGroupLimit(t *testing.T) {
	cell := NewExecCell(ExecCall{
		CallID:  "call-0",
		Command: []string{"bash", "-lc", "echo 0"},
		Source:  ExecSourceAgent,
	}, false)
	if !cell.CompleteCall("call-0", CommandOutput{ExitCode: 0}, time.Second) {
		t.Fatal("call-0 CompleteCall failed")
	}
	for i := 1; i < MaxGroupedCommands; i++ {
		next, ok := cell.WithAddedCall(fmt.Sprintf("call-%d", i), []string{"bash", "-lc", fmt.Sprintf("echo %d", i)}, nil, ExecSourceAgent, "")
		if !ok {
			t.Fatalf("call-%d did not join below the group limit", i)
		}
		cell = next
		if !cell.CompleteCall(fmt.Sprintf("call-%d", i), CommandOutput{ExitCode: 0}, time.Second) {
			t.Fatalf("call-%d CompleteCall failed", i)
		}
		if i < MaxGroupedCommands-1 && cell.ShouldFlush() {
			t.Fatalf("call-%d flushed before the group limit", i)
		}
	}
	if !cell.ShouldFlush() {
		t.Fatal("compact group at the 32-command limit must flush")
	}
	if _, ok := cell.WithAddedCall("call-over", []string{"bash", "-lc", "echo over"}, nil, ExecSourceAgent, ""); ok {
		t.Fatal("compact group over the limit must reject new calls")
	}
}
