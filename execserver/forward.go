package execserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// ForwardOptions configures exec-server forwarding mode (Rust #39249):
// an existing WebSocket exec-server is connected and served as a remote
// environment, forwarding complete payloads unchanged in both directions.
type ForwardOptions struct {
	ConnectURL    string
	EnvironmentID string
	Name          string
	HTTPClient    *http.Client
}

// ForwardServer connects to an existing exec-server WebSocket and serves it
// over the standard exec-server transport. Each authenticated Noise relay
// stream gets its own destination connection through the server's relay
// harness, and complete payloads are forwarded without modification.
func ForwardServer(ctx context.Context, options *ForwardOptions) error {
	if options == nil {
		return errors.New("exec-server forward options are required")
	}
	connectURL := strings.TrimSpace(options.ConnectURL)
	if connectURL == "" {
		return errors.New("exec-server forward requires --connect ws://HOST:PORT")
	}
	if !strings.HasPrefix(connectURL, "ws://") && !strings.HasPrefix(connectURL, "wss://") {
		return fmt.Errorf("exec-server forward requires a ws:// or wss:// connect URL, got %q", connectURL)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = remoteHTTPClient()
	}
	dialCtx, cancel := context.WithTimeout(ctx, defaultRemoteDialTimeout)
	defer cancel()
	conn, response, err := websocket.Dial(dialCtx, connectURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		if response != nil {
			return fmt.Errorf("exec-server forward connect to %s failed: HTTP %d: %w", connectURL, response.StatusCode, err)
		}
		return fmt.Errorf("exec-server forward connect to %s failed: %w", connectURL, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 * 1024 * 1024)
	server := NewServerWithHTTPClient(httpClient)
	return server.serveWebSocketConnection(ctx, conn)
}
