package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust parity: codex-rs/cli/src/exec_server_command.rs (#47601): the shared
// WebsocketAuthArgs family is accepted by `codex exec-server` for direct
// WebSocket listeners and rejected for stdio, --remote and forward.
func TestParseExecServerWebSocketAuthFlagsLikeRust(t *testing.T) {
	digest := strings.Repeat("a", 64)

	parsed, err := Parse([]string{
		"exec-server",
		"--listen", "ws://127.0.0.1:9999",
		"--ws-auth", "capability-token",
		"--ws-token-sha256", digest,
	})
	if err != nil {
		t.Fatalf("Parse exec-server capability token returned error: %v", err)
	}
	if parsed.ExecServer.WebSocketAuthOptions.WSAuth != "capability-token" ||
		parsed.ExecServer.WebSocketAuthOptions.WSTokenSHA256 != digest ||
		!parsed.ExecServer.WebSocketAuthOptions.WSTokenSHA256Set {
		t.Fatalf("exec-server websocket auth = %#v", parsed.ExecServer.WebSocketAuthOptions)
	}

	secretFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretFile, []byte(strings.Repeat("s", 32)), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	signed, err := Parse([]string{
		"exec-server",
		"--listen=ws://127.0.0.1:9999",
		"--ws-auth=signed-bearer-token",
		"--ws-shared-secret-file=" + secretFile,
		"--ws-issuer", "exec-server",
		"--ws-audience", "executor",
		"--ws-max-clock-skew-seconds", "45",
	})
	if err != nil {
		t.Fatalf("Parse exec-server signed bearer token returned error: %v", err)
	}
	auth := signed.ExecServer.WebSocketAuthOptions
	if auth.WSAuth != "signed-bearer-token" || auth.WSSharedSecretFile != secretFile ||
		auth.WSIssuer != "exec-server" || auth.WSAudience != "executor" ||
		auth.WSMaxClockSkewSeconds == nil || *auth.WSMaxClockSkewSeconds != 45 {
		t.Fatalf("exec-server signed auth = %#v", auth)
	}

	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "remote is incompatible with listener auth",
			args: []string{
				"exec-server",
				"--remote", "ws://127.0.0.1:7777",
				"--environment-id", "env-1",
				"--ws-auth", "capability-token",
				"--ws-token-sha256", digest,
			},
			want: "WebSocket listener auth cannot be used with --remote or forward",
		},
		{
			name: "forward is incompatible with listener auth",
			args: []string{
				"exec-server", "forward",
				"--connect", "ws://127.0.0.1:7777",
				"--ws-auth", "capability-token",
				"--ws-token-sha256", digest,
			},
			want: "WebSocket listener auth cannot be used with --remote or forward",
		},
		{
			name: "stdio listener cannot authenticate upgrades",
			args: []string{
				"exec-server",
				"--listen", "stdio",
				"--ws-auth", "capability-token",
				"--ws-token-sha256", digest,
			},
			want: "WebSocket listener auth requires a WebSocket --listen transport",
		},
		{
			name: "auth flags require a mode",
			args: []string{
				"exec-server",
				"--listen", "ws://127.0.0.1:9999",
				"--ws-token-sha256", digest,
			},
			want: "websocket auth flags require `--ws-auth capability-token` or `--ws-auth signed-bearer-token`",
		},
		{
			name: "capability token requires a token source",
			args: []string{
				"exec-server",
				"--listen", "ws://127.0.0.1:9999",
				"--ws-auth", "capability-token",
			},
			want: "`--ws-token-file` or `--ws-token-sha256` is required when `--ws-auth capability-token` is set",
		},
		{
			name: "unknown websocket auth flag",
			args: []string{"exec-server", "--ws-unknown", "value"},
			want: "unknown exec-server option --ws-unknown",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Parse(testCase.args)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Parse(%v) error = %v, want %q", testCase.args, err, testCase.want)
			}
		})
	}
}
