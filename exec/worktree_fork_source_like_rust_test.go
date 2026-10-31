package exec

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/rollout"
	"codex_go/session"
)

func TestLatestRolloutTurnContextCWDLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"thread-1"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-1","cwd":"/first"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-2","cwd":"/second"}}`,
		`not-json`,
		`{"type":"turn_context","turn_context":{"cwd":"/legacy"}}`,
		`{"type":"world_state","payload":{}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if got := latestRolloutTurnContextCWD(path); got != "/legacy" {
		t.Fatalf("latestRolloutTurnContextCWD() = %q, want /legacy", got)
	}
	if got := latestRolloutTurnContextCWD(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Fatalf("latestRolloutTurnContextCWD(missing) = %q, want empty", got)
	}
}

func TestLatestThreadCWDFallsBackToThreadCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	record := &session.Record{
		ID:        "thread-fallback",
		SessionID: "thread-fallback",
		Metadata:  session.Metadata{CWD: "/thread-cwd"},
	}
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home,
		SessionID: record.SessionID,
		ThreadID:  string(record.ID),
		CWD:       "/rollout-meta-cwd",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	runner := &Runner{CodexHome: home}
	if got := runner.latestThreadCWD(record); got != "/thread-cwd" {
		t.Fatalf("latestThreadCWD() = %q, want /thread-cwd", got)
	}
}

func TestExecWorktreeRejectsExplicitlyUntrustedSourceLikeRust(t *testing.T) {
	repo := newWorktreeTestRepo(t)
	home := t.TempDir()
	writeUntrustedProjectConfig(t, home, repo)
	var stdout, stderr bytes.Buffer
	_, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{CWD: repo, Worktree: true},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("Run() error = nil, want the untrusted-source rejection")
	}
	if err.Error() != "--worktree requires a source that is not explicitly untrusted" {
		t.Fatalf("Run() error = %q", err)
	}
	entries, listErr := os.ReadDir(filepath.Join(home, "worktrees"))
	if listErr == nil && len(entries) != 0 {
		t.Fatalf("untrusted source allocated worktrees: %v", entries)
	}
}

func TestExecForkWorktreeUsesForkSourceCWDLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth: %v", err)
	}
	source := newWorktreeTestRepo(t)
	sourcePkg := filepath.Join(source, "pkg")
	if err := os.MkdirAll(sourcePkg, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourcePkg, "pkg.txt"), []byte("pkg\n"), 0o600); err != nil {
		t.Fatalf("write pkg file: %v", err)
	}
	runWorktreeGit(t, source, "add", "pkg/pkg.txt")
	runWorktreeGit(t, source, "commit", "-m", "add pkg")
	invocation := newWorktreeTestRepo(t)
	// No --cd: the invocation directory only becomes the request cwd through
	// the process working directory, exactly like the CLI.
	t.Chdir(invocation)
	const sourceID = "thread-fork-source"
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        sourceID,
		SessionID: sourceID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: invocation},
	}); err != nil {
		t.Fatalf("Save source record: %v", err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home,
		SessionID: sourceID,
		ThreadID:  sourceID,
		CWD:       invocation,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnContext(rollout.TurnContextRecord{TurnID: "turn-1", CWD: sourcePkg}, now); err != nil {
		t.Fatalf("AppendTurnContext() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "fork",
			Fork:       cli.ExecForkOptions{SessionID: sourceID},
			Shared:     cli.SharedOptions{Worktree: true},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v, stderr = %s", err, stderr.String())
	}
	if result == nil || strings.TrimSpace(string(result.ThreadID)) == "" {
		t.Fatalf("Run() result = %#v", result)
	}
	checkouts := worktreeListPaths(t, source)
	if len(checkouts) != 1 {
		t.Fatalf("source worktrees = %v, want one managed checkout", checkouts)
	}
	managedRoot := filepath.Clean(filepath.Join(home, "worktrees"))
	if !strings.HasPrefix(strings.ToLower(filepath.Clean(checkouts[0])), strings.ToLower(managedRoot)+string(filepath.Separator)) {
		t.Fatalf("checkout %q is not under the managed root %q", checkouts[0], managedRoot)
	}
	forked, err := store.Read(session.ThreadID(result.ThreadID), true, true)
	if err != nil {
		t.Fatalf("Read forked record: %v", err)
	}
	wantCWD := filepath.Join(checkouts[0], "pkg")
	if !strings.EqualFold(filepath.Clean(forked.Metadata.CWD), filepath.Clean(wantCWD)) {
		t.Fatalf("forked cwd = %q, want %q", forked.Metadata.CWD, wantCWD)
	}
}

func TestExecForkWorktreeKeepsExplicitCWDLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth: %v", err)
	}
	source := newWorktreeTestRepo(t)
	invocation := newWorktreeTestRepo(t)
	const sourceID = "thread-fork-explicit-cwd"
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Save(&session.Record{
		ID:        sourceID,
		SessionID: sourceID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: source},
	}); err != nil {
		t.Fatalf("Save source record: %v", err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home,
		SessionID: sourceID,
		ThreadID:  sourceID,
		CWD:       source,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendTurnContext(rollout.TurnContextRecord{TurnID: "turn-1", CWD: source}, now); err != nil {
		t.Fatalf("AppendTurnContext() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	result, err := NewLocalRunner(home).Run(Request{
		Exec: cli.ExecOptions{
			Subcommand: "fork",
			Fork:       cli.ExecForkOptions{SessionID: sourceID},
			Shared:     cli.SharedOptions{CWD: invocation, Worktree: true},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v, stderr = %s", err, stderr.String())
	}
	checkouts := worktreeListPaths(t, invocation)
	if len(checkouts) != 1 {
		t.Fatalf("invocation worktrees = %v, want the explicit --cd checkout", checkouts)
	}
	if got := worktreeListPaths(t, source); len(got) != 0 {
		t.Fatalf("source worktrees = %v, want none", got)
	}
	forked, err := store.Read(session.ThreadID(result.ThreadID), true, true)
	if err != nil {
		t.Fatalf("Read forked record: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(forked.Metadata.CWD), filepath.Clean(checkouts[0])) {
		t.Fatalf("forked cwd = %q, want %q", forked.Metadata.CWD, checkouts[0])
	}
}

func newWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runWorktreeGit(t, repo, "init")
	runWorktreeGit(t, repo, "config", "user.email", "test@example.com")
	runWorktreeGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runWorktreeGit(t, repo, "add", "file.txt")
	runWorktreeGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runWorktreeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func worktreeListPaths(t *testing.T, repo string) []string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	var paths []string
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
	}
	// The first entry is the primary checkout; only linked worktrees count.
	if len(paths) <= 1 {
		return nil
	}
	return paths[1:]
}

func writeUntrustedProjectConfig(t *testing.T, home string, repo string) {
	t.Helper()
	path := config.ConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	escaped := strings.ReplaceAll(repo, `\`, `\\`)
	body := "[projects.\"" + escaped + "\"]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
