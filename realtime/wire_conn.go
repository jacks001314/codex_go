package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// realtimeWireConn is the message transport behind one realtime session. A
// websocket session wraps the dialed connection; a caller-owned sideband reads
// and writes JSON strings over channels instead (Rust #58953).
type realtimeWireConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, messageType websocket.MessageType, payload []byte) error
	Close(code websocket.StatusCode, reason string) error
	CloseNow()
}

type websocketWireConn struct {
	conn *websocket.Conn
}

func (c *websocketWireConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	return c.conn.Read(ctx)
}

func (c *websocketWireConn) Write(ctx context.Context, messageType websocket.MessageType, payload []byte) error {
	return c.conn.Write(ctx, messageType, payload)
}

func (c *websocketWireConn) Close(code websocket.StatusCode, reason string) error {
	return c.conn.Close(code, reason)
}

func (c *websocketWireConn) CloseNow() {
	c.conn.CloseNow()
}

// RealtimeSidebandMaxMessageBytes bounds one caller-owned sideband message,
// mirroring Rust's MAX_MESSAGE_BYTES.
const RealtimeSidebandMaxMessageBytes = 1 << 20

var (
	// ErrRealtimeSidebandInputClosed reports that the caller closed its input
	// half. The attachment ends normally after accepted input drains.
	ErrRealtimeSidebandInputClosed = errors.New("realtime sideband input closed")
	// ErrRealtimeSidebandMessageTooLarge reports one message above the bound;
	// both directions close before the payload reaches a parser or the caller.
	ErrRealtimeSidebandMessageTooLarge = errors.New("realtime sideband message exceeds 1 MiB")
	errRealtimeSidebandDetached        = errors.New("realtime sideband detached")
	errRealtimeSidebandWriteClosed     = errors.New("realtime sideband output closed")
)

// sidebandStreamConn adapts caller-owned channels to one realtime transport.
// Reads come straight from the caller's queue, while writes drain through a
// separate pump so a stalled outgoing half cannot block inbound events. Closing
// the connection drops queued writes instead of replaying them.
type sidebandStreamConn struct {
	incoming  <-chan string
	outgoing  chan<- string
	writes    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
	mu        sync.Mutex
	terminal  error
}

func newSidebandStreamConn(incoming <-chan string, outgoing chan<- string) *sidebandStreamConn {
	conn := &sidebandStreamConn{
		incoming: incoming,
		outgoing: outgoing,
		writes:   make(chan []byte, 32),
		done:     make(chan struct{}),
	}
	go conn.pump()
	return conn
}

func (c *sidebandStreamConn) pump() {
	// A caller that closes its outgoing channel ends the output half instead of
	// panicking the host (Rust's sender drop has the same effect).
	defer func() {
		if recover() != nil {
			c.setTerminal(errRealtimeSidebandWriteClosed)
			c.stop()
		}
	}()
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.writes:
			select {
			case c.outgoing <- string(payload):
			case <-c.done:
				return
			}
		}
	}
}

func (c *sidebandStreamConn) setTerminal(err error) {
	c.mu.Lock()
	if c.terminal == nil {
		c.terminal = err
	}
	c.mu.Unlock()
}

func (c *sidebandStreamConn) stop() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
	})
}

func (c *sidebandStreamConn) fail(err error) error {
	c.mu.Lock()
	if c.terminal == nil {
		c.terminal = err
	}
	c.mu.Unlock()
	c.stop()
	return err
}

func (c *sidebandStreamConn) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil {
		return c.terminal
	}
	return errRealtimeSidebandWriteClosed
}

func (c *sidebandStreamConn) accept(message string, ok bool) (websocket.MessageType, []byte, error) {
	if !ok {
		return 0, nil, c.fail(ErrRealtimeSidebandInputClosed)
	}
	if len(message) > RealtimeSidebandMaxMessageBytes {
		return 0, nil, c.fail(ErrRealtimeSidebandMessageTooLarge)
	}
	return websocket.MessageText, []byte(message), nil
}

func (c *sidebandStreamConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	// Input the caller already queued stays deliverable after a detach or close
	// so a final transcript reaches the parser and tail flush (Rust #58953).
	select {
	case message, ok := <-c.incoming:
		return c.accept(message, ok)
	default:
	}
	select {
	case message, ok := <-c.incoming:
		return c.accept(message, ok)
	case <-ctx.Done():
		select {
		case message, ok := <-c.incoming:
			return c.accept(message, ok)
		default:
			return 0, nil, errRealtimeSidebandDetached
		}
	case <-c.done:
		select {
		case message, ok := <-c.incoming:
			return c.accept(message, ok)
		default:
			return 0, nil, c.terminalError()
		}
	}
}

func (c *sidebandStreamConn) Write(ctx context.Context, _ websocket.MessageType, payload []byte) error {
	if c == nil {
		return errRealtimeSidebandWriteClosed
	}
	if len(payload) > RealtimeSidebandMaxMessageBytes {
		return c.fail(ErrRealtimeSidebandMessageTooLarge)
	}
	if c.closed.Load() {
		return c.terminalError()
	}
	copied := append([]byte(nil), payload...)
	select {
	case c.writes <- copied:
		return nil
	case <-c.done:
		return c.terminalError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *sidebandStreamConn) Close(websocket.StatusCode, string) error {
	c.stop()
	return nil
}

func (c *sidebandStreamConn) CloseNow() {
	c.stop()
}
