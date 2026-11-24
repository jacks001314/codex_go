package app

import (
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/cli"
)

// TestRemoteTUIClientModeFollowsEndpointLikeRust mirrors Rust #57267: thread
// settings ownership comes from the opened transport, not the CLI flags. A
// local daemon reached through --remote keeps client-owned settings, while a
// hosted Cloud endpoint is server-owned.
func TestRemoteTUIClientModeFollowsEndpointLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		remote   string
		endpoint *appserverdaemon.RemoteAppServerEndpoint
		want     ThreadParamsMode
	}{
		{
			name:     "local daemon keeps embedded settings",
			remote:   "ws://127.0.0.1:4500/rpc",
			endpoint: appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:4500/rpc", nil),
			want:     ThreadParamsModeEmbedded,
		},
		{
			name:     "explicit remote server",
			remote:   "wss://example.com/rpc",
			endpoint: appserverdaemon.NewWebSocketEndpoint("wss://example.com/rpc", nil),
			want:     ThreadParamsModeRemote,
		},
		{
			name:     "hosted cloud endpoint",
			remote:   "cloud",
			endpoint: appserverdaemon.NewCloudEndpoint(),
			want:     ThreadParamsModeCloud,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := &cli.RootOptions{Remote: testCase.remote}
			client := newRemoteTUIPerTurnClient(root, testCase.endpoint, nil, nil, remoteTUIBrokers{})
			if client.threadParamsMode != testCase.want {
				t.Fatalf("threadParamsMode = %q, want %q", client.threadParamsMode, testCase.want)
			}
			wantOwner := ThreadSettingsOwnerClient
			if testCase.want == ThreadParamsModeCloud {
				wantOwner = ThreadSettingsOwnerServer
			}
			if got := client.threadParamsMode.SettingsOwner(); got != wantOwner {
				t.Fatalf("settings owner = %v, want %v", got, wantOwner)
			}
		})
	}
}
