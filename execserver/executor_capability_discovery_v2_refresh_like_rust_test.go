package execserver

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"codex_go/utils"
)

// refreshTrackingTarget records whether each discovery asked the executor to
// rescan its files (Rust #62675 force_reload).
type refreshTrackingTarget struct {
	instance string
	session  string
	gate     <-chan struct{}

	mu     sync.Mutex
	calls  int
	forces []bool
	name   string
}

func (f *refreshTrackingTarget) CapabilityDiscoveryInstanceID() string { return f.instance }

func (f *refreshTrackingTarget) CapabilityDiscoverySessionID(context.Context) (string, error) {
	return f.session, nil
}

func (f *refreshTrackingTarget) DiscoverCapabilitiesV2(ctx context.Context, request *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	f.mu.Lock()
	f.calls++
	f.forces = append(f.forces, request.ForceReload)
	name, gate := f.name, f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &DiscoverV2CapabilitiesResponse{Skills: []ExecutorSkill{{Name: name, Description: "fixture"}}}, nil
}

func (f *refreshTrackingTarget) setName(name string) {
	f.mu.Lock()
	f.name = name
	f.mu.Unlock()
}

func (f *refreshTrackingTarget) snapshot() (int, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]bool(nil), f.forces...)
}

// Rust #62640/#62675: invalidating a thread's cache reloads each context on its
// next lookup, forwards the rescan flag, and the refreshed entry is reusable.
func TestExecutorCapabilityDiscoveryV2CacheInvalidationForcesReloadLikeRust(t *testing.T) {
	cache := &ExecutorCapabilityDiscoveryV2Cache{}
	target := &refreshTrackingTarget{instance: "environment-instance", session: "session-1", name: "first"}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	first, err := cache.Discover(context.Background(), "executor", target, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Discover(context.Background(), "executor", target, request); err != nil {
		t.Fatal(err)
	}
	calls, forces := target.snapshot()
	if calls != 1 || len(forces) != 1 || forces[0] {
		t.Fatalf("clean cache calls=%d forces=%v, want one ordinary load", calls, forces)
	}

	target.setName("second")
	cache.Invalidate()
	refreshed, err := cache.Discover(context.Background(), "executor", target, request)
	if err != nil {
		t.Fatal(err)
	}
	calls, forces = target.snapshot()
	if calls != 2 || len(forces) != 2 || !forces[1] {
		t.Fatalf("invalidated cache calls=%d forces=%v, want a forced rescan", calls, forces)
	}
	if refreshed == first || len(refreshed.Skills) != 1 || refreshed.Skills[0].Name != "second" {
		t.Fatalf("refreshed inventory = %#v", refreshed)
	}

	reused, err := cache.Discover(context.Background(), "executor", target, request)
	if err != nil {
		t.Fatal(err)
	}
	calls, _ = target.snapshot()
	if calls != 2 || reused != refreshed {
		t.Fatalf("refreshed entry was not reused: calls=%d", calls)
	}
}

// Rust #62640: invalidation during a load applies to the next lookup; readers
// already waiting keep the inventory they captured.
func TestExecutorCapabilityDiscoveryV2CacheInvalidationDuringLoadLikeRust(t *testing.T) {
	gate := make(chan struct{})
	target := &refreshTrackingTarget{instance: "environment-instance", session: "session-1", gate: gate, name: "old"}
	cache := &ExecutorCapabilityDiscoveryV2Cache{}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	type result struct {
		response *DiscoverV2CapabilitiesResponse
		err      error
	}
	firstResult := make(chan result, 1)
	go func() {
		response, err := cache.Discover(context.Background(), "executor", target, request)
		firstResult <- result{response: response, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		calls, _ := target.snapshot()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery load did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cache.Invalidate()
	target.setName("new")
	close(gate)
	first := <-firstResult
	if first.err != nil || first.response == nil || len(first.response.Skills) != 1 || first.response.Skills[0].Name != "old" {
		t.Fatalf("in-flight reader = %#v, %v", first.response, first.err)
	}

	next, err := cache.Discover(context.Background(), "executor", target, request)
	if err != nil {
		t.Fatal(err)
	}
	_, forces := target.snapshot()
	if len(forces) != 2 || !forces[1] {
		t.Fatalf("forces after invalidation = %v, want the next lookup to rescan", forces)
	}
	if next == nil || len(next.Skills) != 1 || next.Skills[0].Name != "new" {
		t.Fatalf("next inventory = %#v", next)
	}
}

// Rust #62675: forceReload asks the executor-side manager to rescan files
// instead of reusing the watcher-backed snapshot, and the refreshed inventory
// replaces the cached entry.
func TestV2DiscoveryManagerForceReloadRescansFilesLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	writeV2File(t, filepath.Join(codexHome, "skills", "demo", "SKILL.md"), "---\nname: demo\ndescription: Demo\n---\n")
	cwd, err := utils.FromHostNativePath(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	manager := newV2DiscoveryManager()
	params := &DiscoverV2CapabilitiesParams{CWD: cwd.String()}
	if _, err := manager.Snapshot(params); err != nil {
		t.Fatal(err)
	}
	writeV2File(t, filepath.Join(codexHome, "skills", "extra", "SKILL.md"), "---\nname: extra\ndescription: Extra\n---\n")

	reloaded, err := manager.Snapshot(&DiscoverV2CapabilitiesParams{CWD: cwd.String(), ForceReload: true})
	if err != nil {
		t.Fatal(err)
	}
	if !v2DiscoveryHasSkill(reloaded, "extra") {
		t.Fatalf("forced reload missed the new skill: %#v", reloaded)
	}
	cached, err := manager.Snapshot(params)
	if err != nil {
		t.Fatal(err)
	}
	if !v2DiscoveryHasSkill(cached, "extra") {
		t.Fatalf("ordinary lookup lost the refreshed inventory: %#v", cached)
	}
}

func v2DiscoveryHasSkill(response *DiscoverV2CapabilitiesResponse, name string) bool {
	if response == nil {
		return false
	}
	for _, skill := range response.Skills {
		if skill.Name == name {
			return true
		}
	}
	return false
}
