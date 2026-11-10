package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// Rust #60500 derives the `codex.conversation_starts` sandbox_policy label from
// the effective permission profile instead of building a legacy SandboxPolicy
// first. Go already labels from the profile; this pins the historical labels so
// the refactor keeps them.
func TestSandboxTelemetryLabelComesFromPermissionProfileLikeRust(t *testing.T) {
	cwd := t.TempDir()
	workspaceWritable := sandbox.NewWorkspaceWritePolicy()
	workspaceWritable.WritableRoots = []string{filepath.Join(cwd, "extra")}

	tests := []struct {
		name    string
		profile *sandbox.PermissionProfile
		want    string
	}{
		{"disabled", &sandbox.PermissionProfile{Disabled: true}, "danger-full-access"},
		{"read-only", &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewReadOnlyPolicy()}, "read-only"},
		{"workspace-write", &sandbox.PermissionProfile{SandboxPolicy: workspaceWritable}, "workspace-write"},
		{
			name:    "full disk with network",
			profile: &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewDangerFullAccessPolicy(), NetworkEnabled: true},
			want:    "danger-full-access",
		},
		{
			name:    "external sandbox without network",
			profile: &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkRestricted)},
			want:    "external-sandbox",
		},
		{
			name:    "external sandbox with network",
			profile: &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkEnabled)},
			want:    "danger-full-access",
		},
		{
			name:    "writable profile without legacy projection",
			profile: &sandbox.PermissionProfile{SandboxPolicy: workspaceWritable, NetworkEnabled: true},
			want:    "workspace-write",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolution := &config.SandboxPermissionProfileResolution{Profile: test.profile}
			if got := telemetrySandboxPolicy(resolution, cwd); got != test.want {
				t.Fatalf("telemetrySandboxPolicy() = %q, want %q", got, test.want)
			}
		})
	}
	if got := telemetrySandboxPolicy(nil, cwd); got != "" {
		t.Fatalf("telemetrySandboxPolicy(nil) = %q, want empty", got)
	}
}
