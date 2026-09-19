package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/cli"
)

const (
	remoteAddDirError      = "--add-dir is not supported with --remote. Configure additional workspace roots on the server."
	remoteWritableRootsErr = "sandbox_workspace_write.writable_roots overrides are not supported with --remote. Configure additional workspace roots on the server."
)

// Mirrors Rust cli/tests/features.rs (#46494): client-configured workspace roots
// belong to the client host and are rejected with --remote before connecting.
func TestInteractiveRemoteWorkspaceRootRejectionsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		root    *cli.RootOptions
		wantErr string
	}{
		{
			name:    "add-dir",
			root:    &cli.RootOptions{Remote: "ws://127.0.0.1:1", Shared: cli.SharedOptions{AddDirs: []string{"remote-extra"}}},
			wantErr: remoteAddDirError,
		},
		{
			name:    "writable-roots dotted override",
			root:    &cli.RootOptions{Remote: "ws://127.0.0.1:1", ConfigOverrides: []string{`sandbox_workspace_write.writable_roots=["./extra"]`}},
			wantErr: remoteWritableRootsErr,
		},
		{
			name:    "writable-roots inline table override",
			root:    &cli.RootOptions{Remote: "ws://127.0.0.1:1", ConfigOverrides: []string{`sandbox_workspace_write={writable_roots=["./extra"],network_access=true}`}},
			wantErr: remoteWritableRootsErr,
		},
		{
			name: "network-access dotted override stays valid",
			root: &cli.RootOptions{Remote: "ws://127.0.0.1:1", ConfigOverrides: []string{"sandbox_workspace_write.network_access=true"}},
		},
		{
			name: "network-access inline table override stays valid",
			root: &cli.RootOptions{Remote: "ws://127.0.0.1:1", ConfigOverrides: []string{"sandbox_workspace_write={network_access=true}"}},
		},
		{
			name: "embedded keeps explicit roots",
			root: &cli.RootOptions{Shared: cli.SharedOptions{AddDirs: []string{"local-extra"}}, ConfigOverrides: []string{`sandbox_workspace_write.writable_roots=["./extra"]`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := interactiveRemoteWorkspaceRootError(tc.root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("interactiveRemoteWorkspaceRootError() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("interactiveRemoteWorkspaceRootError() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// The check runs before connecting, so it surfaces as the fatal TUI error
// (Rust reports the same text as the process error).
func TestInteractiveRemoteAddDirFailsFatalLikeRust(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--remote", "ws://127.0.0.1:1", "--add-dir", "remote-extra"}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("error = %+v, want silent exit 1", err)
	}
	if !strings.Contains(stderr.String(), remoteAddDirError) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), remoteAddDirError)
	}
}

// With a network-access override the launch proceeds past the workspace-root
// check and only then requires a terminal, matching Rust's ordering.
func TestInteractiveRemoteNetworkAccessOverrideProceedsPastRootCheck(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--remote", "ws://127.0.0.1:1", "-c", "sandbox_workspace_write.network_access=true"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil && strings.Contains(err.Error(), "writable_roots") {
		t.Fatalf("error = %v, want the root check to pass", err)
	}
	if strings.Contains(stderr.String(), remoteWritableRootsErr) {
		t.Fatalf("stderr = %q, want the root check to pass", stderr.String())
	}
}

// The session commands (resume/fork/archive/queue --remote) share the same
// workspace-root rejection as the interactive TUI (Rust #46494 runs the check
// in the shared TUI startup path).
func TestSessionRemoteWorkspaceRootRejectionsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts *cli.SessionOptions
		root *cli.RootOptions
	}{
		{
			name: "resume add-dir",
			opts: &cli.SessionOptions{Remote: "ws://127.0.0.1:1", Shared: cli.SharedOptions{AddDirs: []string{"extra"}}},
		},
		{
			name: "fork writable roots",
			root: &cli.RootOptions{Remote: "ws://127.0.0.1:1", ConfigOverrides: []string{`sandbox_workspace_write.writable_roots=["./extra"]`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveSessionRemoteEndpoint(tc.opts, tc.root); err == nil {
				t.Fatal("resolveSessionRemoteEndpoint() = nil error, want workspace root rejection")
			}
		})
	}
	// A network-access override passes the root check and resolves the endpoint.
	endpoint, err := resolveSessionRemoteEndpoint(&cli.SessionOptions{
		Remote:          "ws://127.0.0.1:1",
		ConfigOverrides: []string{"sandbox_workspace_write.network_access=true"},
	}, nil)
	if err != nil || endpoint == nil {
		t.Fatalf("network-access override: endpoint %v, err %v", endpoint, err)
	}
}
