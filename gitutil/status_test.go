package gitutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestShareStatusRunCoalescesConcurrentScansSameRootLikeRust(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	initiator := func() (bool, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return true, nil
	}
	key := filepath.Join("tmp", "repo")
	runA := shareStatusRun(key, initiator)
	<-started // the first scan is now in flight
	// A second consumer for the same canonical root must receive the same
	// in-flight handle (Rust WeakShared upgrade), deterministically.
	runB := shareStatusRun(key, initiator)
	if runA != runB {
		t.Fatal("second consumer received a different run; want coalesced handle")
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1 coalesced scan", calls.Load())
	}
	close(release)
	ctx := context.Background()
	for name, run := range map[string]*statusRun{"first": runA, "second": runB} {
		if ok, err := waitStatus(ctx, run); err != nil || !ok {
			t.Fatalf("%s consumer result = %v, %v; want true, nil", name, ok, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d after completion, want 1", calls.Load())
	}
}

func TestShareStatusRunDifferentRootsScanIndependentlyLikeRust(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	initiator := func() (bool, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return true, nil
	}
	runOne := shareStatusRun("repo-one", initiator)
	runTwo := shareStatusRun("repo-two", initiator)
	<-started
	<-started
	if runOne == runTwo {
		t.Fatal("different repository roots shared one run; want independent scans")
	}
	if calls.Load() != 2 {
		t.Fatalf("runner calls = %d, want 2 independent scans", calls.Load())
	}
	close(release)
	ctx := context.Background()
	for _, run := range []*statusRun{runOne, runTwo} {
		if _, err := waitStatus(ctx, run); err != nil {
			t.Fatalf("consumer error: %v", err)
		}
	}
}

func TestShareStatusRunFreshScanAfterCompletionLikeRust(t *testing.T) {
	var calls atomic.Int32
	initiator := func() (bool, error) {
		calls.Add(1)
		return false, nil
	}
	ctx := context.Background()
	runA := shareStatusRun("repo", initiator)
	if _, err := waitStatus(ctx, runA); err != nil {
		t.Fatalf("first scan error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d after first scan, want 1", calls.Load())
	}
	// The completed run is evicted, so a later request starts a fresh scan.
	runB := shareStatusRun("repo", initiator)
	if runA == runB {
		t.Fatal("completed run was reused; want a fresh scan")
	}
	if _, err := waitStatus(ctx, runB); err != nil {
		t.Fatalf("second scan error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("runner calls = %d, want 2 (fresh scan after completion)", calls.Load())
	}
}

func TestShareStatusRunConsumerCancellationDoesNotAbortSharedScan(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	initiator := func() (bool, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return true, nil
	}
	run := shareStatusRun("repo", initiator)
	<-started // the shared scan is in flight before any consumer cancels
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitStatus(cancelled, run); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled consumer error = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1 (cancelled consumer reuses the run)", calls.Load())
	}
	close(release)
	if ok, err := waitStatus(context.Background(), run); err != nil || !ok {
		t.Fatalf("surviving consumer result = %v, %v; want true, nil", ok, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d after completion, want 1", calls.Load())
	}
}

func TestShareStatusRunCanonicalRootAliasesShareScanLikeRust(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	initiator := func() (bool, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return true, nil
	}
	realKey := canonicalRepoRoot(real)
	aliasKey := canonicalRepoRoot(link)
	if realKey != aliasKey {
		t.Fatalf("canonical keys differ: real=%q alias=%q", realKey, aliasKey)
	}
	runReal := shareStatusRun(realKey, initiator)
	<-started // the scan keyed by the real root is in flight
	runAlias := shareStatusRun(aliasKey, initiator)
	if runReal != runAlias {
		t.Fatal("symlink alias received a different run; want coalesced handle")
	}
	if calls.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1 coalesced scan across real/alias roots", calls.Load())
	}
	close(release)
	ctx := context.Background()
	for _, run := range []*statusRun{runReal, runAlias} {
		if _, err := waitStatus(ctx, run); err != nil {
			t.Fatalf("consumer error: %v", err)
		}
	}
}

func TestHasChangesInRepoReportsRealGitStatus(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) error {
		_, err := Run(context.Background(), dir, args...)
		return err
	}
	if err := runGit("init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	file := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit("add", "tracked.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit("commit", "-qm", "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	ctx := context.Background()
	if ok, err := hasChangesInRepoWithRunner(ctx, dir, dir, defaultStatusRunner); err != nil || ok {
		t.Fatalf("clean repo = %v, %v; want false, nil", ok, err)
	}
	if err := os.WriteFile(file, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := hasChangesInRepoWithRunner(ctx, dir, dir, defaultStatusRunner); err != nil || !ok {
		t.Fatalf("dirty repo = %v, %v; want true, nil", ok, err)
	}
}

func TestCanonicalRepoRootFallsBackToCleanPath(t *testing.T) {
	got := canonicalRepoRoot(filepath.Join("tmp", "..", "tmp", "repo"))
	if got != filepath.Clean(filepath.Join("tmp", "repo")) {
		t.Fatalf("canonicalRepoRoot = %q", got)
	}
}

func TestHasChangesInRepoPropagatesRunnerError(t *testing.T) {
	initiator := func() (bool, error) {
		return false, errors.New("boom")
	}
	run := shareStatusRun("repo", initiator)
	ok, err := waitStatus(context.Background(), run)
	if err == nil || ok {
		t.Fatalf("scan = %v, %v; want error", ok, err)
	}
}

// TestHasChangesInRepoIgnoresDirtySubmoduleWorktreesLikeRust mirrors Rust
// #60669: uncommitted submodule contents are skipped (their clean filters are
// task-writable), while gitlink changes still count.
func TestHasChangesInRepoIgnoresDirtySubmoduleWorktreesLikeRust(t *testing.T) {
	if _, err := Run(context.Background(), ".", "version"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := t.TempDir()
	submodule := filepath.Join(root, "sub")
	super := filepath.Join(root, "super")
	runGit := func(dir string, args ...string) error {
		_, err := Run(context.Background(), dir, args...)
		return err
	}
	for _, dir := range []string{submodule, super} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := runGit(dir, "init", "-q"); err != nil {
			t.Fatalf("git init %s: %v", dir, err)
		}
		_ = runGit(dir, "config", "user.email", "test@example.com")
		_ = runGit(dir, "config", "user.name", "test")
	}
	if err := os.WriteFile(filepath.Join(submodule, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(submodule, "add", "file.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(submodule, "commit", "-qm", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(super, "-c", "protocol.file.allow=always", "submodule", "add", submodule, "sub"); err != nil {
		t.Fatalf("git submodule add: %v", err)
	}
	if err := runGit(super, "commit", "-qm", "add submodule"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if ok, err := HasChangesInRepo(ctx, super, super); err != nil || ok {
		t.Fatalf("clean superproject = %v, %v; want false, nil", ok, err)
	}
	if err := os.WriteFile(filepath.Join(super, "sub", "file.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasChangesInRepo(ctx, super, super); err != nil || ok {
		t.Fatalf("dirty submodule worktree = %v, %v; want false, nil", ok, err)
	}
	submoduleWorktree := filepath.Join(super, "sub")
	other := filepath.Join(submoduleWorktree, "other.txt")
	if err := os.WriteFile(other, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(submoduleWorktree, "add", "other.txt"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(submoduleWorktree, "commit", "-qm", "other"); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasChangesInRepo(ctx, super, super); err != nil || !ok {
		t.Fatalf("changed submodule gitlink = %v, %v; want true, nil", ok, err)
	}
}
