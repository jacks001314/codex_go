package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// signalingConn reports when a transport starts blocking in Read or Write so a
// test can stop the client only after the IO is in flight.
type signalingConn struct {
	net.Conn
	once    sync.Once
	entered chan struct{}
}

func (c *signalingConn) signal() {
	c.once.Do(func() { close(c.entered) })
}

func (c *signalingConn) Read(data []byte) (int, error) {
	c.signal()
	return c.Conn.Read(data)
}

func (c *signalingConn) Write(data []byte) (int, error) {
	c.signal()
	return c.Conn.Write(data)
}

func newSignalingTransport(t *testing.T) (*remoteJSONLineTransport, *signalingConn) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	conn := &signalingConn{Conn: client, entered: make(chan struct{})}
	return newRemoteJSONLineTransport(conn), conn
}

// Rust #63351: dropping the client aborts a transport write stalled on the
// socket so the calling work is released instead of hanging.
func TestRemoteJSONLineTransportCloseReleasesStalledWriteLikeRust(t *testing.T) {
	transport, conn := newSignalingTransport(t)
	result := make(chan error, 1)
	go func() { result <- transport.write(context.Background(), []byte(`{"id":1}`)) }()
	<-conn.entered
	transport.close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stalled write reported success after close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled write stayed blocked after the client was closed")
	}
	if err := transport.write(context.Background(), []byte(`{"id":2}`)); err == nil {
		t.Fatal("write after close reported success")
	}
}

// Rust #63351: cancelling the request aborts a stalled write and reports the
// cancellation rather than a socket timeout.
func TestRemoteJSONLineTransportCancellationReleasesStalledWriteLikeRust(t *testing.T) {
	transport, conn := newSignalingTransport(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- transport.write(ctx, []byte(`{"id":1}`)) }()
	<-conn.entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stalled write error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled write stayed blocked after cancellation")
	}
}

// Rust #63351: the same abort releases a stalled read so a stopped client never
// leaves its reader waiting on the socket.
func TestRemoteJSONLineTransportCancellationReleasesStalledReadLikeRust(t *testing.T) {
	transport, conn := newSignalingTransport(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := transport.read(ctx)
		result <- err
	}()
	<-conn.entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stalled read error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled read stayed blocked after cancellation")
	}
}
