package appserver

import (
	"testing"

	"codex_go/session"
)

// Mirrors Rust #47113: create and fork capture the current authenticated
// identity, while resume preserves the creator recorded at creation.
func TestThreadCreatorIdentityCreateForkResumeLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	router.SetCreatorIdentity(func() (string, string) {
		return "user-parent", "account-parent"
	})

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	parentID := start.Result.(*ThreadStartResponse).Thread.ID
	parent, err := store.Read(session.ThreadID(parentID), true, false)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if parent.Metadata.CreatorUserID != "user-parent" || parent.Metadata.CreatorAccountID != "account-parent" {
		t.Fatalf("parent creator = %q/%q", parent.Metadata.CreatorUserID, parent.Metadata.CreatorAccountID)
	}
	if err := router.createThreadRollout(parent, parent.CreatedAt); err != nil {
		t.Fatalf("create parent rollout: %v", err)
	}

	router.SetCreatorIdentity(func() (string, string) {
		return "user-fork", "account-fork"
	})
	fork := router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{ThreadID: string(parentID)}))
	if fork.Error != nil {
		t.Fatalf("thread fork error: %+v", fork.Error)
	}
	forkID := fork.Result.(*ThreadForkResponse).Thread.ID
	forked, err := store.Read(session.ThreadID(forkID), true, false)
	if err != nil {
		t.Fatalf("read fork: %v", err)
	}
	if forked.Metadata.CreatorUserID != "user-fork" || forked.Metadata.CreatorAccountID != "account-fork" {
		t.Fatalf("fork creator = %q/%q", forked.Metadata.CreatorUserID, forked.Metadata.CreatorAccountID)
	}

	router.SetCreatorIdentity(func() (string, string) {
		return "user-resume", "account-resume"
	})
	resume := router.Handle(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: string(parentID)}))
	if resume.Error != nil {
		t.Fatalf("thread resume error: %+v", resume.Error)
	}
	resumed, err := store.Read(session.ThreadID(parentID), true, false)
	if err != nil {
		t.Fatalf("read resumed parent: %v", err)
	}
	if resumed.Metadata.CreatorUserID != "user-parent" || resumed.Metadata.CreatorAccountID != "account-parent" {
		t.Fatalf("resume overwrote creator = %q/%q", resumed.Metadata.CreatorUserID, resumed.Metadata.CreatorAccountID)
	}
}
