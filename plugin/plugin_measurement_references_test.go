package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type measurementTestFetcher struct {
	calls   atomic.Int32
	bundle  *PluginMeasurementBundle
	err     error
	block   <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *measurementTestFetcher) FetchMeasurementBundle(ctx context.Context, _ PluginMeasurementTarget) (*PluginMeasurementBundle, error) {
	f.calls.Add(1)
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.bundle, f.err
}

func measurementTestBundle(t *testing.T, version string, materialized *atomic.Int32) *PluginMeasurementBundle {
	t.Helper()
	id, err := NewPluginId("demo", trustedRemoteMarketplaceName)
	if err != nil {
		t.Fatal(err)
	}
	return &PluginMeasurementBundle{
		RemotePluginID: "remote-demo",
		PluginID:       id,
		Version:        version,
		Materialize: func(_ context.Context, home string) (TrustedPluginRoots, error) {
			materialized.Add(1)
			root := filepath.Join(home, "plugins", "cache", trustedRemoteMarketplaceName, "demo", version)
			if err := os.MkdirAll(root, 0o755); err != nil {
				return TrustedPluginRoots{}, err
			}
			if err := os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				return TrustedPluginRoots{}, err
			}
			return TrustedPluginRoots{roots: []trustedPluginRoot{{pluginID: id.Key(), version: version, root: root}}}, nil
		},
	}
}

func TestMeasurementCacheReusesAuthenticatedVersionLikeRust(t *testing.T) {
	var materialized atomic.Int32
	fetcher := &measurementTestFetcher{bundle: measurementTestBundle(t, "1.0.0", &materialized)}
	cache := NewRemotePluginMeasurementCache()
	identity := PluginMeasurementIdentity{AuthIdentity: "account-a", Endpoint: "https://api.example.com"}
	target := PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"}

	first, err := cache.Prepare(context.Background(), identity, fetcher, target)
	if err != nil || first == nil {
		t.Fatalf("first Prepare() = %#v, %v", first, err)
	}
	if roots := first.Roots(); len(roots.roots) != 1 || roots.roots[0].version != "1.0.0" {
		t.Fatalf("roots = %#v", roots)
	}
	second, err := cache.Prepare(context.Background(), identity, fetcher, target)
	if err != nil || second == nil {
		t.Fatalf("second Prepare() = %#v, %v", second, err)
	}
	if fetcher.calls.Load() != 2 {
		t.Fatalf("authorization fetches = %d, want 2 (one per preparation)", fetcher.calls.Load())
	}
	if materialized.Load() != 1 {
		t.Fatalf("materializations = %d, want 1", materialized.Load())
	}
	home := first.home
	first.Close()
	second.Close()
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("cached reference home removed while still cached: %v", err)
	}
}

func TestMeasurementCacheScopesByAuthIdentityAndRetiresFilesLikeRust(t *testing.T) {
	var materialized atomic.Int32
	fetcher := &measurementTestFetcher{bundle: measurementTestBundle(t, "1.0.0", &materialized)}
	cache := NewRemotePluginMeasurementCache()
	target := PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"}

	first, err := cache.Prepare(context.Background(), PluginMeasurementIdentity{AuthIdentity: "account-a", Endpoint: "https://api.example.com"}, fetcher, target)
	if err != nil || first == nil {
		t.Fatalf("first Prepare() = %#v, %v", first, err)
	}
	firstHome := first.home
	second, err := cache.Prepare(context.Background(), PluginMeasurementIdentity{AuthIdentity: "account-b", Endpoint: "https://api.example.com"}, fetcher, target)
	if err != nil || second == nil {
		t.Fatalf("second Prepare() = %#v, %v", second, err)
	}
	if second.home == firstHome {
		t.Fatal("auth identities shared one reference home")
	}
	if materialized.Load() != 2 {
		t.Fatalf("materializations = %d, want 2", materialized.Load())
	}
	// The retired entry keeps its files leased while the command still holds
	// the reference, then removes them when it closes.
	first.Close()
	if _, err := os.Stat(firstHome); !os.IsNotExist(err) {
		t.Fatalf("retired reference home still exists after Close: %v", err)
	}
	second.Close()
}

