package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/gitutil"
	"codex_go/turn"
)

// stubTurnGitSeams replaces the Git observation helpers for one test.
func stubTurnGitSeams(t *testing.T, head func(context.Context, string) gitutil.GitHeadState,
	hasChanges func(context.Context, string, string) (bool, error)) *int64 {
	t.Helper()
	originalDiscover, originalRemotes, originalHead, originalChanges :=
		turnGitDiscoverRoot, turnGitRemoteURLs, turnGitHeadState, turnGitHasChanges
	t.Cleanup(func() {
		turnGitDiscoverRoot, turnGitRemoteURLs, turnGitHeadState, turnGitHasChanges =
			originalDiscover, originalRemotes, originalHead, originalChanges
	})
	turnGitDiscoverRoot = func(context.Context, string) (string, error) { return "/repo", nil }
	turnGitRemoteURLs = func(context.Context, string) map[string]string {
		return map[string]string{"origin": "https://example.com/acme/repo"}
	}
	turnGitHeadState = head
	var scans int64
	turnGitHasChanges = func(ctx context.Context, cwd string, root string) (bool, error) {
		atomic.AddInt64(&scans, 1)
		return hasChanges(ctx, cwd, root)
	}
	return &scans
}

// Requests read the latest Git metadata while collection continues: HEAD and the
// remote URLs are available before admission, and the worktree status only
// afterwards (Rust #62437).
func TestTurnGitEnrichmentPublishesBeforeAndAfterAdmissionLikeRust(t *testing.T) {
	release := make(chan struct{})
	scans := stubTurnGitSeams(t,
		func(context.Context, string) gitutil.GitHeadState {
			return gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: "deadbeef"}
		},
		func(context.Context, string, string) (bool, error) {
			<-release
			return true, nil
		})
	enrichment := startTurnGitEnrichment(context.Background(), "/repo")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := enrichment.Snapshot()
		if workspace, ok := snapshot["/repo"]; ok {
			if workspace.LatestGitCommitHash != "deadbeef" {
				t.Fatalf("commit = %q, want deadbeef", workspace.LatestGitCommitHash)
			}
			if workspace.AssociatedRemoteURLs["origin"] != "https://example.com/acme/repo" {
				t.Fatalf("remotes = %v", workspace.AssociatedRemoteURLs)
			}
			if workspace.HasChanges != nil {
				t.Fatal("worktree status was reported before admission")
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt64(scans); got != 0 {
		t.Fatalf("worktree scans before admission = %d, want 0", got)
	}

	enrichment.Admit()
	close(release)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if workspace, ok := enrichment.Snapshot()["/repo"]; ok && workspace.HasChanges != nil {
			if !*workspace.HasChanges {
				t.Fatal("has_changes = false, want the observed dirty status")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worktree status never published: %+v", enrichment.Snapshot())
}

// A turn discarded before it runs never starts the worktree scan.
func TestTurnGitEnrichmentInterruptSkipsWorktreeScanLikeRust(t *testing.T) {
	scans := stubTurnGitSeams(t,
		func(context.Context, string) gitutil.GitHeadState {
			return gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: "deadbeef"}
		},
		func(context.Context, string, string) (bool, error) {
			return true, nil
		})
	ctx, cancel := context.WithCancel(context.Background())
	enrichment := startTurnGitEnrichment(ctx, "/repo")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := enrichment.Snapshot()["/repo"]; ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt64(scans); got != 0 {
		t.Fatalf("worktree scans after an interrupt = %d, want 0", got)
	}
	if workspace, ok := enrichment.Snapshot()["/repo"]; ok && workspace.HasChanges != nil {
		t.Fatal("an interrupted turn must not report a worktree status")
	}
}

// Rust enriches a freshly resolved single local environment only.
func TestTurnGitEnrichmentCWDSelectionLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		params *turn.TurnStartParams
		want   string
		ok     bool
	}{
		{name: "default local", params: &turn.TurnStartParams{CWD: "/repo"}, want: "/repo", ok: true},
		{name: "single local environment", params: &turn.TurnStartParams{
			CWD:          "/ignored",
			Environments: []map[string]any{{"cwd": "/env"}},
		}, want: "/env", ok: true},
		{name: "single remote environment", params: &turn.TurnStartParams{
			CWD:          "/repo",
			Environments: []map[string]any{{"type": "remote", "cwd": "/env"}},
		}, ok: false},
		{name: "several environments", params: &turn.TurnStartParams{
			CWD:          "/repo",
			Environments: []map[string]any{{"cwd": "/a"}, {"cwd": "/b"}},
		}, ok: false},
		{name: "no cwd", params: &turn.TurnStartParams{}, ok: false},
		{name: "nil params", params: nil, ok: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := turnGitEnrichmentCWD(testCase.params)
			if ok != testCase.ok || (ok && got != testCase.want) {
				t.Fatalf("turnGitEnrichmentCWD() = %q/%v, want %q/%v", got, ok, testCase.want, testCase.ok)
			}
		})
	}
}

