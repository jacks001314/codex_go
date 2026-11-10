package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/tcptunnel"
)

// TestTcpTunnelSSHRequiresChatGPTLogin mirrors Rust's dispatch check: the SSH
// relay only accepts a ChatGPT OAuth login (not an API key, not the managed
// chatgptAuthTokens mode), and it refuses before touching the network.
func TestTcpTunnelSSHRequiresChatGPTLogin(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		save   func(t *testing.T, home string)
		expect string
	}{
		{
			name: "no credential",
			save: func(t *testing.T, home string) {},
		},
		{
			name: "api key",
			save: func(t *testing.T, home string) {
				if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			},
		},
		{
			name: "managed chatgpt tokens",
			save: func(t *testing.T, home string) {
				snapshot := auth.AuthDotJSON{
					AuthMode: "chatgptAuthTokens",
					Tokens:   map[string]any{"access_token": "access-token"},
				}
				if err := auth.NewStore(home).Save(snapshot); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			testCase.save(t, home)
			args, err := tcptunnel.ParseArgs([]string{"--environment-id", "ccarenv_test"})
			if err != nil {
				t.Fatalf("ParseArgs() error = %v", err)
			}
			var stdout bytes.Buffer
			err = runTcpTunnelSSH(context.Background(), args, strings.NewReader(""), &stdout)
			if err == nil || !strings.Contains(err.Error(), "ChatGPT OAuth login required for SSH") {
				t.Fatalf("runTcpTunnelSSH() error = %v, want the OAuth requirement", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("runTcpTunnelSSH() wrote %q", stdout.String())
			}
		})
	}
}

// TestTcpTunnelSSHRefusesUntrustedGateway keeps the OAuth credential bound to
// the built-in gateway before any QUIC handshake.
func TestTcpTunnelSSHRefusesUntrustedGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   map[string]any{"access_token": "access-token"},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	args, err := tcptunnel.ParseArgs([]string{
		"--proxy-url", "https://codex-portals.api.openai.com.bad",
		"--target", "env:22",
	})
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	var stdout bytes.Buffer
	err = runTcpTunnelSSH(context.Background(), args, strings.NewReader(""), &stdout)
	if err == nil || !strings.Contains(err.Error(), "proxy URL origin is not trusted") {
		t.Fatalf("runTcpTunnelSSH() error = %v, want the untrusted-origin refusal", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runTcpTunnelSSH() wrote %q", stdout.String())
	}
}
