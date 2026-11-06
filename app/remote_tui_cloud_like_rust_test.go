package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cloudbackend"
)

type fakeCloudRequester struct {
	notifications chan cloudbackend.Notification
	answers       []string
	closed        bool
}

func (f *fakeCloudRequester) Request(_ context.Context, method appserver.Method, _ json.RawMessage) (json.RawMessage, *cloudbackend.RPCError) {
	if method == appserver.Method("initialize") {
		return json.RawMessage("{\"userAgent\":\"codex-cloud\"}"), nil
	}
	return nil, &cloudbackend.RPCError{Code: -32601, Message: "Method not found"}
}

func (f *fakeCloudRequester) Notifications() <-chan cloudbackend.Notification {
	return f.notifications
}

func (f *fakeCloudRequester) AnswerServerRequest(requestID string) error {
	f.answers = append(f.answers, requestID)
	return nil
}

func (f *fakeCloudRequester) Close() { f.closed = true }

type cloudResponseEnvelope struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

type cloudNotificationEnvelope struct {
	Method string `json:"method"`
	Params struct {
		ThreadID string `json:"threadId"`
	} `json:"params"`
}

// Rust #59828: the TUI's byte transport answers app-server requests with the
// Cloud backend client and republishes its notifications.
func TestCloudAppServerTransportBridgesRequestsLikeRust(t *testing.T) {
	requester := &fakeCloudRequester{notifications: make(chan cloudbackend.Notification, 4)}
	transport := newCloudAppServerTransport(context.Background(), requester)
	defer transport.close()

	if err := transport.write(context.Background(), []byte("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"initialize\",\"params\":{}}")); err != nil {
		t.Fatalf("write() error = %v", err)
	}
	response := readCloudTransportMessage(t, transport)
	var decoded cloudResponseEnvelope
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode response: %v (%s)", err, response)
	}
	if decoded.ID != 7 || decoded.Error != nil || len(decoded.Result) == 0 {
		t.Fatalf("response = %s", response)
	}

	if err := transport.write(context.Background(), []byte("{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"thread/read\",\"params\":{}}")); err != nil {
		t.Fatalf("write() error = %v", err)
	}
	response = readCloudTransportMessage(t, transport)
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode response: %v (%s)", err, response)
	}
	if decoded.ID != 8 || decoded.Error == nil || decoded.Error.Code != -32601 {
		t.Fatalf("unsupported method response = %s", response)
	}

	requester.notifications <- cloudbackend.Notification{
		Method: appserver.NotificationMethod("thread/started"),
		Params: map[string]any{"threadId": "thread-1"},
	}
	notification := readCloudTransportMessage(t, transport)
	var decodedNotification cloudNotificationEnvelope
	if err := json.Unmarshal(notification, &decodedNotification); err != nil {
		t.Fatalf("decode notification: %v (%s)", err, notification)
	}
	if decodedNotification.Method != "thread/started" || decodedNotification.Params.ThreadID != "thread-1" {
		t.Fatalf("notification = %s", notification)
	}

	if err := transport.write(context.Background(), []byte("{\"jsonrpc\":\"2.0\",\"id\":9,\"result\":{\"decision\":\"accept\"}}")); err != nil {
		t.Fatalf("write() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(requester.answers) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(requester.answers) != 1 || requester.answers[0] != "9" {
		t.Fatalf("answered server requests = %#v", requester.answers)
	}
}

// Rust #59828: Cloud is an explicit remote selection and stays behind the
// tui_multi_backend feature.
func TestResolveInteractiveRemoteAddressSelectsCloudLikeRust(t *testing.T) {
	for _, address := range []string{"cloud", "cloud://", "CLOUD"} {
		endpoint, err := resolveInteractiveRemoteAddress(address)
		if err != nil || endpoint == nil || endpoint.Kind != appserverdaemon.RemoteEndpointCloud {
			t.Fatalf("resolveInteractiveRemoteAddress(%q) = %#v, %v", address, endpoint, err)
		}
	}
}

func readCloudTransportMessage(t *testing.T, transport *cloudAppServerTransport) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	message, err := transport.read(ctx)
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	return message
}
