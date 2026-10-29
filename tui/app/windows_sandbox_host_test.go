package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/execserver"
)

// Mirrors Rust #45813 app/tests/startup.rs
// windows_sandbox_host_from_observed_environments.
func TestWindowsSandboxHostFromObservedEnvironments(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		environmentIDs []string
		want           WindowsSandboxHost
	}{
		{name: "missing", environmentIDs: nil, want: WindowsSandboxHostUnknown},
		{name: "empty", environmentIDs: []string{}, want: WindowsSandboxHostUnknown},
		{name: "local", environmentIDs: []string{execserver.LocalEnvironmentID}, want: WindowsSandboxHostLocal},
		{name: "remote", environmentIDs: []string{"remote"}, want: WindowsSandboxHostRemote},
		{
			name:           "mixed",
			environmentIDs: []string{"remote", execserver.LocalEnvironmentID},
			want:           WindowsSandboxHostMixed,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			environments := make([]appserver.ThreadEnvironment, 0, len(testCase.environmentIDs))
			for _, id := range testCase.environmentIDs {
				environments = append(environments, appserver.ThreadEnvironment{EnvironmentID: id, CWD: "/tmp"})
			}
			if got := HostFromEnvironments(environments); got != testCase.want {
				t.Fatalf("host = %q, want %q", got, testCase.want)
			}
		})
	}
}

// Mirrors Rust #45813 inactive-thread replay: a thread that reports a remote
// environment must restore the remote host classification with its session.
func TestSessionStateForThreadReadRestoresRemoteSandboxHost(t *testing.T) {
	session := SessionStateForThreadRead(nil, ThreadSessionDefaults{CWD: "/repo"}, ThreadReadSnapshot{
		ThreadID:     "agent-thread",
		CWD:          "/tmp/agent",
		Environments: []appserver.ThreadEnvironment{{EnvironmentID: "remote", CWD: "/tmp/agent"}},
	})
	if session.WindowsSandboxHost != WindowsSandboxHostRemote {
		t.Fatalf("host = %q, want remote", session.WindowsSandboxHost)
	}

	local := SessionStateForThreadRead(nil, ThreadSessionDefaults{CWD: "/repo"}, ThreadReadSnapshot{
		ThreadID: "agent-thread",
		CWD:      "/tmp/agent",
	})
	if local.WindowsSandboxHost != WindowsSandboxHostUnknown {
		t.Fatalf("missing environments host = %q, want unknown", local.WindowsSandboxHost)
	}
}
