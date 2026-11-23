package execserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeDiscoveryV2Target struct {
	instance string
	session  string
	gate     <-chan struct{}

	mu          sync.Mutex
	calls       int
	failures    []error
	alwaysFail  error
	forceReload []bool
}

func (f *fakeDiscoveryV2Target) CapabilityDiscoveryInstanceID() string {
	return f.instance
}

func (f *fakeDiscoveryV2Target) CapabilityDiscoverySessionID(context.Context) (string, error) {
	return f.session, nil
}

func (f *fakeDiscoveryV2Target) DiscoverCapabilitiesV2(ctx context.Context, request *DiscoverV2CapabilitiesParams) (*DiscoverV2CapabilitiesResponse, error) {
	f.mu.Lock()
	f.calls++
	f.forceReload = append(f.forceReload, request != nil && request.ForceReload)
	err := f.alwaysFail
	if len(f.failures) > 0 {
		err = f.failures[0]
		f.failures = f.failures[1:]
	}
	f.mu.Unlock()
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &DiscoverV2CapabilitiesResponse{
		Skills: []ExecutorSkill{{Name: request.CWD, Description: "fixture"}},
	}, nil
}

func (f *fakeDiscoveryV2Target) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestExecutorCapabilityDiscoveryV2CacheCoalescesAndSeparatesContexts(t *testing.T) {
	gate := make(chan struct{})
	target := &fakeDiscoveryV2Target{instance: "environment-instance", session: "session-1", gate: gate}
	cache := &ExecutorCapabilityDiscoveryV2Cache{}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	type result struct {
		response *DiscoverV2CapabilitiesResponse
		err      error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			response, err := cache.Discover(context.Background(), "executor", target, request)
			results <- result{response: response, err: err}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for target.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent discovery errors = %v, %v", first.err, second.err)
	}
	if first.response != second.response {
		t.Fatal("concurrent discovery did not share the cached response")
	}
	if got := target.callCount(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}

	restricted := &DiscoverV2CapabilitiesParams{
		CWD: "file:///repo",
		Sandbox: &FileSystemSandboxContext{
			CWD: "file:///repo",
		},
	}
	if _, err := cache.Discover(context.Background(), "executor", target, restricted); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Discover(context.Background(), "executor", target, request); err != nil {
		t.Fatal(err)
	}
	if got := target.callCount(); got != 2 {
		t.Fatalf("sandbox-separated discovery calls = %d, want 2", got)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheBacksOffFailuresLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := &ExecutorCapabilityDiscoveryV2Cache{now: func() time.Time { return now }}
	target := &fakeDiscoveryV2Target{instance: "backoff", session: "session", alwaysFail: errors.New("discovery failed")}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	for attempt, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		_, err := cache.Discover(context.Background(), "executor", target, request)
		if err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt+1)
		}
		if got := target.callCount(); got != attempt+1 {
			t.Fatalf("attempt %d calls = %d", attempt+1, got)
		}
		now = now.Add(delay - time.Millisecond)
		suppressed, cooldownErr := cache.Discover(context.Background(), "executor", target, request)
		if cooldownErr == nil || suppressed != nil {
			t.Fatalf("cooldown lookup = %#v, %v", suppressed, cooldownErr)
		}
		if cooldownErr.Error() != err.Error() {
			t.Fatalf("cooldown error = %q, want cached %q", cooldownErr, err)
		}
		if got := target.callCount(); got != attempt+1 {
			t.Fatalf("cooldown lookup issued a request: calls = %d", got)
		}
		// Suppressed lookups must not move the deadline or advance the count.
		now = now.Add(time.Millisecond)
	}

	recovered := &fakeDiscoveryV2Target{instance: "backoff", session: "session"}
	response, err := cache.Discover(context.Background(), "executor", recovered, request)
	if err != nil || response == nil {
		t.Fatalf("recovery lookup = %#v, %v", response, err)
	}
	now = now.Add(300 * time.Second)
	reused, err := cache.Discover(context.Background(), "executor", recovered, request)
	if err != nil || reused != response {
		t.Fatalf("cached success = %#v, %v; want %#v", reused, err, response)
	}
	if got := recovered.callCount(); got != 1 {
		t.Fatalf("recovered calls = %d, want 1", got)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheCoalescesFailuresAndRetriesOnce(t *testing.T) {
	gate := make(chan struct{})
	now := time.Unix(1_700_000_000, 0)
	cache := &ExecutorCapabilityDiscoveryV2Cache{now: func() time.Time { return now }}
	failing := &fakeDiscoveryV2Target{
		instance: "coalesced", session: "session", gate: gate,
		failures: []error{errors.New("discovery failed")},
	}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}
	type result struct {
		response *DiscoverV2CapabilitiesResponse
		err      error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			response, err := cache.Discover(context.Background(), "executor", failing, request)
			results <- result{response: response, err: err}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for failing.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	first, second := <-results, <-results
	if first.err == nil || second.err == nil {
		t.Fatalf("coalesced failures = %v, %v", first.err, second.err)
	}
	if first.err.Error() != second.err.Error() {
		t.Fatalf("queued caller error = %q, want cached %q", second.err, first.err)
	}
	if got := failing.callCount(); got != 1 {
		t.Fatalf("coalesced calls = %d, want 1", got)
	}

	now = now.Add(time.Second)
	succeeding := &fakeDiscoveryV2Target{instance: "coalesced", session: "session"}
	recovered := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			response, err := cache.Discover(context.Background(), "executor", succeeding, request)
			recovered <- result{response: response, err: err}
		}()
	}
	first, second = <-recovered, <-recovered
	if first.err != nil || second.err != nil {
		t.Fatalf("recovered errors = %v, %v", first.err, second.err)
	}
	if first.response != second.response {
		t.Fatal("concurrent callers did not share the recovered response")
	}
	if got := succeeding.callCount(); got != 1 {
		t.Fatalf("recovered calls = %d, want one shared retry", got)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheCancellationAddsNoCooldownLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := &ExecutorCapabilityDiscoveryV2Cache{now: func() time.Time { return now }}
	gate := make(chan struct{})
	defer close(gate)
	target := &fakeDiscoveryV2Target{instance: "cancel", session: "session", gate: gate}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Discover(ctx, "executor", target, request); err == nil {
		t.Fatal("canceled discovery unexpectedly succeeded")
	}
	gate2 := make(chan struct{})
	target.gate = gate2
	close(gate2)
	response, err := cache.Discover(context.Background(), "executor", target, request)
	if err != nil || response == nil {
		t.Fatalf("discovery after cancellation = %#v, %v", response, err)
	}
	if got := target.callCount(); got != 2 {
		t.Fatalf("calls after cancellation = %d, want an immediate retry", got)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheInvalidationBypassesCooldownLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := &ExecutorCapabilityDiscoveryV2Cache{now: func() time.Time { return now }}
	target := &fakeDiscoveryV2Target{instance: "invalidate", session: "session", alwaysFail: errors.New("discovery failed")}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}

	if _, err := cache.Discover(context.Background(), "executor", target, request); err == nil {
		t.Fatal("initial discovery unexpectedly succeeded")
	}
	if _, err := cache.Discover(context.Background(), "executor", target, request); err == nil {
		t.Fatal("cached failure was retried during cooldown")
	} else if got := target.callCount(); got != 1 {
		t.Fatalf("cooldown calls = %d, want 1", got)
	}

	cache.Invalidate()
	response, err := cache.Discover(context.Background(), "executor", target, request)
	if err == nil {
		t.Fatalf("invalidated discovery = %#v, want a fresh failure", response)
	}
	if got := target.callCount(); got != 2 {
		t.Fatalf("invalidated calls = %d, want an immediate retry", got)
	}
	target.mu.Lock()
	reloads := append([]bool(nil), target.forceReload...)
	target.mu.Unlock()
	if len(reloads) != 2 || reloads[0] || !reloads[1] {
		t.Fatalf("forceReload requests = %#v, want [false true]", reloads)
	}

	// The forced reload survives its failed retry: the next lookup still asks
	// the executor to rescan.
	now = now.Add(time.Second)
	if _, err := cache.Discover(context.Background(), "executor", target, request); err == nil {
		t.Fatal("forced retry unexpectedly succeeded")
	}
	target.mu.Lock()
	reloads = append([]bool(nil), target.forceReload...)
	target.mu.Unlock()
	if len(reloads) != 3 || !reloads[2] {
		t.Fatalf("retried forceReload requests = %#v, want the last entry forced", reloads)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheEvictsFIFOAndSeparatesExecutorInstances(t *testing.T) {
	cache := &ExecutorCapabilityDiscoveryV2Cache{}
	target := &fakeDiscoveryV2Target{instance: "first", session: "session"}
	firstRequest := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}
	first, err := cache.Discover(context.Background(), "executor", target, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	cache.EnsureCapacity(17)
	for i := 0; i < 17; i++ {
		request := &DiscoverV2CapabilitiesParams{CWD: fmt.Sprintf("file:///repo/context-%d", i)}
		if _, err := cache.Discover(context.Background(), "executor", target, request); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := cache.Discover(context.Background(), "executor", target, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == first {
		t.Fatal("the oldest entry survived FIFO eviction")
	}
	if got := target.callCount(); got != 19 {
		t.Fatalf("FIFO discovery calls = %d, want 19", got)
	}

	replacement := &fakeDiscoveryV2Target{instance: "replacement", session: "session"}
	if _, err := cache.Discover(context.Background(), "executor", replacement, firstRequest); err != nil {
		t.Fatal(err)
	}
	if got := replacement.callCount(); got != 1 {
		t.Fatalf("replacement environment calls = %d, want 1", got)
	}
}
