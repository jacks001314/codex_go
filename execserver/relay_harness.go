package execserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type relayHarnessClientConnection struct {
	conn            *websocket.Conn
	streamID        string
	writeMu         sync.Mutex
	nextSeq         uint32
	keepaliveCancel context.CancelFunc
	closeOnce       sync.Once
	closeErr        error
}

func isRendezvousHarnessURL(websocketURL string) bool {
	_, query, ok := strings.Cut(websocketURL, "?")
	if !ok {
		return false
	}
	for _, pair := range strings.Split(query, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if ok && key == "role" && value == "harness" {
			return true
		}
	}
	return false
}

func newRelayHarnessClientConnection(ctx context.Context, conn *websocket.Conn) (clientConnection, error) {
	streamID := uuid.NewString()
	encoded, err := encodeRelayMessageFrame(newRelayResumeFrame(streamID))
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		return nil, err
	}
	return &relayHarnessClientConnection{conn: conn, streamID: streamID, keepaliveCancel: startClientWebSocketKeepalive(conn)}, nil
}

func (c *relayHarnessClientConnection) Write(ctx context.Context, message []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	frame := newRelayDataFrame(c.streamID, c.nextSeq, message)
	encoded, err := encodeRelayMessageFrame(frame)
	if err != nil {
		return err
	}
	c.nextSeq++
	return c.conn.Write(ctx, websocket.MessageBinary, encoded)
}

func (c *relayHarnessClientConnection) Read(ctx context.Context) ([]byte, error) {
	for {
		messageType, payload, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != websocket.MessageBinary {
			return nil, errors.New("relay exec-server transport expects binary protobuf frames")
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to parse relay message frame: %w", err)
		}
		if frame.StreamID != c.streamID {
			continue
		}
		switch frame.Kind {
		case relayFrameData:
			return frame.Data.Payload, nil
		case relayFrameReset:
			if frame.ResetReason != "" {
				return nil, errors.New(frame.ResetReason)
			}
			return nil, errors.New("exec-server relay stream reset")
		case relayFrameAck, relayFrameResume, relayFrameHeartbeat, relayFrameHandshake:
		}
	}
}

func (c *relayHarnessClientConnection) Close() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
	})
	return c.closeErr
}

func (c *relayHarnessClientConnection) CloseNow() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.CloseNow()
	})
	return c.closeErr
}

var _ clientConnection = (*relayHarnessClientConnection)(nil)