func TestMeasurementCacheSharesConcurrentPreparationLikeRust(t *testing.T) {
	var materialized atomic.Int32
	release := make(chan struct{})
	fetcher := &measurementTestFetcher{
		bundle:  measurementTestBundle(t, "1.0.0", &materialized),
		block:   release,
		started: make(chan struct{}),
	}
	cache := NewRemotePluginMeasurementCache()
	identity := PluginMeasurementIdentity{AuthIdentity: "account-a", Endpoint: "https://api.example.com"}
	target := PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"}

	firstDone := make(chan *PluginMeasurementReference, 1)
	go func() {
		reference, _ := cache.Prepare(context.Background(), identity, fetcher, target)
		firstDone <- reference
	}()
	<-fetcher.started
	secondDone := make(chan *PluginMeasurementReference, 1)
	go func() {
		reference, _ := cache.Prepare(context.Background(), identity, fetcher, target)
		secondDone <- reference
	}()
	// Give the second caller time to attach to the in-flight preparation.
	time.Sleep(20 * time.Millisecond)
	close(release)
	first := <-firstDone
	second := <-secondDone
	if first == nil || second == nil || first != second {
		t.Fatalf("concurrent references = %#v %#v", first, second)
	}
	if materialized.Load() != 1 {
		t.Fatalf("materializations = %d, want 1", materialized.Load())
	}
	first.Close()
	second.Close()
}

func TestMeasurementCacheReusesHistoricalVersionAfterReleaseUpdateLikeRust(t *testing.T) {
	var materialized atomic.Int32
	fetcher := &measurementTestFetcher{bundle: measurementTestBundle(t, "2.0.0", &materialized)}
	cache := NewRemotePluginMeasurementCache()
	identity := PluginMeasurementIdentity{AuthIdentity: "account-a", Endpoint: "https://api.example.com"}
	target := PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0", WindowsPathConvention: true}

	first, err := cache.Prepare(context.Background(), identity, fetcher, target)
	if err != nil || first == nil {
		t.Fatalf("first Prepare() = %#v, %v", first, err)
	}
	second, err := cache.Prepare(context.Background(), identity, fetcher, target)
	if err != nil || second == nil {
		t.Fatalf("second Prepare() = %#v, %v", second, err)
	}
	if materialized.Load() != 1 {
		t.Fatalf("historical hint downloaded the release again: %d", materialized.Load())
	}
	first.Close()
	second.Close()
}

func TestMeasurementCacheCancelledWaiterLeavesSharedPreparationLikeRust(t *testing.T) {
	var materialized atomic.Int32
	release := make(chan struct{})
	fetcher := &measurementTestFetcher{
		bundle:  measurementTestBundle(t, "1.0.0", &materialized),
		block:   release,
		started: make(chan struct{}),
	}
	cache := NewRemotePluginMeasurementCache()
	identity := PluginMeasurementIdentity{AuthIdentity: "account-a", Endpoint: "https://api.example.com"}
	target := PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"}

	firstDone := make(chan *PluginMeasurementReference, 1)
	go func() {
		reference, _ := cache.Prepare(context.Background(), identity, fetcher, target)
		firstDone <- reference
	}()
	<-fetcher.started
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if reference, err := cache.Prepare(waitCtx, identity, fetcher, target); reference != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %#v, %v", reference, err)
	}
	close(release)
	first := <-firstDone
	if first == nil || materialized.Load() != 1 {
		t.Fatalf("shared preparation lost: ref=%#v materialized=%d", first, materialized.Load())
	}
	first.Close()
}

func TestMeasurementCacheReportsSanitizedPreparationFailureLikeRust(t *testing.T) {
	fetcher := &measurementTestFetcher{err: errors.New("https://signed.example/bundle?token=secret")}
	cache := NewRemotePluginMeasurementCache()
	reference, err := cache.Prepare(context.Background(), PluginMeasurementIdentity{AuthIdentity: "a"}, fetcher, PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"})
	if reference != nil || err == nil {
		t.Fatalf("Prepare() = %#v, %v", reference, err)
	}
	if err.Error() != "plugin measurement catalog lookup failed" {
		t.Fatalf("error = %q", err.Error())
	}
	// A completed failure is not cached as a permanent result: a new caller
	// retries the authenticated lookup.
	if reference, err := cache.Prepare(context.Background(), PluginMeasurementIdentity{AuthIdentity: "a"}, fetcher, PluginMeasurementTarget{PluginName: "demo", Version: "1.0.0"}); reference != nil || err == nil {
		t.Fatalf("retry Prepare() = %#v, %v", reference, err)
	}
	if fetcher.calls.Load() != 2 {
		t.Fatalf("fetches = %d, want 2", fetcher.calls.Load())
	}
}
