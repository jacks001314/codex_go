package codemode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

const WebSocketCloseTimeout = 5 * time.Second

type WebSocketTransport struct {
	conn *websocket.Conn
}

func DialWebSocket(ctx context.Context, url string, httpClient *http.Client) (*WebSocketTransport, *http.Response, error) {
	options := &websocket.DialOptions{HTTPClient: httpClient}
	conn, response, err := websocket.Dial(ctx, url, options)
	if err != nil {
		return nil, response, err
	}
	conn.SetReadLimit(ProtocolMaxFrameBytes + 4)
	return &WebSocketTransport{conn: conn}, response, nil
}

func NewWebSocketTransport(conn *websocket.Conn) *WebSocketTransport {
	if conn != nil {
		conn.SetReadLimit(ProtocolMaxFrameBytes + 4)
	}
	return &WebSocketTransport{conn: conn}
}

func (t *WebSocketTransport) Read(ctx context.Context, target any) (bool, error) {
	if t == nil || t.conn == nil {
		return false, fmt.Errorf("code-mode websocket transport is nil")
	}
	messageType, payload, err := t.conn.Read(ctx)
	if err != nil {
		status := websocket.CloseStatus(err)
		if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read code-mode host websocket message: %w", err)
	}
	if messageType != websocket.MessageBinary {
		return false, fmt.Errorf("code-mode host websocket messages must be binary framed messages")
	}
	return NewFramedReader(bytes.NewReader(payload)).Read(target)
}

func (t *WebSocketTransport) Write(ctx context.Context, message any) error {
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	return t.WriteFrame(ctx, frame)
}

func (t *WebSocketTransport) WriteFrame(ctx context.Context, frame EncodedFrame) error {
	if t == nil || t.conn == nil {
		return fmt.Errorf("code-mode websocket transport is nil")
	}
	payload, err := (&frame).Bytes()
	if err != nil {
		return err
	}
	if err := t.conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		return fmt.Errorf("failed to write code-mode host websocket message: %w", err)
	}
	return nil
}

func (t *WebSocketTransport) Close() error {
	if t == nil || t.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), WebSocketCloseTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- t.conn.Close(websocket.StatusNormalClosure, "") }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timed out closing code-mode host websocket connection")
	}
}
