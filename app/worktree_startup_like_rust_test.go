package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/cli"
	"codex_go/rollout"
	"codex_go/session"
)

func initWorktreeSourceRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s", args, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, args := range [][]string{{"add", "file.txt"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s", args, output)
		}
	}
	requireGitWorktreeListZero(t, repo)
	return repo
}

// Rust #43069 worktree_startup: an interactive launch with --worktree allocates
// the managed checkout before the session starts and moves the launch cwd there.
func TestPrepareInteractiveWorktreeCreatesCheckoutAndMovesCWDLikeRust(t *testing.T) {
	repo := initWorktreeSourceRepo(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")

	root := &cli.RootOptions{}
	root.Shared.CWD = repo
	root.Shared.Worktree = true
	startup, err := prepareInteractiveWorktree(root)
	if err != nil {
		t.Fatalf("prepareInteractiveWorktree: %v", err)
	}
	if startup == nil {
		t.Fatal("--worktree did not allocate a checkout")
	}
	if root.Shared.CWD != startup.checkout.Root || startup.checkout.Root == repo {
		t.Fatalf("launch cwd = %q, checkout = %q (source %q)", root.Shared.CWD, startup.checkout.Root, repo)
	}
	if _, err := os.Stat(filepath.Join(startup.checkout.Root, "file.txt")); err != nil {
		t.Fatalf("checkout missing source file: %v", err)
	}
	if err := startup.bind("thread-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if owner, err := startup.manager.Owner(startup.checkout.Root); err != nil || owner != "thread-1" {
		t.Fatalf("owner = %q, %v, want thread-1", owner, err)
	}
	// A bound checkout must not print recovery instructions.
	var stderr bytes.Buffer
	startup.reportStartupFailure(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("bound worktree printed recovery text: %q", stderr.String())
	}
}

func TestPrepareInteractiveWorktreeWithoutFlagIsNoopLikeRust(t *testing.T) {
	root := &cli.RootOptions{}
	root.Shared.CWD = t.TempDir()
	startup, err := prepareInteractiveWorktree(root)
	if err != nil || startup != nil {
		t.Fatalf("prepareInteractiveWorktree() = %#v, %v; want nil, nil", startup, err)
	}
}

// Rust #43069: an explicitly untrusted source is refused before any checkout is
// created.
func TestPrepareInteractiveWorktreeRejectsUntrustedSourceLikeRust(t *testing.T) {
	repo := initWorktreeSourceRepo(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", "")
	restore := interactiveWorktreeSourceUntrusted
	interactiveWorktreeSourceUntrusted = func(*cli.RootOptions, string) bool { return true }
	defer func() { interactiveWorktreeSourceUntrusted = restore }()

	root := &cli.RootOptions{}
	root.Shared.CWD = repo
	root.Shared.Worktree = true
	startup, err := prepareInteractiveWorktree(root)
	if err == nil || !strings.Contains(err.Error(), "explicitly untrusted source") {
		t.Fatalf("err = %v, want untrusted-source error", err)
	}
	if startup != nil {
		t.Fatalf("untrusted source created %#v", startup)
	}
	if root.Shared.CWD != repo {
		t.Fatalf("cwd changed to %q for a rejected source", root.Shared.CWD)
	}
}

// Rust StartupRecovery: an unbound checkout stays on disk and prints removal
// instructions instead of being deleted.
func TestInteractiveWorktreeStartupReportsUnboundCheckoutLikeRust(t *testing.T) {
	repo := initWorktreeSourceRepo(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", "")
	root := &cli.RootOptions{}
	root.Shared.CWD = repo
	root.Shared.Worktree = true
	startup, err := prepareInteractiveWorktree(root)
	if err != nil {
		t.Fatalf("prepareInteractiveWorktree: %v", err)
	}
	var stderr bytes.Buffer
	startup.reportStartupFailure(&stderr)
	message := stderr.String()
	if !strings.Contains(message, fmt.Sprintf("%q", startup.checkout.Root)) {
		t.Fatalf("recovery message missing checkout path: %q", message)
	}
	if !strings.Contains(message, "The checkout was kept") || !strings.Contains(message, "Do not use --force") {
		t.Fatalf("recovery message = %q", message)
	}
	if _, err := os.Stat(startup.checkout.Root); err != nil {
		t.Fatalf("checkout was removed: %v", err)
	}
	// Rust's StartupRecovery reports at most once even when startup and the
	// outer launcher both reach the failure path.
	startup.reportStartupFailure(&stderr)
	if got := strings.Count(stderr.String(), "Startup did not finish binding"); got != 1 {
		t.Fatalf("recovery message count = %d, want 1\n%s", got, stderr.String())
	}
}

func prepareForkWorktreeSession(t *testing.T) (string, *session.Store, session.ThreadID) {
	t.Helper()
	repo := initWorktreeSourceRepo(t)
	pkg := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "pkg.txt"), []byte("pkg\n"), 0o600); err != nil {
		t.Fatalf("write pkg file: %v", err)
	}
	command := exec.Command("git", "add", "pkg/pkg.txt")
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add pkg: %v\n%s", err, output)
	}
	command = exec.Command("git", "commit", "-m", "add pkg")
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit pkg: %v\n%s", err, output)
	}

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")
	store := session.NewStore(filepath.Join(home, "sessions"))
	target := session.ThreadID("019f1234-5678-7000-8000-000000000001")
	now := time.Now().UTC()
	if err := store.Save(&session.Record{
		ID:        target,
		SessionID: string(target),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: repo, Source: "cli"},
		Items: []session.Item{{
			ID: "user-1", Type: "user_message", Role: "user", Text: "carry me over", CreatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("save source thread: %v", err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home,
		SessionID: string(target),
		ThreadID:  string(target),
		CWD:       repo,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("new rollout recorder: %v", err)
	}
	if err := recorder.AppendTurnContext(rollout.TurnContextRecord{TurnID: "turn-1", CWD: pkg}, now); err != nil {
		t.Fatalf("append turn context: %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close rollout recorder: %v", err)
	}
	return repo, store, target
}

// Rust #43069 interactive fork worktree startup: the checkout is based on the
// source conversation's newest saved turn cwd, the fork inherits its history,
// and checkout ownership is bound before the first turn can run.
func TestPrepareInteractiveForkWorktreeStartsForkInLatestSourceDirectoryLikeRust(t *testing.T) {
	repo, store, target := prepareForkWorktreeSession(t)
	root := &cli.RootOptions{}
	root.Shared.Worktree = true
	opts := &cli.SessionOptions{Target: string(target)}

	startup, err := prepareInteractiveForkWorktree(root, opts, store)
	if err != nil {
		t.Fatalf("prepareInteractiveForkWorktree: %v", err)
	}
	if startup == nil || startup.worktree == nil || startup.forked == nil {
		t.Fatalf("startup = %#v", startup)
	}
	wantCWD := filepath.Join(startup.worktree.checkout.Root, "pkg")
	if !strings.EqualFold(filepath.Clean(root.Shared.CWD), filepath.Clean(wantCWD)) {
		t.Fatalf("launch cwd = %q, want %q", root.Shared.CWD, wantCWD)
	}
	if !strings.EqualFold(filepath.Clean(startup.forked.Metadata.CWD), filepath.Clean(wantCWD)) {
		t.Fatalf("forked cwd = %q, want %q", startup.forked.Metadata.CWD, wantCWD)
	}
	if startup.forked.ForkedFromID != target {
		t.Fatalf("fork source = %q, want %q", startup.forked.ForkedFromID, target)
	}
	if len(startup.forked.Items) == 0 || startup.forked.Items[0].Text != "carry me over" {
		t.Fatalf("forked history = %#v", startup.forked.Items)
	}
	owner, err := startup.worktree.manager.Owner(startup.worktree.checkout.Root)
	if err != nil {
		t.Fatalf("read worktree owner: %v", err)
	}
	if owner != string(startup.forked.ID) {
		t.Fatalf("worktree owner = %q, want %q", owner, startup.forked.ID)
	}
	if !strings.EqualFold(filepath.Clean(startup.worktree.checkout.SourceRoot), filepath.Clean(repo)) {
		t.Fatalf("checkout source root = %q, want %q", startup.worktree.checkout.SourceRoot, repo)
	}
}

// An explicit --cd remains the checkout source, matching Rust's precedence over
// the source conversation's saved working directory.
func TestPrepareInteractiveForkWorktreeKeepsExplicitCWDLikeRust(t *testing.T) {
	repo, store, target := prepareForkWorktreeSession(t)
	explicit := filepath.Join(repo, "pkg")
	root := &cli.RootOptions{}
	root.Shared.Worktree = true
	root.Shared.CWD = explicit
	opts := &cli.SessionOptions{Target: string(target)}

	startup, err := prepareInteractiveForkWorktree(root, opts, store)
	if err != nil {
		t.Fatalf("prepareInteractiveForkWorktree: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(startup.worktree.checkout.SourceCWD), filepath.Clean(explicit)) {
		t.Fatalf("checkout source cwd = %q, want %q", startup.worktree.checkout.SourceCWD, explicit)
	}
}

func TestPrepareInteractiveWorktreeAnchorsRelativeAddDirLikeRust(t *testing.T) {
	repo := initWorktreeSourceRepo(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", "")
	invocation := t.TempDir()
	t.Chdir(invocation)
	root := &cli.RootOptions{}
	root.Shared.CWD = repo
	root.Shared.Worktree = true
	root.Shared.AddDirs = []string{"extra"}

	if _, err := prepareInteractiveWorktree(root); err != nil {
		t.Fatalf("prepareInteractiveWorktree: %v", err)
	}
	want := filepath.Join(invocation, "extra")
	if len(root.Shared.AddDirs) != 1 || !strings.EqualFold(filepath.Clean(root.Shared.AddDirs[0]), filepath.Clean(want)) {
		t.Fatalf("add dirs = %#v, want %q", root.Shared.AddDirs, want)
	}
}

// Rust #43069 routes `codex fork <id> --worktree` into the interactive TUI and
// merges the subcommand flags into the interactive launch.
func TestSessionForkWorktreeUsesInteractiveStarterLikeRust(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	restore := interactiveForkWorktreeStarter
	defer func() { interactiveForkWorktreeStarter = restore }()
	called := false
	interactiveForkWorktreeStarter = func(_ context.Context, root *cli.RootOptions, opts *cli.SessionOptions, _ *session.Store, _ io.Reader, _ io.Writer, _ io.Writer) error {
		called = true
		if root == nil || root.Shared.Model != "fork-model" || root.Prompt != "continue here" {
			t.Fatalf("merged root = %#v", root)
		}
		if opts == nil || opts.Target != "019f1234-5678-7000-8000-000000000001" || !opts.Shared.Worktree {
			t.Fatalf("fork options = %#v", opts)
		}
		return nil
	}
	rootCWD := t.TempDir()
	root := &cli.RootOptions{Shared: cli.SharedOptions{CWD: rootCWD, Model: "root-model"}}
	opts := &cli.SessionOptions{
		Target: "019f1234-5678-7000-8000-000000000001",
		Prompt: "continue here",
		Shared: cli.SharedOptions{Worktree: true, Model: "fork-model"},
	}
	var stdout, stderr bytes.Buffer
	if err := runSessionFork(opts, root, newTerminalReader(""), terminalOut(&stdout), &stderr); err != nil {
		t.Fatalf("runSessionFork: %v", err)
	}
	if !called {
		t.Fatal("fork --worktree did not enter the interactive starter")
	}
}
