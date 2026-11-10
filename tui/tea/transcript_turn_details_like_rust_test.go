package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func turnDetailToolMessage(id string, text string) codextui.Message {
	return codextui.Message{
		Role:        codextui.RoleHistory,
		Text:        text,
		RawText:     text,
		ActivityIDs: []string{id},
	}
}

// TestCollapseTurnDetailsSummarizesCompletedTurnLikeRust covers Rust
// #60554/#60556: consecutive tool activity inside a completed turn becomes one
// disclosure row that carries invocation labels only, never output.
func TestCollapseTurnDetailsSummarizesCompletedTurnLikeRust(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleUser, Text: "please inspect the repo"},
		turnDetailToolMessage("exec-1", "Ran ls\n  \u2514 a.go"),
		turnDetailToolMessage("exec-2", "Ran go vet\n  \u2514 clean"),
		turnDetailToolMessage("exec-3", "Ran go build\n  \u2514 ok"),
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	collapsed := CollapseTurnDetails(messages, nil)
	if collapsed == nil {
		t.Fatal("completed turn activity was not collapsed")
	}
	if len(collapsed) != len(messages) {
		t.Fatalf("collapsed slice keeps positions: got %d messages, want %d", len(collapsed), len(messages))
	}
	if !collapsed[1].TurnDetailSummary {
		t.Fatalf("first tool row is not a summary: %#v", collapsed[1])
	}
	if got, want := collapsed[1].Text, "\u25b8 Ran 3 commands"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if strings.Contains(collapsed[1].Text, "a.go") || strings.Contains(collapsed[1].Text, "ok") {
		t.Fatalf("summary leaked tool output: %q", collapsed[1].Text)
	}
	for _, index := range []int{2, 3} {
		if strings.TrimSpace(collapsed[index].Text) != "" || len(collapsed[index].ActivityIDs) != 0 {
			t.Fatalf("absorbed row %d was not blanked: %#v", index, collapsed[index])
		}
	}
	if collapsed[0].Text != "please inspect the repo" || collapsed[4].Text != "done" {
		t.Fatalf("prompt or answer changed: %#v", collapsed)
	}
}

// TestTurnDetailSummaryLabelMixLikeRust covers Rust's ToolGroup::label counts:
// reads and searches count as exploring, everything else as running.
func TestTurnDetailSummaryLabelMixLikeRust(t *testing.T) {
	if got, want := TurnDetailSummaryLabel([]string{"Read a.go", "Ran go build"}), "Explored files \u00b7 ran 1 command"; got != want {
		t.Fatalf("mixed label = %q, want %q", got, want)
	}
	if got, want := TurnDetailSummaryLabel([]string{"Read a.go", "Searching for foo"}), "Explored files"; got != want {
		t.Fatalf("explore label = %q, want %q", got, want)
	}
	if got, want := TurnDetailSummaryLabel([]string{"Read a.go"}), "Read a.go"; got != want {
		t.Fatalf("single label = %q, want %q", got, want)
	}
}

// TestCollapseTurnDetailsKeepsActiveTurnLikeRust covers Rust #60557's
// completion rule: the live tail keeps its activity until the turn settles.
func TestCollapseTurnDetailsKeepsActiveTurnLikeRust(t *testing.T) {
	active := []codextui.Message{
		{Role: codextui.RoleUser, Text: "run it"},
		turnDetailToolMessage("exec-1", "Ran ls"),
		turnDetailToolMessage("exec-2", "Ran pwd"),
	}
	if collapsed := CollapseTurnDetails(active, nil); collapsed != nil {
		t.Fatalf("active turn was collapsed: %#v", collapsed)
	}

	commentaryOnly := []codextui.Message{
		{Role: codextui.RoleUser, Text: "run it"},
		turnDetailToolMessage("exec-1", "Ran ls"),
		{Role: codextui.RoleAssistant, Text: "working on it", Phase: "commentary"},
	}
	if collapsed := CollapseTurnDetails(commentaryOnly, nil); collapsed != nil {
		t.Fatalf("turn with only commentary was collapsed: %#v", collapsed)
	}
}

