package network

import (
	"net"
	"sync"
)

// Active-connection tracking for the managed network proxy (Rust #43884):
// hijacked HTTP CONNECT tunnels and SOCKS5 connections are not owned by the
// HTTP server, so teardown must close them explicitly instead of leaving
// accepted or half-closed tunnels alive after their thread is unloaded.
//
// The tracking state lives on ProxyServer so it can be attached to the
// listener wrappers without changing the accepted-connection types.

type proxyConnTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (t *proxyConnTracker) track(conn net.Conn) func() {
	if t == nil || conn == nil {
		return func() {}
	}
	t.mu.Lock()
	if t.conns == nil {
		t.conns = map[net.Conn]struct{}{}
	}
	t.conns[conn] = struct{}{}
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		delete(t.conns, conn)
		t.mu.Unlock()
	}
}

func (t *proxyConnTracker) closeAll() {
	if t == nil {
		return
	}
	t.mu.Lock()
	conns := make([]net.Conn, 0, len(t.conns))
	for conn := range t.conns {
		conns = append(conns, conn)
	}
	t.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// proxyTrackingListener wraps a listener so every accepted connection is
// tracked for the lifetime of the proxy.
type proxyTrackingListener struct {
	net.Listener
	tracker *proxyConnTracker
}

func (l proxyTrackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	untrack := l.tracker.track(conn)
	return &proxyTrackedConn{Conn: conn, untrack: untrack}, nil
}

// proxyTrackedConn unregisters its connection when it closes.
type proxyTrackedConn struct {
	net.Conn
	untrack func()
	once    sync.Once
}

func (c *proxyTrackedConn) Close() error {
	c.once.Do(func() {
		if c.untrack != nil {
			c.untrack()
		}
	})
	return c.Conn.Close()
}

// CloseWrite and CloseRead preserve TCP half-close behavior for the SOCKS5
// relay, which relies on shutting down one direction without closing the
// tracked connection outright.
func (c *proxyTrackedConn) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}

func (c *proxyTrackedConn) CloseRead() error {
	if closer, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return closer.CloseRead()
	}
	return nil
}

// unwrapProxyTracked returns the concrete connection and the release function
// for a tracked connection, or the input and nil when it is not tracked.
// Callers that hand the connection to a library which type-asserts concrete
// TCP connections (for example TCP half-close) must use the unwrapped conn and
// release the tracking when the borrowed operation finishes.
func unwrapProxyTracked(conn net.Conn) (net.Conn, func()) {
	if tracked, ok := conn.(*proxyTrackedConn); ok {
		release := tracked.untrack
		if release == nil {
			release = func() {}
		}
		return tracked.Conn, release
	}
	return conn, nil
}
