package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
)

type deviceSupportVerificationProvider struct {
	UnsupportedUserVerificationProvider
	supported bool
}

func (p deviceSupportVerificationProvider) DeviceSupported() bool {
	return p.supported
}

type userVerificationTargetSink struct {
	broker      *ServerRequestBroker
	targeted    chan string
	broadcasted chan struct{}
}

func (s *userVerificationTargetSink) SendServerRequest(request *ServerRequest) {
	if s.broadcasted != nil {
		s.broadcasted <- struct{}{}
	}
}

func (s *userVerificationTargetSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	if s.targeted != nil {
		s.targeted <- connectionID
	}
	if s.broker != nil {
		_, _ = s.broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{
			Action:  MCPElicitationActionAccept,
			Content: map[string]any{"credentialId": "credential-1", "signature": "c2ln"},
		}))
	}
}

// Mirrors Rust #44613: only an opted-in in-process codex-tui or stdio Codex
// Desktop connection with a supported device receives the verification mode.
// Verification requests are targeted at that connection and never broadcast.
func TestUserVerificationDesktopActivationLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		transport    string
		clientName   string
		experimental bool
		supported    bool
		want         bool
	}{
		{name: "in-process tui", transport: "in-process", clientName: "codex-tui", experimental: true, supported: true, want: true},
		{name: "stdio desktop", transport: "stdio", clientName: "Codex Desktop", experimental: true, supported: true, want: true},
		{name: "websocket tui", transport: "websocket", clientName: "codex-tui", experimental: true, supported: true},
		{name: "remote control tui", transport: "remote_control", clientName: "codex-tui", experimental: true, supported: true},
		{name: "stdio tui", transport: "stdio", clientName: "codex-tui", experimental: true, supported: true},
		{name: "in-process desktop", transport: "in-process", clientName: "Codex Desktop", experimental: true, supported: true},
		{name: "websocket desktop", transport: "websocket", clientName: "Codex Desktop", experimental: true, supported: true},
		{name: "remote control desktop", transport: "remote_control", clientName: "Codex Desktop", experimental: true, supported: true},
		{name: "stdio other client", transport: "stdio", clientName: "other-ui", experimental: true, supported: true},
		{name: "stdio case mismatch", transport: "stdio", clientName: "codex_desktop", experimental: true, supported: true},
		{name: "desktop without experimental", transport: "stdio", clientName: "Codex Desktop", supported: true},
		{name: "desktop unsupported device", transport: "stdio", clientName: "Codex Desktop", experimental: true},
		{name: "tui unsupported device", transport: "in-process", clientName: "codex-tui", experimental: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := NewRuntimeRouter(RuntimeServices{
				UserVerificationProvider: deviceSupportVerificationProvider{supported: tc.supported},
			})
			router.SetRequestTransport(tc.transport)
			connectionID := tc.transport + "-connection"
			initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
				ClientInfo: ClientInfo{Name: tc.clientName, Version: "1.0.0"},
				Capabilities: &InitializeCapabilities{
					ExperimentalAPI: tc.experimental,
				},
			})
			initialize.ConnectionID = connectionID
			if response := router.Handle(initialize); response.Error != nil {
				t.Fatalf("initialize error = %+v", response.Error)
			}
			if got := router.anyConnectionUserVerification(); got != tc.want {
				t.Fatalf("user verification enabled = %v, want %v", got, tc.want)
			}

			broker := router.requireServerRequests()
			sink := &userVerificationTargetSink{
				broker:      broker,
				targeted:    make(chan string, 1),
				broadcasted: make(chan struct{}, 1),
			}
			router.SetServerRequestSink(sink)
			handlerValue := router.requireMCP().ElicitationHandler()
			handler, ok := handlerValue.(*appserverMCPElicitationHandler)
			if !ok {
				t.Fatalf("elicitation handler = %T", handlerValue)
			}
			response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
				ServerName:  "codex_apps",
				ThreadID:    "thread-1",
				Method:      mcp.UserVerificationMode,
				Title:       "Approve",
				Description: "Sign the challenge",
				Challenge:   "AQ",
			})
			if err != nil {
				t.Fatalf("HandleMCPElicitation() error = %v", err)
			}
			if tc.want {
				if response.Action != mcp.MCPElicitationActionAccept {
					t.Fatalf("response = %#v, want accept", response)
				}
				select {
				case got := <-sink.targeted:
					if got != connectionID {
						t.Fatalf("targeted connection = %q, want %q", got, connectionID)
					}
				default:
					t.Fatal("verification request was not targeted at the eligible connection")
				}
				select {
				case <-sink.broadcasted:
					t.Fatal("verification request was broadcast to every connection")
				default:
				}
				return
			}
			if response.Action != mcp.MCPElicitationActionCancel {
				t.Fatalf("response = %#v, want cancel", response)
			}
			if reason, ok := mcp.UserVerificationReasonFromMeta(response.Meta); !ok || reason != mcp.UserVerificationReasonApprovalUnavailable {
				t.Fatalf("cancellation meta = %#v", response.Meta)
			}
			select {
			case <-sink.targeted:
				t.Fatal("ineligible connection received a verification request")
			default:
			}
		})
	}
}

func TestRemoteControlTransportStampsNetworkOriginLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	NewRemoteControlTransportServer(router, nil)
	if router.requestTransport != "remote_control" {
		t.Fatalf("remote-control transport = %q, want remote_control", router.requestTransport)
	}
}
