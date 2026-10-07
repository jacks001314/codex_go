package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// hangingEnvironmentInfoConnection accepts requests but never answers them, so
// a probe sent over it can only finish through its own deadline.
type hangingEnvironmentInfoConnection struct {
	writes    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newHangingEnvironmentInfoConnection() *hangingEnvironmentInfoConnection {
	return &hangingEnvironmentInfoConnection{
		writes: make(chan []byte, 4),
		closed: make(chan struct{}),
	}
}

func (c *hangingEnvironmentInfoConnection) Read(ctx context.Context) ([]byte, error) {
	<-c.closed
	return nil, errors.New("connection closed")
}

func (c *hangingEnvironmentInfoConnection) Write(ctx context.Context, data []byte) error {
	select {
	case c.writes <- append([]byte(nil), data...):
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *hangingEnvironmentInfoConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *hangingEnvironmentInfoConnection) CloseNow() error { return c.Close() }

// Rust #49407: an unanswered environment/info probe must time out, retire only
// the connection it probed, and resume the same session while retaining the
// client instance (codex-rs/exec-server/src/client.rs,
// remote_websocket_client_resumes_session/health_check_timed_out).
func TestEnvironmentInfoTimeoutRetiresConnectionLikeRust(t *testing.T) {
	previous := environmentInfoTimeout
	environmentInfoTimeout = 40 * time.Millisecond
	defer func() { environmentInfoTimeout = previous }()

	conn := newHangingEnvironmentInfoConnection()
	client := newNetworkPolicyTestClient(conn)
	client.sessionID = "session-1"
	recovered := make(chan clientConnection, 1)
	client.open = func(ctx context.Context, sessionID string, _ func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, error) {
		replacement := newHangingEnvironmentInfoConnection()
		recovered <- replacement
		return replacement, &InitializeResponse{SessionID: sessionID}, nil
	}

	startedAt := time.Now()
	_, err := client.EnvironmentInfo(context.Background())
	elapsed := time.Since(startedAt)
	if err == nil {
		t.Fatal("unanswered environment/info probe did not time out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("EnvironmentInfo() error = %v, want a timeout error", err)
	}
	if elapsed < environmentInfoTimeout {
		t.Fatalf("EnvironmentInfo() returned after %v, before the %v probe deadline", elapsed, environmentInfoTimeout)
	}

	client.mu.Lock()
	pending := len(client.pending)
	client.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending calls after the timeout = %d, want 0", pending)
	}

	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the probed connection was not closed after the timeout")
	}

	select {
	case replacement := <-recovered:
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			client.mu.Lock()
			current := client.conn
			client.mu.Unlock()
			if current == replacement {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("the client never adopted the recovered connection")
	case <-time.After(2 * time.Second):
		t.Fatal("session recovery was not requested after the timeout")
	}
}

// The probe deadline is environment/info-specific: a caller deadline that
// expires first is reported as-is and must not retire the connection.
func TestEnvironmentInfoKeepsCallerCancellationLikeRust(t *testing.T) {
	previous := environmentInfoTimeout
	environmentInfoTimeout = 5 * time.Second
	defer func() { environmentInfoTimeout = previous }()

	conn := newHangingEnvironmentInfoConnection()
	client := newNetworkPolicyTestClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := client.EnvironmentInfo(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnvironmentInfo() error = %v, want context.Canceled", err)
	}
	select {
	case <-conn.closed:
		t.Fatal("caller cancellation must not retire the connection")
	default:
	}

	client.mu.Lock()
	current := client.conn
	client.mu.Unlock()
	if current != clientConnection(conn) {
		t.Fatal("caller cancellation must keep the current connection")
	}
}
