package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"codex_go/session"
	"codex_go/worktree"
)

func runAppGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func initAppGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runAppGit(t, dir, "init")
	runAppGit(t, dir, "config", "user.email", "codex@example.com")
	runAppGit(t, dir, "config", "user.name", "Codex Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runAppGit(t, dir, "add", ".")
	runAppGit(t, dir, "commit", "-m", "init")
	return dir
}

func createAppThreadRecord(t *testing.T, store *session.Store, cwd string, text string) session.ThreadID {
	t.Helper()
	now := time.Now().UTC()
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	record := &session.Record{
		ID:        session.ThreadID(id.String()),
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  session.Metadata{CWD: cwd, Source: "cli"},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if strings.TrimSpace(text) != "" {
		if _, err := store.AppendItem(record.ID, session.Item{
			ID: "user-1", Type: "user_message", Role: "user", Text: text, CreatedAt: now,
		}); err != nil {
			t.Fatalf("append item: %v", err)
		}
	}
	return record.ID
}

// TestInteractiveStartManagedWorktreeForkBindsAndInheritsHistory covers Rust
// #43120 for the local handler: a managed checkout is created, the conversation
// is forked into it with history, and the new thread is bound to the worktree.
func TestInteractiveStartManagedWorktreeForkBindsAndInheritsHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	source := initAppGitRepo(t)

	settings, err := worktree.FromDesktopConfig(home, nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig: %v", err)
	}
	store := newSessionStore()
	sourceID := createAppThreadRecord(t, store, source, "carry me over")

	handler := interactiveStartManagedWorktreeHandler(settings)
	response, err := handler("fork", "", source, string(sourceID))
	if err != nil {
		t.Fatalf("start managed worktree: %v", err)
	}
	if response.Summary == nil || strings.TrimSpace(response.Summary.ThreadID) == "" {
		t.Fatalf("response = %#v", response)
	}
	if response.Summary.ThreadID == string(sourceID) {
		t.Fatal("fork must create a new thread id")
	}
	record, err := store.Read(session.ThreadID(response.Summary.ThreadID), true, false)
	if err != nil || record == nil {
		t.Fatalf("read forked record: %v", err)
	}
	worktreeRoot := filepath.Clean(settings.Root)
	if !strings.HasPrefix(filepath.Clean(record.Metadata.CWD), worktreeRoot) {
		t.Fatalf("forked cwd = %q, want inside %q", record.Metadata.CWD, worktreeRoot)
	}
	if len(response.Messages) == 0 {
		t.Fatal("fork must inherit the source history")
	}

	manager := worktree.NewWorktreeManager(settings)
	listed, err := manager.List(source)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list managed worktrees = %#v (err=%v)", listed, err)
	}
	owner, err := manager.Owner(listed[0].Root)
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if owner != response.Summary.ThreadID {
		t.Fatalf("owner = %q, want %q", owner, response.Summary.ThreadID)
	}
	if filepath.Clean(listed[0].CWD) != filepath.Clean(record.Metadata.CWD) {
		t.Fatalf("listed cwd = %q, want %q", listed[0].CWD, record.Metadata.CWD)
	}
}

// TestInteractiveStartManagedWorktreeNewCreatesFreshThread covers the "start
// new conversation" choice: a fresh thread rooted at the new checkout with no
// inherited history.
func TestInteractiveStartManagedWorktreeNewCreatesFreshThread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	source := initAppGitRepo(t)

	settings, err := worktree.FromDesktopConfig(home, nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig: %v", err)
	}
	handler := interactiveStartManagedWorktreeHandler(settings)
	response, err := handler("new", "named checkout", source, "")
	if err != nil {
		t.Fatalf("start managed worktree: %v", err)
	}
	if response.Summary == nil || strings.TrimSpace(response.Summary.ThreadID) == "" {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Messages) != 0 {
		t.Fatalf("a fresh conversation must not inherit history: %#v", response.Messages)
	}
	store := newSessionStore()
	record, err := store.Read(session.ThreadID(response.Summary.ThreadID), true, false)
	if err != nil || record == nil {
		t.Fatalf("read created record: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(record.Metadata.CWD), filepath.Clean(settings.Root)) {
		t.Fatalf("created cwd = %q", record.Metadata.CWD)
	}
}

// TestInteractiveWorktreeOwnerLookupResolvesThreads covers the browser's owner
// metadata lookup.
func TestInteractiveWorktreeOwnerLookupResolvesThreads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := newSessionStore()
	threadID := createAppThreadRecord(t, store, home, "owner preview")

	lookup, ok := interactiveWorktreeOwnerLookup(string(threadID))
	if !ok {
		t.Fatal("owner lookup failed for an existing thread")
	}
	if lookup.ID != string(threadID) {
		t.Fatalf("lookup id = %q", lookup.ID)
	}
	if strings.TrimSpace(lookup.Preview) == "" && strings.TrimSpace(lookup.Name) == "" {
		t.Fatalf("lookup metadata = %#v", lookup)
	}
	if _, ok := interactiveWorktreeOwnerLookup("missing-thread"); ok {
		t.Fatal("unknown thread should not resolve")
	}
	if _, ok := interactiveWorktreeOwnerLookup(""); ok {
		t.Fatal("empty thread id should not resolve")
	}
}
