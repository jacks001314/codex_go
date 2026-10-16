package network

import (
	"crypto/x509"
	"os"
	"sync"
)

// PlatformRootPoolClone hands the caller its own copy of the platform trust
// anchors. It mirrors Rust #54056 (`http-client/src/native_roots.rs` `load()`,
// consumed as `(*load()).clone()` in `custom_ca.rs`): the platform store is
// read into a fresh pool, so a custom CA bundle layered on top of it by one
// client cannot leak into another client and cannot mutate the process-wide
// pool the standard library memoizes.
//
// Coalescing follows the same module: callers that overlap share one in-flight
// read (`NativeRootsLoader`), a completed read is never retained, so a later
// caller re-reads the platform store. `SSL_CERT_FILE` / `SSL_CERT_DIR` are
// treated as platform-store overrides and bypass coalescing, exactly like the
// Rust loader, so their contents are re-read for every build. Custom Codex CA
// bundles stay the caller's business.
func PlatformRootPoolClone() *x509.CertPool {
	if os.Getenv("SSL_CERT_FILE") != "" || os.Getenv("SSL_CERT_DIR") != "" {
		return clonePlatformRootPool(loadPlatformRootPool())
	}
	return clonePlatformRootPool(platformRootsLoaderSingleton.load(loadPlatformRootPool))
}

var platformRootsLoaderSingleton platformRootsLoader

// loadPlatformRootPool reads the platform trust anchors. It is a package
// variable so tests can count and hold platform reads open.
var loadPlatformRootPool = func() *x509.CertPool {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		return x509.NewCertPool()
	}
	return pool
}

func clonePlatformRootPool(pool *x509.CertPool) *x509.CertPool {
	if pool == nil {
		return x509.NewCertPool()
	}
	if cloned := pool.Clone(); cloned != nil {
		return cloned
	}
	return x509.NewCertPool()
}

// platformRootsLoader mirrors Rust's `NativeRootsLoader`: the in-flight cell is
// shared while a read is pending and dropped once it completes.
type platformRootsLoader struct {
	mu       sync.Mutex
	inFlight *platformRootsCell
}

func (l *platformRootsLoader) load(load func() *x509.CertPool) *x509.CertPool {
	l.mu.Lock()
	cell := l.inFlight
	if cell == nil || cell.complete() {
		cell = &platformRootsCell{done: make(chan struct{})}
		l.inFlight = cell
	}
	l.mu.Unlock()

	pool := cell.load(load)

	// Retain no completed snapshot: the next caller starts a fresh read.
	l.mu.Lock()
	if l.inFlight == cell && cell.complete() {
		l.inFlight = nil
	}
	l.mu.Unlock()
	return pool
}

type platformRootsCell struct {
	once sync.Once
	done chan struct{}
	pool *x509.CertPool
}

// complete reports whether the shared read already finished. The check must not
// block on a read that is still in flight, otherwise a caller arriving after
// the read started would mistake the cell for a completed one and read the
// platform store again.
func (c *platformRootsCell) complete() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// load performs the shared read once. Overlapping callers block until the
// first one finished and then observe the same pool.
func (c *platformRootsCell) load(load func() *x509.CertPool) *x509.CertPool {
	c.once.Do(func() {
		c.pool = load()
		close(c.done)
	})
	<-c.done
	return c.pool
}
