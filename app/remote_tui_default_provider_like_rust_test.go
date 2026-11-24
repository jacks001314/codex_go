package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
)

// TestRemoteDefaultModelProviderComesFromTheServerLikeRust mirrors Rust #63553
// startup_catalog_provider: a 0.169+ server answers provider/list with the
// provider new remote threads default to, and older servers keep the
// configured provider because they ignore the modelProvider selector.
func TestRemoteDefaultModelProviderComesFromTheServerLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		userAgent   string
		wantDefault string
		wantListed  bool
	}{
		{name: "supported server", userAgent: "codex_app_server/0.169.0 (linux; x86_64)", wantDefault: "acme", wantListed: true},
		{name: "older server", userAgent: "codex_app_server/0.168.3 (linux; x86_64)", wantDefault: "", wantListed: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listed := false
			serverErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					remoteTUITestSendErr(serverErr, err)
					return
				}
				defer conn.Close(websocket.StatusNormalClosure, "")
				for {
					req, err := remoteTUITestReadRequest(ctx, conn)
					if err != nil {
						return
					}
					switch req.Method {
					case string(appserver.MethodInitialize):
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"codexHome":      "",
							"platformFamily": "unix",
							"platformOs":     "linux",
							"userAgent":      testCase.userAgent,
						}})
					case string(appserver.MethodModelProviderList):
						listed = true
						remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
							"data": []any{map[string]any{
								"id":      "acme",
								"name":    "Acme",
								"canEdit": false,
							}},
							"defaultProviderId": "acme",
							"canChangeDefault":  true,
						}})
					default:
						remoteTUITestSendErr(serverErr, nil)
						return
					}
				}
			}))
			defer server.Close()

			endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if got := interactiveRemoteDefaultModelProvider(ctx, endpoint); got != testCase.wantDefault {
				t.Fatalf("default provider = %q, want %q", got, testCase.wantDefault)
			}
			if listed != testCase.wantListed {
				t.Fatalf("provider/list called = %v, want %v", listed, testCase.wantListed)
			}
		})
	}
}

// TestCloudKeepsNoRemoteDefaultProviderLikeRust mirrors Rust #63553: Cloud
// supplies the model on activation, so startup discovery is skipped.
func TestCloudKeepsNoRemoteDefaultProviderLikeRust(t *testing.T) {
	if got := interactiveRemoteDefaultModelProvider(context.Background(), appserverdaemon.NewCloudEndpoint()); got != "" {
		t.Fatalf("Cloud default provider = %q, want empty", got)
	}
}
