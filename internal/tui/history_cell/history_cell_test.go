package historycell

import (
	"strings"
	"testing"
)

func TestBaseCellsDisplayRawAndHyperlinks(t *testing.T) {
	plain := NewPlainHistoryCell([]string{"one", "two"})
	if got := strings.Join(plain.DisplayLines(10), "|"); got != "one|two" {
		t.Fatalf("plain display = %q", got)
	}
	if got := strings.Join(plain.RawLines(), "|"); got != "one|two" {
		t.Fatalf("plain raw = %q", got)
	}

	wrapped := NewPrefixedWrappedHistoryCell("hello world from codex", "> ", "  ")
	lines := wrapped.DisplayLines(12)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "> ") {
		t.Fatalf("wrapped lines = %#v", lines)
	}

	web := NewWebHyperlinkHistoryCell([]string{"see https://example.com/a."})
	links := web.DisplayHyperlinkLines(80)
	if len(links) != 1 || len(links[0].Links) != 1 || links[0].Links[0].Destination != "https://example.com/a" {
		t.Fatalf("hyperlinks = %#v", links)
	}

	composite := NewCompositeHistoryCell([]HistoryCell{
		NewPlainHistoryCell([]string{"a"}),
		NewPlainHistoryCell([]string{"b"}),
	})
	if got := strings.Join(composite.DisplayLines(80), "|"); got != "a||b" {
		t.Fatalf("composite display = %q", got)
	}
	if got := strings.Join(composite.RawLines(), "|"); got != "a||b" {
		t.Fatalf("composite raw = %q", got)
	}
}

func TestUserAndAgentMessageCells(t *testing.T) {
	user := NewUserPrompt("hello\nworld\n", nil, nil, []string{"https://example.com/image.png", "https://example.com/2.png"})
	display := user.DisplayLines(40)
	joined := strings.Join(display, "\n")
	for _, want := range []string{"[image]", "[image 2]", "• hello", "  world"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("user display missing %q:\n%s", want, joined)
		}
	}
	raw := strings.Join(user.RawLines(), "\n")
	if !strings.Contains(raw, "hello\nworld") || !strings.Contains(raw, "[image 2]") {
		t.Fatalf("user raw = %q", raw)
	}

	agent := NewAgentMessageCell([]string{"assistant says hello", "second line"}, true)
	agentDisplay := strings.Join(agent.DisplayLines(16), "\n")
	if !strings.Contains(agentDisplay, "• assistant") || !strings.Contains(agentDisplay, "  second line") {
		t.Fatalf("agent display:\n%s", agentDisplay)
	}
	if got := strings.Join(agent.RawLines(), "|"); got != "assistant says hello|second line" {
		t.Fatalf("agent raw = %q", got)
	}

	reasoning := NewReasoningSummaryCell("thinking through files", false)
	if got := strings.Join(reasoning.DisplayLines(12), "\n"); !strings.Contains(got, "• thinking") {
		t.Fatalf("reasoning display = %q", got)
	}
	transcriptOnly := NewReasoningSummaryCell("hidden", true)
	if len(transcriptOnly.DisplayLines(80)) != 0 || len(transcriptOnly.RawLines()) != 0 {
		t.Fatalf("transcript only reasoning leaked display/raw")
	}
}

func TestPlanCells(t *testing.T) {
	cell := NewPlanUpdate("Need to update TUI.", []PlanItemArg{
		{Step: "scan Rust modules", Status: StepCompleted},
		{Step: "port cells", Status: StepInProgress},
		{Step: "write snapshots", Status: StepPending},
	})
	display := strings.Join(cell.DisplayLines(40), "\n")
	for _, want := range []string{"Updated Plan", "✓ scan Rust modules", "▶ port cells", "□ write snapshots"} {
		if !strings.Contains(display, want) {
			t.Fatalf("plan display missing %q:\n%s", want, display)
		}
	}
	raw := strings.Join(cell.RawLines(), "\n")
	for _, want := range []string{"Completed: scan Rust modules", "InProgress: port cells", "Pending: write snapshots"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("plan raw missing %q:\n%s", want, raw)
		}
	}

	empty := NewPlanUpdate("", nil)
	if got := strings.Join(empty.DisplayLines(40), "\n"); !strings.Contains(got, "(no steps provided)") {
		t.Fatalf("empty plan display = %q", got)
	}

	proposed := NewProposedPlan("- one\n- two")
	proposedDisplay := strings.Join(proposed.DisplayLines(40), "\n")
	if !strings.Contains(proposedDisplay, "Proposed Plan") || !strings.Contains(proposedDisplay, "- one") {
		t.Fatalf("proposed display:\n%s", proposedDisplay)
	}
	if got := strings.Join(proposed.RawLines(), "|"); got != "- one|- two" {
		t.Fatalf("proposed raw = %q", got)
	}
}

func TestExecHistoryCells(t *testing.T) {
	waited := NewUnifiedExecInteraction("go test ./...", "")
	if got := strings.Join(waited.DisplayLines(80), "\n"); !strings.Contains(got, "Waited for background terminal") || !strings.Contains(got, "go test ./...") {
		t.Fatalf("waited display = %q", got)
	}
	if got := strings.Join(waited.RawLines(), "|"); got != "Waited for background terminal: go test ./..." {
		t.Fatalf("waited raw = %q", got)
	}

	interaction := NewUnifiedExecInteraction("python", "print(1)\nprint(2)")
	display := strings.Join(interaction.DisplayLines(80), "\n")
	if !strings.Contains(display, "Interacted with background terminal") || !strings.Contains(display, "└ print(1)") {
		t.Fatalf("interaction display:\n%s", display)
	}
	raw := strings.Join(interaction.RawLines(), "\n")
	if !strings.Contains(raw, "Interacted with background terminal: python") || !strings.Contains(raw, "print(2)") {
		t.Fatalf("interaction raw:\n%s", raw)
	}

	processes := make([]UnifiedExecProcessDetails, 18)
	for i := range processes {
		processes[i] = UnifiedExecProcessDetails{
			CommandDisplay: "command " + string(rune('a'+i)),
			RecentChunks:   []string{"chunk"},
		}
	}
	cell := NewUnifiedExecProcessesOutput(processes)
	lines := strings.Join(cell.DisplayLines(40), "\n")
	if !strings.Contains(lines, "/ps") || !strings.Contains(lines, "Background terminals") || !strings.Contains(lines, "... and 2 more running") {
		t.Fatalf("process lines:\n%s", lines)
	}
}
