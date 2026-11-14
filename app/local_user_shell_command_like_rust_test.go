package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/protocol"
	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// The embedded TUI has no app-server session to answer thread/shellCommand, so
// the host runs the standalone user shell task itself and announces it as a
// turn with commandExecution items (Rust #44611).
func TestInteractiveLocalUserShellCommandStreamsTurnLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-shell")
	state.CWD = t.TempDir()
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID:        "thread-shell",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: state.CWD},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	run := interactiveLocalUserShellCommand(context.Background(), &cli.RootOptions{}, state, store)
	cmd := run("thread-shell", "echo hello")
	if cmd == nil {
		t.Fatal("shell command returned no command")
	}
	message := cmd()
	started, ok := message.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", message)
	}
	var eventTypes []string
	var completed protocol.ThreadItem
	for item := range started.Messages {
		event, ok := item.(codextea.ThreadScopedEventMsg)
		if !ok {
			continue
		}
		if event.ThreadID != "thread-shell" {
			t.Fatalf("event thread = %q", event.ThreadID)
		}
		eventTypes = append(eventTypes, event.Event.Type)
		if event.Event.Type == "item.completed" && event.Event.Item != nil {
			completed = *event.Event.Item
		}
	}
	want := []string{"turn.started", "item.started", "item.completed", "turn.completed"}
	if strings.Join(eventTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", eventTypes, want)
	}
	if completed.Command != "echo hello" || completed.AggregatedOutput == nil || !strings.Contains(*completed.AggregatedOutput, "hello") {
		t.Fatalf("completed item = %#v", completed)
	}
	if completed.ExitCode == nil || *completed.ExitCode != 0 || completed.Status != "completed" {
		t.Fatalf("completed status = %#v", completed)
	}
	if source, _ := completed.Metadata["source"].(string); source != "userShell" {
		t.Fatalf("item source = %#v", completed.Metadata)
	}

	record, err := store.Read(session.ThreadID("thread-shell"), true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	found := false
	for _, item := range record.Items {
		if item.Metadata["kind"] == "user_shell_command" && strings.Contains(item.Text, "echo hello") {
			found = true
		}
	}
	if !found {
		t.Fatalf("user shell record missing from rollout: %#v", record.Items)
	}
}
