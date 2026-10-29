package app

import (
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
)

// TestHasExplicitRemotePermissionOverrideLikeRust covers Rust #43330's
// has_explicit_resume_permission_override inputs that live on the launch flags
// and generic `-c` overrides.
func TestHasExplicitRemotePermissionOverrideLikeRust(t *testing.T) {
	profile := "work"
	tests := []struct {
		name string
		root *cli.RootOptions
		want bool
	}{
		{name: "nil root", root: nil},
		{name: "no overrides", root: &cli.RootOptions{}},
		{name: "sandbox flag", root: &cli.RootOptions{Shared: cli.SharedOptions{Sandbox: "read-only"}}, want: true},
		{name: "approval flag", root: &cli.RootOptions{Shared: cli.SharedOptions{ApprovalPolicy: "never"}}, want: true},
		{name: "add-dir", root: &cli.RootOptions{Shared: cli.SharedOptions{AddDirs: []string{"/extra"}}}, want: true},
		{name: "bypass approvals and sandbox", root: &cli.RootOptions{Shared: cli.SharedOptions{DangerouslyBypassApprovalsAndSandbox: true}}, want: true},
		{name: "config sandbox_mode", root: &cli.RootOptions{ConfigOverrides: []string{"sandbox_mode=workspace-write"}}, want: true},
		{name: "config nested permissions", root: &cli.RootOptions{ConfigOverrides: []string{"permissions.network.enabled=true"}}, want: true},
		{name: "config workspace roots", root: &cli.RootOptions{ConfigOverrides: []string{"workspace_roots=[\"/extra\"]"}}, want: true},
		{name: "unrelated config", root: &cli.RootOptions{ConfigOverrides: []string{"model=gpt-5"}}},
		{name: "profile alone is not an override", root: &cli.RootOptions{Shared: cli.SharedOptions{Profile: profile}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasExplicitRemotePermissionOverride(tt.root); got != tt.want {
				t.Fatalf("hasExplicitRemotePermissionOverride() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLayersCarryRemotePermissionOverrideLikeRust keeps Rust's layer rule: a
// session-flags layer or a profile-scoped user layer is a launch selection,
// while project and plain user layers are saved configuration.
func TestLayersCarryRemotePermissionOverrideLikeRust(t *testing.T) {
	profile := "work"
	sessionFlags := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceSessionFlags},
		Config: map[string]any{"sandbox_mode": "read-only"},
	}
	profileUser := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceUser, Profile: &profile},
		Config: map[string]any{"approvals_reviewer": "guardian"},
	}
	plainUser := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceUser},
		Config: map[string]any{"sandbox_mode": "read-only"},
	}
	project := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceProject},
		Config: map[string]any{"permissions": map[string]any{"network": map[string]any{"enabled": false}}},
	}
	unrelated := config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceSessionFlags},
		Config: map[string]any{"model": "gpt-5"},
	}
	tests := []struct {
		name   string
		layers []config.Layer
		want   bool
	}{
		{name: "session flags", layers: []config.Layer{sessionFlags}, want: true},
		{name: "profile-scoped user", layers: []config.Layer{profileUser}, want: true},
		{name: "plain user layer", layers: []config.Layer{plainUser}},
		{name: "project layer", layers: []config.Layer{project}},
		{name: "unrelated session flag", layers: []config.Layer{unrelated}},
		{name: "empty", layers: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := layersCarryRemotePermissionOverride(tt.layers); got != tt.want {
				t.Fatalf("layersCarryRemotePermissionOverride() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRemoteForkRejectsPermissionOverrideLikeRust locks the fork half: the
// refusal happens before the handler opens a connection, so a nil endpoint still
// reports the permission error rather than a transport failure.
func TestRemoteForkRejectsPermissionOverrideLikeRust(t *testing.T) {
	backendID := codextui.NewBackendID()
	handler := interactiveRemoteSessionActionHandler(nil, nil, &cli.RootOptions{
		Remote: "wss://example.com/rpc",
		Shared: cli.SharedOptions{Sandbox: "read-only"},
	}, backendID)
	selection := codextui.SessionSelection{
		Kind:   codextui.SessionSelectionFork,
		Target: codextui.SessionTarget{BackendID: backendID, ThreadID: "thread-parent"},
	}
	if _, err := handler(selection); err == nil || !strings.Contains(err.Error(), "Permission overrides are not supported when forking a remote task.") {
		t.Fatalf("remote fork override error = %v", err)
	}
	inheriting := interactiveRemoteSessionActionHandler(nil, nil, &cli.RootOptions{Remote: "wss://example.com/rpc"}, backendID)
	if _, err := inheriting(selection); err == nil || !strings.Contains(err.Error(), "remote app-server") {
		t.Fatalf("inheriting fork should reach the transport, error = %v", err)
	}
}

// TestRemoteResumeRejectsPermissionOverrideLikeRust locks the resume half: an
// explicit remote workspace refuses a launch override, while a local daemon
// keeps the launch's settings and the inherited case sends no permission fields.
func TestRemoteResumeRejectsPermissionOverrideLikeRust(t *testing.T) {
	remoteRoot := &cli.RootOptions{Remote: "wss://example.com/rpc"}
	if _, err := remoteTUIResumeParams(remoteRoot, "thread-resume", ""); err != nil {
		t.Fatalf("inherited remote resume error = %v", err)
	}
	overridden := &cli.RootOptions{Remote: "wss://example.com/rpc", Shared: cli.SharedOptions{Sandbox: "read-only"}}
	params, err := remoteTUIResumeParams(overridden, "thread-resume", "")
	if err == nil || !strings.Contains(err.Error(), "Permission overrides are not supported when resuming a remote task.") {
		t.Fatalf("remote resume override error = %v", err)
	}
	if params.ApprovalPolicy != nil || params.Sandbox != nil || len(params.Config) != 0 {
		t.Fatalf("rejected resume carried permission params: %#v", params)
	}
	local := &cli.RootOptions{Shared: cli.SharedOptions{Sandbox: "read-only"}}
	if _, err := remoteTUIResumeParams(local, "thread-resume", "/saved/session"); err != nil {
		t.Fatalf("local daemon resume error = %v", err)
	}
}
