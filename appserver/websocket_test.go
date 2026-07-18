package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/remotecontrol"
	"codex_go/session"
)

func TestWebSocketListenAddress(t *testing.T) {
	address, err := WebSocketListenAddress("")
	if err != nil {
		t.Fatalf("WebSocketListenAddress empty error = %v", err)
	}
	if address != "127.0.0.1:0" {
		t.Fatalf("empty listen address = %q", address)
	}
	address, err = WebSocketListenAddress("ws://127.0.0.1:7777")
	if err != nil {
		t.Fatalf("WebSocketListenAddress explicit error = %v", err)
	}
	if address != "127.0.0.1:7777" {
		t.Fatalf("explicit listen address = %q", address)
	}
	if _, err := WebSocketListenAddress("http://127.0.0.1:7777"); err == nil {
		t.Fatal("non-websocket scheme should fail")
	}
	if _, err := WebSocketListenAddress("ws://127.0.0.1:7777/rpc"); err == nil {
		t.Fatal("websocket listen path should fail")
	}
}

func TestWebSocketServerHealthAndInitialize(t *testing.T) {
	codexHome := t.TempDir()
	routerFactory := func() *RuntimeRouter {
		return NewDefaultRuntimeRouter(session.NewStore(t.TempDir()), codexHome)
	}
	server := httptest.NewServer(NewWebSocketServer(nil, routerFactory))
	defer server.Close()

	health, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d", health.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"method":"config/read","params":{}}`)); err != nil {
		t.Fatalf("write config/read error = %v", err)
	}
	response := readWebSocketResponseForTest(t, ctx, conn)
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "Not initialized" {
		t.Fatalf("pre-initialize response = %#v", response)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"clientInfo":{"name":"ws-test","version":"1.0.0"}}}`)); err != nil {
		t.Fatalf("write initialize error = %v", err)
	}
	response = readWebSocketResponseForTest(t, ctx, conn)
	if response.Error != nil || response.ID.String() != "2" {
		t.Fatalf("initialize response = %#v", response)
	}
	status := readWebSocketRemoteControlStatusForTest(t, ctx, conn)
	if status.Status != remotecontrol.StatusDisabled || status.EnvironmentID != nil {
		t.Fatalf("initialize remote-control status = %+v", status)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":3,"method":"config/read","params":{}}`)); err != nil {
		t.Fatalf("write config/read after initialize error = %v", err)
	}
	response = readWebSocketResponseForTest(t, ctx, conn)
	if response.Error != nil || response.ID.String() != "3" {
		t.Fatalf("post-initialize response = %#v", response)
	}
}

func TestWebSocketRouterFactoryPassesRuntimeOptions(t *testing.T) {
	router := NewWebSocketRouterFactoryWithOptions(t.TempDir(), t.TempDir(), &RuntimeRouterOptions{
		RemoteControlStartupMode: RemoteControlStartupEnabledEphemeral,
	})()

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "ws-test", Version: "1.0.0"},
		Capabilities: &InitializeCapabilities{
			ExperimentalAPI: true,
		},
	})
	initialize.ConnectionID = "ws-1"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}

	status := requestWithParams(t, IntID(2), MethodRemoteControlStatusRead, map[string]any{})
	status.ConnectionID = "ws-1"
	response := router.Handle(status)
	if response.Error != nil {
		t.Fatalf("status/read error: %+v", response.Error)
	}
	got, ok := response.Result.(*remotecontrol.StatusReadResponse)
	if !ok || got.Status != remotecontrol.StatusConnected {
		t.Fatalf("status/read result = %#v, want connected", response.Result)
	}
}

func TestWebSocketServerRejectsBrowserOrigin(t *testing.T) {
	server := httptest.NewServer(NewWebSocketServer(nil, func() *RuntimeRouter {
		return NewDefaultRuntimeRouter(session.NewStore(t.TempDir()), t.TempDir())
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("websocket dial with foreign origin should fail")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin status = %#v, err = %v", response, err)
	}
}

func readWebSocketResponseForTest(t *testing.T, ctx context.Context, conn *websocket.Conn) *Response {
	t.Helper()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket response error = %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("websocket message type = %v", messageType)
	}
	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode websocket response error = %v", err)
	}
	return &response
}

func readWebSocketRemoteControlStatusForTest(t *testing.T, ctx context.Context, conn *websocket.Conn) *RemoteControlStatusChangedNotification {
	t.Helper()
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket notification error = %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("websocket message type = %v", messageType)
	}
	var notification struct {
		Method NotificationMethod                     `json:"method"`
		Params RemoteControlStatusChangedNotification `json:"params"`
	}
	if err := json.Unmarshal(data, &notification); err != nil {
		t.Fatalf("decode websocket notification error = %v", err)
	}
	if notification.Method != NotificationRemoteControlStatusChanged {
		t.Fatalf("websocket notification method = %s, want %s", notification.Method, NotificationRemoteControlStatusChanged)
	}
	return &notification.Params
}
