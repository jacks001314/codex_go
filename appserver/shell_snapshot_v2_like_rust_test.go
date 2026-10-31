package appserver

import (
	"context"
	"testing"

	execserverclient "codex_go/execserver"
	"codex_go/tool"
)

// Rust #39957 `shell_snapshot_request`: the executor-owned snapshot is requested
// only with the `shell_snapshot_v2` feature, a supported environment, the
// environment's own directory, and a POSIX login shell, and its scope is
// `thread:environment`.
func TestShellSnapshotV2ProviderGatesLikeRust(t *testing.T) {
	previous := localShellSnapshotV2Supported
	localShellSnapshotV2Supported = func() bool { return true }
	t.Cleanup(func() { localShellSnapshotV2Supported = previous })

	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = false\nshell_snapshot_v2 = true\n")
	t.Cleanup(func() { _ = router.Close() })
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		t.Fatal("effectiveWriteStdinConfig() = nil")
	}
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("a session with shell_snapshot_v2 has no provider")
	}

	base := tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
		EnvironmentID:   "work",
	}
	result := provider(context.Background(), base)
	if result.Local == nil {
		t.Fatalf("local result = %#v, want an executor snapshot request", result)
	}
	if result.Local.ScopeID != threadID+":work" || result.Local.Shell.Name != "bash" || result.Local.Shell.Path != "/bin/bash" {
		t.Fatalf("local snapshot = %#v", result.Local)
	}
	if result.Path != "" || result.Remote != nil {
		t.Fatalf("v2-only result = %#v, want no session file or remote request", result)
	}

	noEnvironment := base
	noEnvironment.EnvironmentID = ""
	if got := provider(context.Background(), noEnvironment); got.Local == nil ||
		got.Local.ScopeID != threadID+":"+execserverclient.LocalEnvironmentID {
		t.Fatalf("default-environment snapshot = %#v", got.Local)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*tool.SnapshotProviderRequest)
	}{
		{"non-login launch", func(request *tool.SnapshotProviderRequest) { request.AllowLoginShell = false }},
		{"PowerShell", func(request *tool.SnapshotProviderRequest) { request.ShellType = tool.ShellPowerShell }},
		{"other directory", func(request *tool.SnapshotProviderRequest) { request.CWD = t.TempDir() }},
	} {
		request := base
		tc.mutate(&request)
		if got := provider(context.Background(), request); got.Local != nil || got.Remote != nil || got.Path != "" {
			t.Fatalf("%s: result = %#v, want no executor snapshot", tc.name, got)
		}
	}

	// A remote environment needs the capability from its cached environment info.
	remote := base
	remote.Remote = true
	if got := provider(context.Background(), remote); got.Remote != nil {
		t.Fatalf("remote without capability = %#v, want no request", got.Remote)
	}
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)
	router.services.Environment.cacheEnvironmentInfo("work", &EnvironmentInfoResponse{
		Capabilities: execserverclient.EnvironmentCapabilities{ShellSnapshotV2: true},
	})
	if got := provider(context.Background(), remote); got.Remote == nil || got.Remote.ScopeID != threadID+":work" {
		t.Fatalf("remote snapshot = %#v, want thread:environment scope", got.Remote)
	}

	// The local executor must advertise the capability too.
	localShellSnapshotV2Supported = func() bool { return false }
	if got := provider(context.Background(), base); got.Local != nil {
		t.Fatalf("unsupported local executor = %#v, want no request", got.Local)
	}
}

// The feature gate is Rust's `Feature::ShellSnapshotV2`: without it the session
// file snapshot alone answers the launch.
func TestShellSnapshotV2ProviderFeatureGateLikeRust(t *testing.T) {
	router, threadID, _ := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = false\nshell_snapshot_v2 = false\n")
	t.Cleanup(func() { _ = router.Close() })
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		t.Fatal("effectiveWriteStdinConfig() = nil")
	}
	if provider := router.shellSnapshotProviderForTurn(threadID, cfg); provider != nil {
		t.Fatal("a session without either snapshot feature has a provider")
	}
}

// Both mechanisms are independent in Rust: with v1 and v2 enabled the provider
// answers with the session file path and the executor request together.
func TestShellSnapshotProviderCombinesV1AndV2LikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	previous := localShellSnapshotV2Supported
	localShellSnapshotV2Supported = func() bool { return true }
	t.Cleanup(func() { localShellSnapshotV2Supported = previous })

	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = true\nshell_snapshot_v2 = true\n")
	t.Cleanup(func() { _ = router.Close() })
	cfg := router.effectiveWriteStdinConfig(threadID)
	if cfg == nil {
		t.Fatal("effectiveWriteStdinConfig() = nil")
	}
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no snapshot provider")
	}
	result := provider(context.Background(), tool.SnapshotProviderRequest{
		ShellType:           tool.ShellBash,
		ShellPath:           "/bin/bash",
		CWD:                 cwd,
		AllowLoginShell:     true,
		PermissionProfileID: "resolved",
		EnvironmentID:       "work",
	})
	if result.Path == "" {
		t.Fatalf("result = %#v, want the session file snapshot", result)
	}
	if result.Local == nil || result.Local.ScopeID != threadID+":work" {
		t.Fatalf("result = %#v, want the executor snapshot request", result)
	}
}
