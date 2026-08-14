package execserver

import (
	"errors"
	"testing"
)

func TestCapabilityDiscoveryCacheMemoizesSuccessfulResults(t *testing.T) {
	cache := NewCapabilityDiscoveryCache()
	params := &CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "root-1", Path: "/workspace"}}}
	if _, ok := cache.Get(params); ok {
		t.Fatal("empty cache returned a value")
	}
	response := &CapabilityRootsDiscoverResponse{Roots: []CapabilityRootDiscovery{{ID: "root-1"}}}
	cache.Put(params, response, nil, false)
	got, ok := cache.Get(params)
	if !ok || got.response == nil || len(got.response.Roots) != 1 || got.response.Roots[0].ID != "root-1" {
		t.Fatalf("cached response = %#v, ok=%v", got, ok)
	}
	if _, ok := (&CapabilityDiscoveryCache{}).Get(params); ok {
		t.Fatal("nil-entries cache returned a value")
	}
}

func TestCapabilityDiscoveryCacheRetriesTransientFailuresAndCachesPermanent(t *testing.T) {
	cache := NewCapabilityDiscoveryCache()
	params := &CapabilityRootsDiscoverParams{Roots: []CapabilityRootDiscoverRequest{{ID: "root-1", Path: "/workspace"}}}

	// Transient (retryable) failures are not cached, so the next request retries.
	cache.Put(params, nil, errors.New("transport disconnected"), true)
	if _, ok := cache.Get(params); ok {
		t.Fatal("transient failure should be treated as a miss")
	}

	// Permanent failures are cached and returned without a retry.
	cache.Put(params, nil, errors.New("protocol error"), false)
	got, ok := cache.Get(params)
	if !ok || got.err == nil || got.err.Error() != "protocol error" {
		t.Fatalf("permanent failure entry = %#v, ok=%v", got, ok)
	}

	// A later success replaces the failure and reports recovery.
	cache.Put(params, &CapabilityRootsDiscoverResponse{Roots: []CapabilityRootDiscovery{{ID: "root-1"}}}, nil, false)
	if !cache.TakeRecoveredDiscovery() {
		t.Fatal("successful recovery should be reported")
	}
	if cache.TakeRecoveredDiscovery() {
		t.Fatal("recovery flag should be cleared after observation")
	}
}
