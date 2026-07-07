package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/internal/remotecontrol"
)

func TestRemoteControlTransportRoutesJSONRPC(t *testing.T) {
	manager := remotecontrol.NewManager("codex", "installation-id")
	manager.Enable(&remotecontrol.EnableParams{Ephemeral: true})
	router := NewRuntimeRouter(RuntimeServices{Remote: manager})
	events := make(chan remotecontrol.RemoteClientTransportEvent, 8)
	writer := make(chan remotecontrol.RemoteClientOutgoingMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewRemoteControlTransportServer(router, events).Serve(ctx)
	}()

	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientConnectionOpened,
		ConnectionID: 1,
		Writer:       writer,
	}
	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientIncomingMessage,
		ConnectionID: 1,
		Message:      []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"remote","version":"1"},"capabilities":{"experimentalApi":true}}}`),
	}

	initialize := readRemoteControlTransportMessage(t, writer)
	if string(initialize["id"]) != "1" || !strings.Contains(string(initialize["result"]), `"codexHome"`) {
		t.Fatalf("initialize response = %s", marshalRawMapForTest(initialize))
	}
	statusNotification := readRemoteControlTransportMessage(t, writer)
	if method := strings.Trim(string(statusNotification["method"]), `"`); method != string(NotificationRemoteControlStatusChanged) {
		t.Fatalf("status notification method = %q", method)
	}

	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientIncomingMessage,
		ConnectionID: 1,
		Message:      []byte(`{"jsonrpc":"2.0","id":2,"method":"remoteControl/status/read","params":{}}`),
	}
	status := readRemoteControlTransportMessage(t, writer)
	if string(status["id"]) != "2" || !strings.Contains(string(status["result"]), `"status":"connected"`) {
		t.Fatalf("status response = %s", marshalRawMapForTest(status))
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("remote control transport returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control transport did not stop")
	}
}

func TestRemoteControlTransportTargetsServerRequests(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Remote: remotecontrol.NewManager("codex", "installation-id")})
	events := make(chan remotecontrol.RemoteClientTransportEvent, 8)
	writer := make(chan remotecontrol.RemoteClientOutgoingMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewRemoteControlTransportServer(router, events).Serve(ctx)
	}()

	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientConnectionOpened,
		ConnectionID: 7,
		Writer:       writer,
	}
	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientIncomingMessage,
		ConnectionID: 7,
		Message:      []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"remote","version":"1"},"capabilities":{"experimentalApi":true}}}`),
	}
	_ = readRemoteControlTransportMessage(t, writer)
	_ = readRemoteControlTransportMessage(t, writer)

	requestDone := make(chan error, 1)
	go func() {
		requestDone <- router.requireServerRequests().RequestToConnection(
			context.Background(),
			remoteControlConnectionIDString(7),
			ServerRequestCurrentTimeRead,
			map[string]any{},
			nil,
		)
	}()
	serverRequest := readRemoteControlTransportMessage(t, writer)
	requestID := strings.Trim(string(serverRequest["id"]), `"`)
	if requestID == "" || strings.Trim(string(serverRequest["method"]), `"`) != string(ServerRequestCurrentTimeRead) {
		t.Fatalf("server request = %s", marshalRawMapForTest(serverRequest))
	}
	events <- remotecontrol.RemoteClientTransportEvent{
		Type:         remotecontrol.RemoteClientIncomingMessage,
		ConnectionID: 7,
		Message:      []byte(`{"jsonrpc":"2.0","id":"` + requestID + `","result":{}}`),
	}

	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("server request returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server request did not resolve")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("remote control transport returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote control transport did not stop")
	}
}

func readRemoteControlTransportMessage(t *testing.T, writer <-chan remotecontrol.RemoteClientOutgoingMessage) map[string]json.RawMessage {
	t.Helper()
	select {
	case outgoing := <-writer:
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(outgoing.Message, &payload); err != nil {
			t.Fatalf("decode remote control outgoing %s: %v", outgoing.Message, err)
		}
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote control outgoing message")
		return nil
	}
}

func marshalRawMapForTest(value map[string]json.RawMessage) string {
	data, _ := json.Marshal(value)
	return string(data)
}
