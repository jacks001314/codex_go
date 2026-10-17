package appserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/session"
)

func TestWebSocketServerNegotiatesPersistentZstdStreams(t *testing.T) {
	codexHome := t.TempDir()
	routerFactory := func() *RuntimeRouter {
		return NewDefaultRuntimeRouter(session.NewStore(t.TempDir()), codexHome)
	}
	server := httptest.NewServer(NewWebSocketServer(nil, routerFactory))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			WebsocketCompressionHeader: {WebsocketZstdStreamProtocol},
		},
	})
	if err != nil {
		t.Fatalf("compressed websocket dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := response.Header.Get(WebsocketCompressionHeader); got != WebsocketZstdStreamProtocol {
		t.Fatalf("compression response header = %q, want %q", got, WebsocketZstdStreamProtocol)
	}
	clientEncoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer clientEncoder.Close()
	clientDecoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer clientDecoder.Close()

	writeCompressedWebSocketJSONForTest(t, ctx, conn, clientEncoder, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"clientInfo": map[string]string{"name": "zstd-test", "version": "1.0.0"},
		},
	})
	initialize := readCompressedWebSocketResponseForTest(t, ctx, conn, clientDecoder, "1")
	if initialize.Error != nil {
		t.Fatalf("initialize response = %+v", initialize.Error)
	}

	writeCompressedWebSocketJSONForTest(t, ctx, conn, clientEncoder, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "config/read",
		"params":  map[string]any{},
	})
	read := readCompressedWebSocketResponseForTest(t, ctx, conn, clientDecoder, "2")
	if read.Error != nil {
		t.Fatalf("config/read response = %+v", read.Error)
	}
	pingErr := make(chan error, 1)
	go func() {
		pingErr <- conn.Ping(ctx)
	}()
	writeCompressedWebSocketJSONForTest(t, ctx, conn, clientEncoder, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "config/read",
		"params":  map[string]any{},
	})
	pingResponse := readCompressedWebSocketResponseForTest(t, ctx, conn, clientDecoder, "3")
	if pingResponse.Error != nil {
		t.Fatalf("config/read response after ping = %+v", pingResponse.Error)
	}
	if err := <-pingErr; err != nil {
		t.Fatalf("compressed websocket ping error = %v", err)
	}
}

func TestWebSocketServerKeepsTextFallbackWithoutNegotiation(t *testing.T) {
	codexHome := t.TempDir()
	routerFactory := func() *RuntimeRouter {
		return NewDefaultRuntimeRouter(session.NewStore(t.TempDir()), codexHome)
	}
	server := httptest.NewServer(NewWebSocketServer(nil, routerFactory))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			WebsocketCompressionHeader: {"unsupported-stream-v9"},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if got := response.Header.Get(WebsocketCompressionHeader); got != "" {
		t.Fatalf("unsupported offer was acknowledged with %q", got)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"text-test","version":"1.0.0"}}}`,
	)); err != nil {
		t.Fatalf("write text initialize error = %v", err)
	}
	initialize := readWebSocketResponseForTest(t, ctx, conn)
	if initialize.Error != nil || initialize.ID.String() != "1" {
		t.Fatalf("text fallback initialize response = %#v", initialize)
	}
}

func TestCompressedWebSocketRejectsTextAndMalformedBinary(t *testing.T) {
	codexHome := t.TempDir()
	routerFactory := func() *RuntimeRouter {
		return NewDefaultRuntimeRouter(session.NewStore(t.TempDir()), codexHome)
	}
	server := httptest.NewServer(NewWebSocketServer(nil, routerFactory))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tests := []struct {
		name        string
		messageType websocket.MessageType
		payload     []byte
	}{
		{name: "text", messageType: websocket.MessageText, payload: []byte(`{"jsonrpc":"2.0","id":1}`)},
		{name: "malformed binary", messageType: websocket.MessageBinary, payload: []byte("not a zstd stream")},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
				HTTPHeader: map[string][]string{
					WebsocketCompressionHeader: {WebsocketZstdStreamProtocol},
				},
			})
			if err != nil {
				t.Fatalf("websocket dial error = %v", err)
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			if got := response.Header.Get(WebsocketCompressionHeader); got != WebsocketZstdStreamProtocol {
				t.Fatalf("compression response header = %q", got)
			}
			if err := conn.Write(ctx, testCase.messageType, testCase.payload); err != nil {
				t.Fatalf("write invalid compressed message error = %v", err)
			}
			if _, _, err := conn.Read(ctx); err == nil {
				t.Fatal("invalid compressed message kept the connection open")
			} else if status := websocket.CloseStatus(err); status != websocket.StatusUnsupportedData {
				t.Fatalf("close status = %v, want %v (error = %v)", status, websocket.StatusUnsupportedData, err)
			}
		})
	}
}

func writeCompressedWebSocketJSONForTest(
	t *testing.T,
	ctx context.Context,
	conn *websocket.Conn,
	encoder *ZstdEncoder,
	value any,
) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	payload, err := encoder.Encode(string(data))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write compressed websocket message error = %v", err)
	}
}

func readCompressedWebSocketResponseForTest(
	t *testing.T,
	ctx context.Context,
	conn *websocket.Conn,
	decoder *ZstdDecoder,
	wantID string,
) *Response {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read compressed websocket message error = %v", err)
		}
		if messageType != websocket.MessageBinary {
			t.Fatalf("compressed websocket message type = %v, want binary", messageType)
		}
		decoded, err := decoder.Decode(payload)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		var response Response
		if err := json.Unmarshal([]byte(decoded), &response); err != nil {
			t.Fatalf("json.Unmarshal(%q) error = %v", decoded, err)
		}
		if response.ID.String() == wantID {
			return &response
		}
	}
	t.Fatalf("compressed websocket response id %s not received", wantID)
	return nil
}
