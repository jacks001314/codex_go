package exec

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"codex_go/protocol"
)

// Rust renders a completed AgentMessage into the human transcript and remembers
// it as the final message (event_processor_with_human_output.rs).
func TestExecRendersCompletedAgentMessageLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	human := newExecHumanRenderer(&stderr, "always")
	human.renderItemCompleted(protocol.ThreadItem{Type: "agent_message", Text: "LM-OK"})

	if got, want := stderr.String(), "\x1b[3m\x1b[35mcodex\x1b[0m\nLM-OK\n"; got != want {
		t.Fatalf("agent message transcript = %q, want %q", got, want)
	}
	if !human.agentMessageRendered || human.lastAgentMessage != "LM-OK" {
		t.Fatalf("renderer state = (%v, %q), want (true, %q)", human.agentMessageRendered, human.lastAgentMessage, "LM-OK")
	}
}

func TestExecAgentMessageWithoutColorIsUnstyled(t *testing.T) {
	var stderr bytes.Buffer
	human := newExecHumanRenderer(&stderr, "never")
	human.renderItemCompleted(protocol.ThreadItem{Type: "agent_message", Text: "done"})

	if got, want := stderr.String(), "codex\ndone\n"; got != want {
		t.Fatalf("agent message transcript = %q, want %q", got, want)
	}
}

// Rust's handle_last_message reports a failed write and keeps the run
// successful instead of aborting the turn.
func TestWriteLastMessageFileReportsFailureWithoutError(t *testing.T) {
	var stderr bytes.Buffer
	badPath := filepath.Join(t.TempDir(), "missing-dir", "last.txt")

	writeLastMessageFile(&stderr, badPath, "LM-OK", true)

	quoted := strconv.Quote(badPath)
	want := "Failed to write last message file " + quoted + ": "
	if !strings.HasPrefix(stderr.String(), want) {
		t.Fatalf("stderr = %q, want prefix %q", stderr.String(), want)
	}
	if strings.Count(stderr.String(), quoted) != 1 {
		t.Fatalf("stderr repeats the path inside the OS error: %q", stderr.String())
	}
	if strings.HasSuffix(stderr.String(), ": \n") {
		t.Fatalf("stderr has no OS error text: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("stderr should not warn when the turn had a message: %q", stderr.String())
	}
}

func TestWriteLastMessageFileWritesEmptyContentWithWarning(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "last.txt")

	writeLastMessageFile(&stderr, path, "", false)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("last message file = %q, want empty", data)
	}
	if want := "Warning: no last agent message; wrote empty content to " + path + "\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestWriteHumanFinalMessageNonTerminalStdoutGetsReply(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := writeHumanFinalMessage(&stdout, &stderr, "LM-OK", false, "codex"); err != nil {
		t.Fatalf("writeHumanFinalMessage returned error: %v", err)
	}
	if stdout.String() != "LM-OK\n" || stderr.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want stdout only", stdout.String(), stderr.String())
	}
}

func TestWriteHumanFinalMessageTerminalSkipsRenderedReply(t *testing.T) {
	stdout := &execTerminalBuffer{}
	stderr := &execTerminalBuffer{}

	if err := writeHumanFinalMessage(stdout, stderr, "LM-OK", true, "codex"); err != nil {
		t.Fatalf("writeHumanFinalMessage returned error: %v", err)
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Fatalf("stdout = %q, stderr = %q, want nothing when the transcript rendered it", stdout.String(), stderr.String())
	}
}

func TestWriteHumanFinalMessageTerminalPrintsUnrenderedReply(t *testing.T) {
	stdout := &execTerminalBuffer{}
	stderr := &execTerminalBuffer{}

	if err := writeHumanFinalMessage(stdout, stderr, "LM-OK", false, "\x1b[3m\x1b[35mcodex\x1b[0m"); err != nil {
		t.Fatalf("writeHumanFinalMessage returned error: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if want := "\x1b[3m\x1b[35mcodex\x1b[0m\nLM-OK\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

// Rust's io::Error Display carries only the reason, so the shared path prefix
// printed by the caller must not be duplicated.
func TestIOErrorMessageDropsPathFromPathError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := os.ReadFile(path)
	if err == nil {
		t.Fatal("expected a read error")
	}
	message := ioErrorMessage(err).Error()
	if strings.Contains(message, path) || strings.Contains(message, "missing.txt") {
		t.Fatalf("ioErrorMessage = %q, want the path stripped", message)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || message != pathErr.Err.Error() {
		t.Fatalf("ioErrorMessage = %q, want %q", message, pathErr.Err)
	}
}
