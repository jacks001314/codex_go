package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	codextea "codex_go/tui/tea"
)

func TestRemoteAccountStatusFromAuthModeLikeRust(t *testing.T) {
	pro := auth.PlanPro
	for _, testCase := range []struct {
		name       string
		authMode   string
		plan       *auth.PlanType
		want       string
		hasChatGPT bool
	}{
		{
			name:       "chatgpt",
			authMode:   "chatgpt",
			plan:       &pro,
			want:       "Pro 200",
			hasChatGPT: true,
		},
		{
			name:       "external chatgpt tokens",
			authMode:   "chatgptAuthTokens",
			plan:       &pro,
			want:       "Pro 200",
			hasChatGPT: true,
		},
		{
			name:       "agent identity",
			authMode:   "agentIdentity",
			plan:       &pro,
			want:       "Pro 200",
			hasChatGPT: true,
		},
		{
			name:       "personal access token",
			authMode:   "personalAccessToken",
			plan:       &pro,
			want:       "Pro 200",
			hasChatGPT: true,
		},
		{
			name:     "api key",
			authMode: "apikey",
			want:     "API key configured (run codex login to use ChatGPT)",
		},
		{
			name:     "unsupported auth mode",
			authMode: "bedrockApiKey",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			status, hasChatGPT := remoteAccountStatusFromAuthMode(testCase.authMode, testCase.plan)
			if status == nil {
				if testCase.want != "" {
					t.Fatalf("status = nil, want %q", testCase.want)
				}
				return
			}
			got, ok := status.DisplayValue()
			if !ok || got != testCase.want {
				t.Fatalf("display = %q ok=%v, want %q", got, ok, testCase.want)
			}
			if hasChatGPT != testCase.hasChatGPT {
				t.Fatalf("hasChatGPT = %v, want %v", hasChatGPT, testCase.hasChatGPT)
			}
		})
	}
}

func TestAccountUpdatedRefreshesEmailLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const email = "user@example.com"
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			request, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
					websocket.CloseStatus(err) == websocket.StatusGoingAway ||
					errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			switch request.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result":  map[string]any{},
				})
			case string(appserver.MethodGetAccount):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      request.ID,
					"result": map[string]any{
						"account": map[string]any{
							"type":     string(auth.AccountChatGPT),
							"email":    email,
							"planType": string(auth.PlanPro),
						},
						"requiresOpenaiAuth": false,
					},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, errors.New("unexpected method "+request.Method))
				return
			}
		}
	}))
	defer server.Close()

	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{
		endpoint: appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil),
		messages: messages,
	}
	mode := "chatgpt"
	plan := auth.PlanPro
	params, err := json.Marshal(auth.AccountUpdatedNotification{AuthMode: &mode, PlanType: &plan})
	if err != nil {
		t.Fatalf("marshal account update: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationAccountUpdated),
		Params: params,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}

	statusMessage := readAccountStatusUpdate(t, messages)
	if statusMessage.Display != "Pro 200" || !statusMessage.HasChatGPTAccount {
		t.Fatalf("status update = %#v", statusMessage)
	}
	if statusMessage.EmailRequestID == "" {
		t.Fatal("ChatGPT account update did not start an email request")
	}

	emailMessage := readAccountEmailLoaded(t, messages)
	if emailMessage.RequestID != statusMessage.EmailRequestID || emailMessage.Email != email {
		t.Fatalf("email result = %#v, want request %q and %q", emailMessage, statusMessage.EmailRequestID, email)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func readAccountStatusUpdate(t *testing.T, messages <-chan bubbletea.Msg) codextea.AccountStatusUpdatedMsg {
	t.Helper()
	select {
	case message := <-messages:
		update, ok := message.(codextea.AccountStatusUpdatedMsg)
		if !ok {
			t.Fatalf("message = %T, want AccountStatusUpdatedMsg", message)
		}
		return update
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for account status update")
		return codextea.AccountStatusUpdatedMsg{}
	}
}

func readAccountEmailLoaded(t *testing.T, messages <-chan bubbletea.Msg) codextea.AccountEmailLoadedMsg {
	t.Helper()
	select {
	case message := <-messages:
		loaded, ok := message.(codextea.AccountEmailLoadedMsg)
		if !ok {
			t.Fatalf("message = %T, want AccountEmailLoadedMsg", message)
		}
		return loaded
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for account email")
		return codextea.AccountEmailLoadedMsg{}
	}
}
