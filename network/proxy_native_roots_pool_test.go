package network

import (
	"crypto/x509"
	"sync/atomic"
	"testing"
	"time"
)

// countingPlatformLoader replaces the platform read seam and counts reads.
// hold, when non-nil, is called from the first read and blocks it until it
// returns; started, when non-nil, is closed when that first read begins.
func countingPlatformLoader(t *testing.T, reads *int32, started chan<- struct{}, hold func()) {
	t.Helper()
	original := loadPlatformRootPool
	// A caching platform loader, like the memoized pool the runtime hands out
	// on hosts where the platform store is loaded once.
	shared := x509.NewCertPool()
	loadPlatformRootPool = func() *x509.CertPool {
		if atomic.AddInt32(reads, 1) == 1 && hold != nil {
			if started != nil {
				close(started)
			}
			hold()
		}
		return shared
	}
	t.Cleanup(func() { loadPlatformRootPool = original })
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
}

// TestPlatformRootPoolCloneCoalescesInFlightLoadsLikeRust mirrors Rust #54056
// (`http-client/src/native_roots.rs`): callers that overlap share one platform
// read (`NativeRootsLoader`), and the snapshot is not retained, so a later
// caller reads the platform store again.
func TestPlatformRootPoolCloneCoalescesInFlightLoadsLikeRust(t *testing.T) {
	var reads int32
	started := make(chan struct{})
	release := make(chan struct{})
	countingPlatformLoader(t, &reads, started, func() { <-release })

	first := make(chan *x509.CertPool, 1)
	go func() { first <- PlatformRootPoolClone() }()
	<-started

	// A second caller arriving while the read is in flight must join it instead
	// of starting another read. It can only return early by doing its own read,
	// so waiting for it here cannot lose a legitimately blocked caller.
	second := make(chan *x509.CertPool, 1)
	go func() { second <- PlatformRootPoolClone() }()
	select {
	case <-second:
		t.Fatal("the overlapping caller returned while the platform read was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	firstPool, secondPool := <-first, <-second
	if got := atomic.LoadInt32(&reads); got != 1 {
		t.Fatalf("platform reads = %d, want 1 shared in-flight read", got)
	}
	if firstPool == nil || secondPool == nil {
		t.Fatalf("clones = %v, %v, want pools", firstPool, secondPool)
	}

	// No retention: a sequential caller reads the platform store again.
	PlatformRootPoolClone()
	if got := atomic.LoadInt32(&reads); got != 2 {
		t.Fatalf("platform reads after a later caller = %d, want 2 (no retained snapshot)", got)
	}
}

// TestPlatformRootPoolCloneAlwaysReturnsAnIndependentPool pins the clone itself:
// appending a custom bundle to one caller's pool must not reach another caller
// or the platform store, which is what Rust's `(*load()).clone()` guarantees.
func TestPlatformRootPoolCloneAlwaysReturnsAnIndependentPool(t *testing.T) {
	var reads int32
	countingPlatformLoader(t, &reads, nil, nil)

	platform := loadPlatformRootPool()
	first := PlatformRootPoolClone()
	second := PlatformRootPoolClone()
	if first == platform || second == platform {
		t.Fatal("PlatformRootPoolClone must not hand out the process-wide platform pool")
	}
	if first == second {
		t.Fatal("PlatformRootPoolClone must return a fresh pool per caller")
	}
}

// TestPlatformRootPoolCloneBypassesCoalescingForOverrides pins the override arm
// of the Rust loader: `SSL_CERT_FILE` / `SSL_CERT_DIR` replace the platform
// store, so their contents are re-read for every build instead of being shared.
func TestPlatformRootPoolCloneBypassesCoalescingForOverrides(t *testing.T) {
	var reads int32
	started := make(chan struct{})
	release := make(chan struct{})
	countingPlatformLoader(t, &reads, started, func() { <-release })
	t.Setenv("SSL_CERT_FILE", `C:\overridden\ca.pem`)

	first := make(chan *x509.CertPool, 1)
	go func() { first <- PlatformRootPoolClone() }()
	<-started
	second := make(chan *x509.CertPool, 1)
	go func() { second <- PlatformRootPoolClone() }()
	<-second

	close(release)
	<-first
	if got := atomic.LoadInt32(&reads); got != 2 {
		t.Fatalf("platform reads with SSL_CERT_FILE set = %d, want 2 independent reads", got)
	}
}
