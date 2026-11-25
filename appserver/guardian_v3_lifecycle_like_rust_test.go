package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/session"
)

// guardianV3LifecycleRouter builds a router whose config enables Guardian v3.
func guardianV3LifecycleRouter(t *testing.T, enabled bool) (*RuntimeRouter, *session.Store, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home),
		[]byte("[features.guardianv3]\nenabled = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !enabled {
		if err := os.WriteFile(config.ConfigPath(home),
			[]byte("[features.guardianv3]\nenabled = false\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	return router, store, home
}

// Rust guardian-v3 host.rs thread lifecycle: retaining a root starts its worker,
// a child thread leaves the tree root in charge, and releasing the root stops it.
func TestGuardianV3HostFollowsThreadLifecycleLikeRust(t *testing.T) {
	router, store, home := guardianV3LifecycleRouter(t, true)
	now := time.Now().UTC()
	root := session.ThreadID("guardian-lifecycle-root")
	child := session.ThreadID("guardian-lifecycle-child")
	for _, record := range []*session.Record{
		{ID: root, SessionID: string(root), CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: home}},
		{ID: child, ParentThreadID: root, SessionID: string(child), CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: home}},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("create %s: %v", record.ID, err)
		}
	}
	sampled := make(chan agentboard.GuardianRoutingBatch, 4)
	router.guardianV3Sampler = func(_ context.Context, instructions string, input string) (string, error) {
		sampled <- agentboard.GuardianRoutingBatch{Sources: []agentboard.GuardianRoutingSource{{ID: agentboard.GuardianEvidenceID{Ordinal: 1}, Text: input}}}
		return `{"routes":[["release"]],"descriptions":[{"channel":"release","description":"Shipping"}]}`, nil
	}

	rootRecord, err := store.Read(root, true, false)
	if err != nil || rootRecord == nil {
		t.Fatalf("read root record: %v", err)
	}
	if err := router.threads.RetainLiveThread(store, rootRecord); err != nil {
		t.Fatalf("RetainLiveThread(root) error = %v", err)
	}
	if _, ok := router.guardianV3Host.Root(string(root)); !ok {
		t.Fatal("retaining the root did not start its Guardian worker")
	}
	childRecord, err := store.Read(child, true, false)
	if err != nil || childRecord == nil {
		t.Fatalf("read child record: %v", err)
	}
	if err := router.threads.RetainLiveThread(store, childRecord); err != nil {
		t.Fatalf("RetainLiveThread(child) error = %v", err)
	}
	if _, ok := router.guardianV3Host.Root(string(child)); ok {
		t.Fatal("a child thread started its own Guardian worker")
	}

	// A finished turn wakes the root, and the originals committed for it route.
	if _, err := store.AppendItems(root, []session.Item{guardianOriginalItem("lifecycle-1", "user", "ship the change", time.Now().UTC())}); err != nil {
		t.Fatalf("append item: %v", err)
	}
	cancel := func() {}
	if err := router.threads.RegisterTurn(string(root), "turn-lifecycle", cancel, time.Now().UnixMilli(), nil); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if _, ok := router.threads.ConsumeTurn(string(root), "turn-lifecycle", false); !ok {
		t.Fatal("ConsumeTurn() did not finish the turn")
	}
	select {
	case <-sampled:
	case <-time.After(5 * time.Second):
		t.Fatal("turn end never woke the Guardian routing worker")
	}

	router.threads.ReleaseLiveThreads([]session.ThreadID{root})
	if _, ok := router.guardianV3Host.Root(string(root)); ok {
		t.Fatal("releasing the root left its Guardian worker running")
	}
}

// Without features.guardianv3 the host never starts a worker.
func TestGuardianV3HostStaysOffWithoutFeatureLikeRust(t *testing.T) {
	router, store, home := guardianV3LifecycleRouter(t, false)
	now := time.Now().UTC()
	root := session.ThreadID("guardian-disabled-root")
	if err := store.Create(&session.Record{ID: root, SessionID: string(root), CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: home}}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	record, err := store.Read(root, true, false)
	if err != nil || record == nil {
		t.Fatalf("read root: %v", err)
	}
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("RetainLiveThread() error = %v", err)
	}
	if _, ok := router.guardianV3Host.Root(string(root)); ok {
		t.Fatal("Guardian worker started with the feature disabled")
	}
}

// The routing input reaches a resume at its durable tail and a new root at the
// beginning (Rust EvidenceCursor::at_thread_ready).
func TestGuardianV3StartPositionLikeRust(t *testing.T) {
	store, root := guardianOriginalHistoryFixture(t)
	start := guardianV3StartPosition(store, string(root))
	if start.EndOrdinalExclusive != 3 {
		t.Fatalf("resume start = %#v, want the durable tail 3", start)
	}
	fresh := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	freshRoot := session.ThreadID("guardian-fresh-root")
	if err := fresh.Create(&session.Record{ID: freshRoot, SessionID: string(freshRoot), CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("create fresh root: %v", err)
	}
	if got := guardianV3StartPosition(fresh, string(freshRoot)); got.EndOrdinalExclusive != 0 {
		t.Fatalf("fresh start = %#v, want the beginning", got)
	}
}
