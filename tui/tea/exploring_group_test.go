package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// These tests cover the live exploring-group accumulation (Rust #46565): the
// tea Model keeps one active exploring ExecCell, merges adjacent read/list/
// search commands into it, keeps intervening reasoning in transcript order, and
// closes the group on a non-exploring command.

func exploringModel() *Model {
	return NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
}

func groupMessageWith(model *Model, needle string) (codextui.Message, bool) {
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, needle) {
			return message, true
		}
	}
	return codextui.Message{}, false
}

func startedCommandItem(id string, command string) *protocol.ThreadItem {
	item := protocol.CommandExecutionItem(id, command, "", nil, "in_progress")
	return &item
}

func completedCommandItem(id string, command string, output string) *protocol.ThreadItem {
	exitCode := 0
	item := protocol.CommandExecutionItem(id, command, output, &exitCode, "completed")
	return &item
}

func TestExploringCommandsGroupIntoOneCellLikeRust(t *testing.T) {
	model := exploringModel()
	model.applyItemStarted(startedCommandItem("read-1", "cat README.md"), 0)
	model.applyItemStarted(startedCommandItem("list-1", "ls src"), 0)

	view := model.View()
	if !strings.Contains(view, "Read README.md") || !strings.Contains(view, "List src") {
		t.Fatalf("exploring group should contain both parsed actions:\n%s", view)
	}
	if n := strings.Count(view, "Exploring"); n != 1 {
		t.Fatalf("exploring groups = %d, want 1 (adjacent calls share one cell):\n%s", n, view)
	}
	if strings.Contains(view, "Running cat README.md") || strings.Contains(view, "Running ls src") {
		t.Fatalf("exploring commands leaked the generic running label:\n%s", view)
	}
}

func TestExploringGroupKeepsReasoningInOrderLikeRust(t *testing.T) {
	model := exploringModel()
	model.applyItemStarted(startedCommandItem("read-1", "cat README.md"), 0)
	model.applyItemCompleted(&protocol.ThreadItem{
		ID:      "reason-1",
		Type:    "reasoning",
		Summary: []string{"Looking at the readme"},
	})
	model.applyItemStarted(startedCommandItem("list-1", "ls src"), 0)

	message, ok := groupMessageWith(model, "Read README.md")
	if !ok {
		t.Fatalf("exploring group message missing:\n%s", model.View())
	}
	if !strings.Contains(message.Text, "List src") {
		t.Fatalf("second exploring call did not join the group: %q", message.Text)
	}
	transcript := message.TranscriptText
	readIndex := strings.Index(transcript, "cat README.md")
	// The rendered block wraps and styles the summary, so match a contiguous
	// fragment of it.
	reasoningIndex := strings.Index(transcript, "Looking at the")
	listIndex := strings.Index(transcript, "ls src")
	if readIndex < 0 || reasoningIndex < 0 || listIndex < 0 {
		t.Fatalf("transcript missing ordered parts (read=%d reasoning=%d list=%d):\n%s", readIndex, reasoningIndex, listIndex, transcript)
	}
	if !(readIndex < reasoningIndex && reasoningIndex < listIndex) {
		t.Fatalf("reasoning not kept in transcript order between the calls:\n%s", transcript)
	}
	// The reasoning is attached to the group, so it must not also be committed
	// as its own visible history entry.
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, "Looking at the") && !strings.Contains(message.Text, "Read README.md") {
			t.Fatalf("reasoning leaked into a standalone history entry: %q", message.Text)
		}
	}
}

func TestExploringGroupEndsOnNonExploringCommandLikeRust(t *testing.T) {
	model := exploringModel()
	model.applyItemStarted(startedCommandItem("read-1", "cat README.md"), 0)
	model.applyItemStarted(startedCommandItem("list-1", "ls src"), 0)
	model.applyItemStarted(startedCommandItem("run-1", "go test ./..."), 0)

	view := model.View()
	if !strings.Contains(view, "Read README.md") || !strings.Contains(view, "List src") {
		t.Fatalf("exploring group lost its actions after a later command:\n%s", view)
	}
	if !strings.Contains(view, "Running go test ./...") {
		t.Fatalf("non-exploring command did not render its own cell:\n%s", view)
	}
	if n := strings.Count(view, "Exploring") + strings.Count(view, "Explored"); n != 1 {
		t.Fatalf("exploring groups = %d, want 1:\n%s", n, view)
	}
}

func TestExploringGroupClosesOnCompletionLikeRust(t *testing.T) {
	model := exploringModel()
	model.applyItemStarted(startedCommandItem("read-1", "cat README.md"), 0)
	model.applyItemStarted(startedCommandItem("list-1", "ls src"), 0)
	model.applyItemCompleted(completedCommandItem("read-1", "cat README.md", "contents\n"))
	model.applyItemCompleted(completedCommandItem("list-1", "ls src", "src\n"))

	view := model.View()
	if !strings.Contains(view, "Explored") {
		t.Fatalf("completed exploring group should render as Explored:\n%s", view)
	}
	if !strings.Contains(view, "Read README.md") || !strings.Contains(view, "List src") {
		t.Fatalf("completed exploring group lost its actions:\n%s", view)
	}
}
