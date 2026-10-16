package execserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestNoiseHandshakeReportsDisconnectReasonLikeRust mirrors Rust #54637's
// `noise_handshake_reports_disconnect_reason`
// (codex-rs/exec-server/src/client_transport_tests.rs): a Noise rendezvous
// handshake that fails before the connection becomes ready must report the
// transport's own disconnect reason, which includes both an unexpected
// non-binary frame and a relay stream reset.
//
// Go performs the rendezvous handshake inline in `dialNoiseHarnessConnection`,
// so the transport reason is the error the caller already receives (Rust's
// pre-readiness failure was a generic `Disconnected` message because the reason
// sat unread in the connection event channel until #54637 re-attached it). This
// test pins that property against the same two server behaviours.
func TestNoiseHandshakeReportsDisconnectReasonLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		sendReset  bool
		wantReason string
	}{
		{
			name:       "unexpected_text_frame",
			wantReason: "Noise relay transport expects binary protobuf frames",
		},
		{
			name:       "relay_stream_reset",
			sendReset:  true,
			wantReason: "Noise relay stream reset",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			executorIdentity, err := generateRemoteNoiseIdentity()
			if err != nil {
				t.Fatalf("generate executor identity: %v", err)
			}
			defer executorIdentity.Destroy()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				messageType, payload, err := conn.Read(ctx)
				if err != nil || messageType != websocket.MessageBinary {
					return
				}
				resume, err := decodeRelayMessageFrame(payload)
				if err != nil || resume.Kind != relayFrameResume {
					return
				}
				// The harness handshake frame follows the resume frame; the server
				// never has to answer it with a valid handshake.
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
				if !testCase.sendReset {
					_ = conn.Write(ctx, websocket.MessageText, []byte("untrusted text"))
					return
				}
				encoded, err := encodeRelayMessageFrame(relayMessageFrame{
					Version:     relayMessageFrameVersion,
					StreamID:    resume.StreamID,
					Kind:        relayFrameReset,
					ResetReason: "untrusted reason",
				})
				if err != nil {
					return
				}
				_ = conn.Write(ctx, websocket.MessageBinary, encoded)
			}))
			defer server.Close()

			identity, err := generateRemoteNoiseIdentity()
			if err != nil {
				t.Fatalf("generate harness identity: %v", err)
			}
			defer identity.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			_, err = dialNoiseHarnessConnection(ctx, &NoiseRendezvousConnectBundle{
				WebSocketURL:            "ws" + strings.TrimPrefix(server.URL, "http"),
				EnvironmentID:           "environment-1",
				ExecutorRegistrationID:  "registration-1",
				ExecutorPublicKey:       executorIdentity.PublicKey(),
				HarnessKeyAuthorization: "authorization-1",
			}, identity, server.Client())
			if err == nil {
				t.Fatal("dialNoiseHarnessConnection() = nil, want the handshake failure")
			}
			if !strings.Contains(err.Error(), testCase.wantReason) {
				t.Fatalf("handshake error = %q, want it to carry %q", err, testCase.wantReason)
			}
			// The reset frame's reason is untrusted relay input, so it must never
			// be echoed back as the disconnect reason.
			if strings.Contains(err.Error(), "untrusted reason") {
				t.Fatalf("handshake error = %q, want the untrusted reset reason dropped", err)
			}
		})
	}
}
