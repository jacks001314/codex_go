package execserver

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/coder/websocket"
)

// serveForwardedStream bridges one authenticated Noise relay stream to the
// existing exec-server WebSocket named by SetForwardDestination. Rust's
// forwarder (codex-rs/exec-server/src/forward.rs) does the same thing per
// stream: it dials the destination once, sends every relay payload as one
// WebSocket message and frames every WebSocket message back to the relay
// unchanged.
func (s *Server) serveForwardedStream(ctx context.Context, conn net.Conn) error {
	if conn == nil {
		return errors.New("exec-server forward stream is missing")
	}
	destination := s.forwardDestinationURL()
	if destination == "" {
		return errors.New("exec-server forward destination is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dialCtx, cancel := context.WithTimeout(ctx, defaultRemoteDialTimeout)
	websocketConn, _, err := websocket.Dial(dialCtx, destination, &websocket.DialOptions{HTTPClient: s.httpClient})
	cancel()
	if err != nil {
		slog.Warn("failed to connect to forwarded exec-server", "error", err)
		return err
	}
	defer websocketConn.CloseNow()
	websocketConn.SetReadLimit(int64(maxNoiseJSONRPCMessageLen))

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	defer conn.Close()

	toDestination := make(chan struct{})
	go func() {
		defer close(toDestination)
		reader := bufio.NewReader(conn)
		for {
			payload, err := readRemoteRelayJSONLine(reader)
			if err != nil {
				cancelStream()
				return
			}
			if err := websocketConn.Write(streamCtx, websocket.MessageBinary, payload); err != nil {
				cancelStream()
				return
			}
		}
	}()

	fromDestination := make(chan struct{})
	go func() {
		defer close(fromDestination)
		for {
			messageType, payload, err := websocketConn.Read(streamCtx)
			if err != nil {
				cancelStream()
				return
			}
			switch messageType {
			case websocket.MessageText, websocket.MessageBinary:
			default:
				continue
			}
			if _, err := conn.Write(append(payload, '\n')); err != nil {
				cancelStream()
				return
			}
		}
	}()

	<-streamCtx.Done()
	return nil
}
