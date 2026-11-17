package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/gitutil"
)

const (
	memoryGitTestCommit = "0123456789abcdef0123456789abcdef01234567"
	memoryGitOtherHead  = "89abcdef0123456789abcdef0123456789abcdef"
)

// TestMemoryGitCollectorPublishesPartialMetadataLikeRust mirrors Rust #61939:
// HEAD and verified remote URLs observed before a slow worktree scan still
// reach the memory request instead of being discarded.
func TestMemoryGitCollectorPublishesPartialMetadataLikeRust(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	statusRelease := make(chan struct{})
	remotesPublished := make(chan struct{})
	collector := newMemoryGitCollectorWithSources(memoryGitSources{
		head: func(context.Context, string) gitutil.GitHeadState {
			return gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: memoryGitTestCommit}
		},
		status: func(statusCtx context.Context, _, _ string) (*gitutil.GitWorktreeStatus, error) {
			<-statusRelease
			return nil, statusCtx.Err()
		},
		observeRemotes: func(remotesCtx context.Context, _ string, publish func(map[string]string)) {
			publish(map[string]string{"origin": "https://example.com/repo.git"})
			close(remotesPublished)
			<-remotesCtx.Done()
		},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		collector.collect(ctx, "/repo", "/repo")
	}()
	select {
	case <-remotesPublished:
	case <-time.After(2 * time.Second):
		t.Fatal("remotes were not published")
	}
	// HEAD resolves immediately, so it must already be visible while the
	// status scan is still running.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if snapshot := collector.snapshot(); snapshot.LatestGitCommitHash == memoryGitTestCommit {
			if snapshot.AssociatedRemoteURLs["origin"] != "https://example.com/repo.git" {
				t.Fatalf("remotes = %v", snapshot.AssociatedRemoteURLs)
			}
			if snapshot.HasChanges != nil {
				t.Fatalf("HasChanges = %v, want nil while the status scan runs", *snapshot.HasChanges)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HEAD was not published before the deadline")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	close(statusRelease)
	<-done
}

// TestMemoryGitCollectorAppliesStatusOnlyWhenHeadMatchesLikeRust mirrors Rust
// #61939: a shared scan may be stale, so its status only counts for a matching
// HEAD (or an unavailable independent lookup).
func TestMemoryGitCollectorAppliesStatusOnlyWhenHeadMatchesLikeRust(t *testing.T) {
	commit := memoryGitTestCommit
	other := memoryGitOtherHead
	cases := []struct {
		name        string
		head        gitutil.GitHeadState
		status      *gitutil.GitWorktreeStatus
		wantHash    string
		wantChanges *bool
	}{
		{
			name:        "matching commit",
			head:        gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: commit},
			status:      &gitutil.GitWorktreeStatus{HeadCommitHash: &commit, HasChanges: true},
			wantHash:    commit,
			wantChanges: boolPointer(true),
		},
		{
			name:     "conflicting commit",
			head:     gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: commit},
			status:   &gitutil.GitWorktreeStatus{HeadCommitHash: &other, HasChanges: true},
			wantHash: commit,
		},
		{
			name:        "unavailable head accepts status",
			head:        gitutil.GitHeadState{Kind: gitutil.GitHeadUnavailable},
			status:      &gitutil.GitWorktreeStatus{HeadCommitHash: &other, HasChanges: true},
			wantHash:    other,
			wantChanges: boolPointer(true),
		},
		{
			name:        "unborn status for unborn head",
			head:        gitutil.GitHeadState{Kind: gitutil.GitHeadMissing},
			status:      &gitutil.GitWorktreeStatus{HasChanges: false},
			wantChanges: boolPointer(false),
		},
		{
			name:     "committed status for unborn head",
			head:     gitutil.GitHeadState{Kind: gitutil.GitHeadMissing},
			status:   &gitutil.GitWorktreeStatus{HeadCommitHash: &commit, HasChanges: true},
			wantHash: "",
		},
		{
			name:     "status unavailable",
			head:     gitutil.GitHeadState{Kind: gitutil.GitHeadCommit, Commit: commit},
			status:   nil,
			wantHash: commit,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			collector := newMemoryGitCollectorWithSources(memoryGitSources{
				head: func(context.Context, string) gitutil.GitHeadState { return testCase.head },
				status: func(context.Context, string, string) (*gitutil.GitWorktreeStatus, error) {
					return testCase.status, nil
				},
				observeRemotes: func(context.Context, string, func(map[string]string)) {},
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				collector.collect(context.Background(), "/repo", "/repo")
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("collector did not finish")
			}
			snapshot := collector.snapshot()
			if snapshot.LatestGitCommitHash != testCase.wantHash {
				t.Fatalf("LatestGitCommitHash = %q, want %q", snapshot.LatestGitCommitHash, testCase.wantHash)
			}
			switch {
			case testCase.wantChanges == nil && snapshot.HasChanges != nil:
				t.Fatalf("HasChanges = %v, want nil", *snapshot.HasChanges)
			case testCase.wantChanges != nil && (snapshot.HasChanges == nil || *snapshot.HasChanges != *testCase.wantChanges):
				t.Fatalf("HasChanges = %v, want %v", snapshot.HasChanges, *testCase.wantChanges)
			}
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}
