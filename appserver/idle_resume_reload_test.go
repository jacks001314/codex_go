package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/sandbox"
	"codex_go/session"
)

func dropThreadSubscribersForTest(router *RuntimeRouter, id string) {
	for _, connectionID := range router.threads.Subscribers(id) {
		router.threads.Unsubscribe(id, connectionID)
	}
}

// Rust #61136: an idle loaded thread with no subscribers is a cache entry. A
// resume whose overrides disagree with the live runtime closes it and lets the
// cold resume read the rollout again; a matching request keeps it.
func TestUnsubscribedIdleResumeReloadsOnOverrideMismatchLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: extras, ThreadStatus: NewThreadStatusManager()})
	defer router.Close()
	cwd := t.TempDir()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd, Model: "live-model", Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	dropThreadSubscribersForTest(router, id)
	liveSandbox := "read-only"
	liveRaw := json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"}`)
	if _, _, err := extras.UpdateSettingsWithCommit(&SettingsUpdateParams{ThreadID: id, SandboxPolicy: &liveSandbox, normalizedPermissionProfile: liveRaw}); err != nil {
		t.Fatal(err)
	}
	if !router.threadIsLoaded(id) {
		t.Fatal("expected a loaded thread")
	}
	if router.activeRuntimeTurnSnapshot(id) != nil {
		t.Fatal("expected an idle thread")
	}
	if subscribers := len(router.threads.Subscribers(id)); subscribers != 0 {
		t.Fatalf("subscribers = %d, want none", subscribers)
	}

	matching := "read-only"
	if _, err := router.prepareUnsubscribedIdleResume(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Sandbox: &matching})); err != nil {
		t.Fatal(err)
	}
	if !router.threadIsLoaded(id) {
		t.Fatal("matching resume rebuilt the live runtime")
	}

	mismatch := "workspace-write"
	if _, err := router.prepareUnsubscribedIdleResume(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: id, Sandbox: &mismatch})); err != nil {
		t.Fatal(err)
	}
	if router.threadIsLoaded(id) {
		t.Fatal("mismatched resume kept the idle runtime loaded")
	}

	resumed := router.Handle(requestWithParams(t, IntID(4), MethodThreadResume, ThreadResumeParams{ThreadID: id, Sandbox: &mismatch, ExcludeTurns: true}))
	if resumed.Error != nil {
		t.Fatal(resumed.Error)
	}
	response := resumed.Result.(*ThreadResumeResponse)
	if got := resumeSandboxKind(t, response.Sandbox); got != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("resumed sandbox = %s, want workspace-write", got)
	}
	if !router.threadIsLoaded(id) {
		t.Fatal("cold resume did not load the thread")
	}
}

// A named permissions override is always reported as a mismatch while the
// runtime is idle, matching Rust's "provided and ignored while running" rule.
func TestUnsubscribedIdleResumeTreatsPermissionOverrideAsMismatch(t *testing.T) {
	store := session.NewStore(t.TempDir())
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: extras, ThreadStatus: NewThreadStatusManager()})
	defer router.Close()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	dropThreadSubscribersForTest(router, id)
	profile := sandbox.BuiltInPermissionProfileDangerFullAccess
	if _, err := router.prepareUnsubscribedIdleResume(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Permissions: &profile})); err != nil {
		t.Fatal(err)
	}
	if router.threadIsLoaded(id) {
		t.Fatal("permission override did not rebuild the idle runtime")
	}
}

// Busy or subscribed threads keep their runtime regardless of the overrides.
func TestUnsubscribedIdleResumeSkipsLiveThreadsAndSubscribers(t *testing.T) {
	store := session.NewStore(t.TempDir())
	extras := NewThreadExtraService()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), ThreadExtras: extras, ThreadStatus: NewThreadStatusManager()})
	defer router.Close()
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "seed"}))
	if start.Error != nil {
		t.Fatal(start.Error)
	}
	id := start.Result.(*ThreadStartResponse).Thread.ID
	materializeThreadRolloutForTest(t, router.services.ThreadRouter, store, id)
	dropThreadSubscribersForTest(router, id)
	router.threads.Subscribe(id, "observer")
	mismatch := "workspace-write"
	if _, err := router.prepareUnsubscribedIdleResume(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{ThreadID: id, Sandbox: &mismatch})); err != nil {
		t.Fatal(err)
	}
	if !router.threadIsLoaded(id) {
		t.Fatal("subscribed idle resume rebuilt the live runtime")
	}
	if _, err := router.prepareUnsubscribedIdleResume(requestWithParams(t, IntID(3), MethodThreadResume, ThreadResumeParams{ThreadID: "not-loaded", Sandbox: &mismatch})); err != nil {
		t.Fatal(err)
	}
}
