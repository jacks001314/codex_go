package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestParseWorktreeStatusLikeRust pins Rust #61939 porcelain-v2 parsing: HEAD
// and changes come from one scan, unborn HEAD is nil, and any unrecognized or
// duplicated observation is rejected rather than trusted.
func TestParseWorktreeStatusLikeRust(t *testing.T) {
	head := "0123456789abcdef0123456789abcdef01234567"
	sha256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name       string
		output     string
		ok         bool
		head       *string
		hasChanges bool
	}{
		{name: "clean", output: "# branch.oid " + head + "\n# branch.head main\n", ok: true, head: &head},
		{name: "sha256", output: "# branch.oid " + sha256 + "\n", ok: true, head: &sha256},
		{name: "unborn", output: "# branch.oid (initial)\n# branch.head main\n", ok: true},
		{name: "ordinary change", output: "# branch.oid " + head + "\n1 .M N... 100644 100644 100644 aaaa bbbb file.go\n", ok: true, head: &head, hasChanges: true},
		{name: "rename", output: "# branch.oid " + head + "\n2 R. N... 100644 100644 100644 aaaa bbbb R100 new.go\told.go\n", ok: true, head: &head, hasChanges: true},
		{name: "unmerged", output: "# branch.oid " + head + "\nu UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.go\n", ok: true, head: &head, hasChanges: true},
		{name: "untracked", output: "# branch.oid " + head + "\n? new.txt\n", ok: true, head: &head, hasChanges: true},
		{name: "duplicate oid", output: "# branch.oid " + head + "\n# branch.oid " + head + "\n"},
		{name: "missing oid", output: "# branch.head main\n"},
		{name: "invalid oid", output: "# branch.oid not-a-sha\n"},
		{name: "short oid", output: "# branch.oid 0123abc\n"},
		{name: "unknown line", output: "# branch.oid " + head + "\ngarbage\n"},
		{name: "empty", output: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, ok := ParseWorktreeStatus(testCase.output)
			if ok != testCase.ok {
				t.Fatalf("ok = %v, want %v", ok, testCase.ok)
			}
			if !testCase.ok {
				if status != nil {
					t.Fatalf("status = %#v, want nil", status)
				}
				return
			}
			if status.HasChanges != testCase.hasChanges {
				t.Fatalf("HasChanges = %v, want %v", status.HasChanges, testCase.hasChanges)
			}
			switch {
			case testCase.head == nil && status.HeadCommitHash != nil:
				t.Fatalf("HeadCommitHash = %q, want nil", *status.HeadCommitHash)
			case testCase.head != nil && (status.HeadCommitHash == nil || *status.HeadCommitHash != *testCase.head):
				t.Fatalf("HeadCommitHash = %v, want %q", status.HeadCommitHash, *testCase.head)
			}
		})
	}
}

// TestGetHeadStateDistinguishesUnbornAndMissingLikeRust mirrors Rust #61939:
// an unborn HEAD is Missing, a committed HEAD is Commit, and anything else is
// Unavailable.
func TestGetHeadStateDistinguishesUnbornAndMissingLikeRust(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	if state := GetHeadState(ctx, t.TempDir()); state.Kind != GitHeadUnavailable {
		t.Fatalf("non-repository state = %#v, want Unavailable", state)
	}
	repo := initTestRepo(t)
	if state := GetHeadState(ctx, repo); state.Kind != GitHeadMissing {
		t.Fatalf("unborn HEAD state = %#v, want Missing", state)
	}
	commitTestFile(t, repo, "tracked.txt", "one\n")
	state := GetHeadState(ctx, repo)
	if state.Kind != GitHeadCommit || state.Commit == "" {
		t.Fatalf("committed HEAD state = %#v, want Commit", state)
	}
}

// TestGetWorktreeStatusInRepoReportsHeadAndChangesLikeRust pins the combined
// porcelain-v2 scan: the same command reports HEAD and whether the worktree
// changed.
func TestGetWorktreeStatusInRepoReportsHeadAndChangesLikeRust(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initTestRepo(t)
	commitTestFile(t, repo, "tracked.txt", "one\n")
	head := GetHeadState(ctx, repo)
	status, err := GetWorktreeStatusInRepo(ctx, repo, repo)
	if err != nil {
		t.Fatalf("clean status: %v", err)
	}
	if status.HasChanges {
		t.Fatal("clean repository reported changes")
	}
	if status.HeadCommitHash == nil || *status.HeadCommitHash != head.Commit {
		t.Fatalf("status HEAD = %v, want %q", status.HeadCommitHash, head.Commit)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err = GetWorktreeStatusInRepo(ctx, repo, repo)
	if err != nil {
		t.Fatalf("dirty status: %v", err)
	}
	if !status.HasChanges {
		t.Fatal("dirty repository reported no changes")
	}
}

// TestProbeWorktreeChangesOnlyReportsPositiveLikeRust mirrors Rust #61939: a
// clean sample, or a change outside the four most recently modified root
// directories, proves nothing and yields no observation.
func TestProbeWorktreeChangesOnlyReportsPositiveLikeRust(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := initTestRepo(t)
	commitTestFile(t, repo, "tracked.txt", "one\n")
	if probe := ProbeWorktreeChanges(ctx, repo); probe != nil {
		t.Fatalf("clean probe = %#v, want nil", probe)
	}
	directory := filepath.Join(repo, "recent")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "new.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := ProbeWorktreeChanges(ctx, repo)
	if probe == nil || !probe.Status.HasChanges || canonicalRepoRoot(probe.RepoRoot) != canonicalRepoRoot(repo) {
		t.Fatalf("dirty probe = %#v, want changes in %q", probe, repo)
	}
	if probe.Status.HeadCommitHash == nil {
		t.Fatal("probe lost the HEAD observation")
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := Run(context.Background(), ".", "version"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := Run(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return repo
}

func commitTestFile(t *testing.T, repo string, name string, contents string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-qm", "initial"}} {
		if _, err := Run(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}