// TestCollapseTurnDetailsKeepsExpandedGroupLikeRust covers the disclosure
// control: expanding a group restores the original cells.
func TestCollapseTurnDetailsKeepsExpandedGroupLikeRust(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleUser, Text: "run it"},
		turnDetailToolMessage("exec-1", "Ran ls\n  \u2514 a.go"),
		turnDetailToolMessage("exec-2", "Ran pwd"),
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	expanded := func(index int) bool { return index == 2 }
	if collapsed := CollapseTurnDetails(messages, expanded); collapsed != nil {
		t.Fatalf("expanded group was collapsed: %#v", collapsed)
	}
}

// TestCollapseTurnDetailsIgnoresNonToolHistoryLikeRust keeps notices and other
// history cells out of the summary.
func TestCollapseTurnDetailsIgnoresNonToolHistoryLikeRust(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleUser, Text: "run it"},
		{Role: codextui.RoleHistory, Text: "Warning: sandbox unavailable"},
		turnDetailToolMessage("exec-1", "Ran ls"),
		turnDetailToolMessage("exec-2", "Ran pwd"),
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	collapsed := CollapseTurnDetails(messages, nil)
	if collapsed == nil {
		t.Fatal("tool activity after a notice was not collapsed")
	}
	if collapsed[1].Text != "Warning: sandbox unavailable" {
		t.Fatalf("notice was rewritten: %#v", collapsed[1])
	}
	if !collapsed[2].TurnDetailSummary || strings.TrimSpace(collapsed[3].Text) != "" {
		t.Fatalf("tool run after a notice did not fold: %#v", collapsed)
	}
}

// TestCollapseTurnDetailsSingleCallKeepsItsLabelLikeRust keeps a lone call's own
// invocation label instead of counts.
func TestCollapseTurnDetailsSingleCallKeepsItsLabelLikeRust(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleUser, Text: "run it"},
		turnDetailToolMessage("exec-1", "Ran go test ./...\n  \u2514 ok"),
		{Role: codextui.RoleAssistant, Text: "done"},
	}
	collapsed := CollapseTurnDetails(messages, nil)
	if collapsed == nil {
		t.Fatal("single call was not summarized")
	}
	if got, want := collapsed[1].Text, "\u25b8 Ran go test ./..."; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

// TestTranscriptOverlayCollapsesCompletedTurnActivityLikeRust exercises the
// wired full-screen path: the pager shows the summary row and hides output until
// the group is expanded.
func TestTranscriptOverlayCollapsesCompletedTurnActivityLikeRust(t *testing.T) {
	enabled := true
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleUser, "inspect the repo")
	state.AddHistoryMessage(turnDetailToolMessage("exec-1", "Ran ls\n  \u2514 tool-output-one"))
	state.AddHistoryMessage(turnDetailToolMessage("exec-2", "Ran pwd\n  \u2514 tool-output-two"))
	state.AddMessage(codextui.RoleAssistant, "finished")
	model := NewModel(state, Options{Width: 80, Height: 20, CollapseTurnDetails: &enabled})
	content := utils.StripANSI(model.renderTranscriptOverlayCached())
	if !strings.Contains(content, "\u25b8 Ran 2 commands") {
		t.Fatalf("full-screen transcript lost the summary row:\n%s", content)
	}
	if strings.Contains(content, "tool-output-one") || strings.Contains(content, "tool-output-two") {
		t.Fatalf("full-screen transcript kept folded output:\n%s", content)
	}
	index := model.transcriptActivityMessageForIDs([]string{"exec-1"})
	if index < 0 {
		t.Fatalf("activity row is not addressable")
	}
	model.activityFocus.expanded = map[string]bool{}
	for _, key := range model.transcriptActivityKeys(index) {
		model.activityFocus.expanded[key] = true
	}
	expanded := utils.StripANSI(model.renderTranscriptOverlayCached())
	if !strings.Contains(expanded, "tool-output-one") || strings.Contains(expanded, "\u25b8 Ran 2 commands") {
		t.Fatalf("expanded transcript did not restore the details:\n%s", expanded)
	}
}
