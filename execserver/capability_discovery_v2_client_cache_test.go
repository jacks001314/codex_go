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

	mu       sync.Mutex
	calls    int
	failures []error
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
	var err error
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

func TestExecutorCapabilityDiscoveryV2CacheRetriesOnlyTransientFailures(t *testing.T) {
	permanent := &fakeDiscoveryV2Target{
		instance: "permanent", session: "session",
		failures: []error{errors.New("fixture server failure")},
	}
	cache := &ExecutorCapabilityDiscoveryV2Cache{}
	request := &DiscoverV2CapabilitiesParams{CWD: "file:///repo"}
	for i := 0; i < 2; i++ {
		if _, err := cache.Discover(context.Background(), "executor", permanent, request); err == nil {
			t.Fatal("permanent discovery failure unexpectedly succeeded")
		}
	}
	if got := permanent.callCount(); got != 1 {
		t.Fatalf("permanent failure calls = %d, want cached failure", got)
	}
	if cache.TakeRecoveredDiscovery() {
		t.Fatal("permanent failure reported recovery")
	}

	transient := &fakeDiscoveryV2Target{
		instance: "transient", session: "session",
		failures: []error{&clientTransportError{err: errors.New("disconnected")}},
	}
	if _, err := cache.Discover(context.Background(), "executor", transient, request); err == nil {
		t.Fatal("transient discovery failure unexpectedly succeeded")
	}
	if cache.TakeRecoveredDiscovery() {
		t.Fatal("transient failure reported recovery before success")
	}
	response, err := cache.Discover(context.Background(), "executor", transient, request)
	if err != nil || response == nil {
		t.Fatalf("retried discovery = %#v, %v", response, err)
	}
	if !cache.TakeRecoveredDiscovery() {
		t.Fatal("successful retry did not report recovery")
	}
	if cache.TakeRecoveredDiscovery() {
		t.Fatal("recovery observation was not consumed")
	}
	reused, err := cache.Discover(context.Background(), "executor", transient, request)
	if err != nil || reused != response {
		t.Fatalf("cached retry = %#v, %v; want %#v", reused, err, response)
	}
	if got := transient.callCount(); got != 2 {
		t.Fatalf("transient failure calls = %d, want failure plus one retry", got)
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