// The collector reports a real repository's HEAD, remotes and dirty status.
func TestTurnGitEnrichmentReportsRealRepositoryLikeRust(t *testing.T) {
	if _, err := gitutil.Run(context.Background(), t.TempDir(), "--version"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	repo := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if _, err := gitutil.Run(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	for _, args := range [][]string{
		{"add", "tracked.txt"},
		{"commit", "-m", "initial"},
		{"remote", "add", "origin", "https://example.com/acme/repo.git"},
	} {
		if _, err := gitutil.Run(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	enrichment := startTurnGitEnrichment(ctx, repo)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := enrichment.Snapshot()
		if len(snapshot) > 0 {
			for _, workspace := range snapshot {
				if workspace.LatestGitCommitHash == "" {
					break
				}
				if workspace.HasChanges != nil {
					break
				}
				if workspace.AssociatedRemoteURLs["origin"] == "" {
					break
				}
				if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o600); err != nil {
					t.Fatalf("dirty tracked file: %v", err)
				}
				enrichment.Admit()
				for inner := time.Now().Add(10 * time.Second); time.Now().Before(inner); time.Sleep(10 * time.Millisecond) {
					observed := enrichment.Snapshot()
					for _, updated := range observed {
						if updated.HasChanges == nil || !*updated.HasChanges {
							continue
						}
						if updated.LatestGitCommitHash == "" {
							t.Fatal("a dirty report dropped the commit hash")
						}
						if !strings.Contains(updated.AssociatedRemoteURLs["origin"], "acme/repo") {
							t.Fatalf("remotes = %v", updated.AssociatedRemoteURLs)
						}
						return
					}
				}
				t.Fatalf("dirty status never published: %+v", enrichment.Snapshot())
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("repository observation never published: %+v", enrichment.Snapshot())
}

// The model request carries the turn's latest workspace observation, so the
// backend sees HEAD, remotes and the dirty status once each is available
// (Rust #62437).
func TestActiveTurnClientMetadataCarriesGitWorkspacesLikeRust(t *testing.T) {
	router := newTestRuntimeRouter()
	params := &turn.TurnStartParams{ThreadID: "thread-git", CWD: "/repo"}
	if err := router.threads.RegisterTurn("thread-git", "turn-git", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	hasChanges := true
	router.threads.UpdateTurn("thread-git", "turn-git", func(active *activeRuntimeTurn) {
		active.GitEnrichment = &turnGitEnrichment{workspaces: map[string]codexapi.ClientWorkspaceMetadata{
			"/repo": {
				LatestGitCommitHash:  "deadbeef",
				AssociatedRemoteURLs: map[string]string{"origin": "https://example.com/acme/repo"},
				HasChanges:           &hasChanges,
			},
		}}
	})

	client := router.activeTurnClientMetadata("thread-git", "turn-git", nil)
	document := decodeCompactionTurnMetadata(t, client[codexapi.ClientCodexTurnMetadataHeader])
	workspaces, ok := document["workspaces"].(map[string]any)
	if !ok {
		t.Fatalf("workspaces = %#v, want the turn's Git workspace", document["workspaces"])
	}
	workspace, ok := workspaces["/repo"].(map[string]any)
	if !ok {
		t.Fatalf("workspace = %#v", workspaces)
	}
	if workspace["latest_git_commit_hash"] != "deadbeef" {
		t.Fatalf("latest_git_commit_hash = %#v", workspace["latest_git_commit_hash"])
	}
	if workspace["has_changes"] != true {
		t.Fatalf("has_changes = %#v", workspace["has_changes"])
	}
	remotes, ok := workspace["associated_remote_urls"].(map[string]any)
	if !ok || remotes["origin"] != "https://example.com/acme/repo" {
		t.Fatalf("associated_remote_urls = %#v", workspace["associated_remote_urls"])
	}
}
