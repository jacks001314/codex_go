package tool

import (
	"testing"

	"codex_go/sandbox"
)

func TestExecutorWindowsSandboxLevelSelectsRestrictedTokenForWindowsPaths(t *testing.T) {
	windowsCWD := `C:\Users\codex\project`
	posixCWD := "/home/codex/project"
	for _, tc := range []struct {
		name  string
		level sandbox.WindowsSandboxLevel
		cwd   string
		want  sandbox.WindowsSandboxLevel
	}{
		{
			name:  "disabled windows path upgrades to unelevated",
			level: sandbox.WindowsSandboxDisabled,
			cwd:   windowsCWD,
			want:  sandbox.WindowsSandboxUnelevated,
		},
		{
			name:  "disabled posix path stays disabled",
			level: sandbox.WindowsSandboxDisabled,
			cwd:   posixCWD,
			want:  sandbox.WindowsSandboxDisabled,
		},
		{
			name:  "disabled empty cwd stays disabled",
			level: sandbox.WindowsSandboxDisabled,
			cwd:   "",
			want:  sandbox.WindowsSandboxDisabled,
		},
		{
			name:  "configured level preserved for windows path",
			level: sandbox.WindowsSandboxElevated,
			cwd:   windowsCWD,
			want:  sandbox.WindowsSandboxElevated,
		},
		{
			name:  "configured unelevated preserved",
			level: sandbox.WindowsSandboxUnelevated,
			cwd:   windowsCWD,
			want:  sandbox.WindowsSandboxUnelevated,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := executorWindowsSandboxLevel(tc.level, tc.cwd); got != tc.want {
				t.Fatalf("executorWindowsSandboxLevel(%q, %q) = %q, want %q", tc.level, tc.cwd, got, tc.want)
			}
		})
	}
}

func TestPathUsesWindowsConvention(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: `C:\Users\codex`, want: true},
		{path: `C:/Users/codex`, want: true},
		{path: `\\server\share`, want: true},
		{path: "/home/codex", want: false},
		{path: "relative/path", want: false},
		{path: "", want: false},
	} {
		if got := pathUsesWindowsConvention(tc.path); got != tc.want {
			t.Fatalf("pathUsesWindowsConvention(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestNewFileSystemSandboxContextUpgradesWindowsLevelLikeRust(t *testing.T) {
	profile := &sandbox.PermissionProfile{Disabled: false, SandboxPolicy: sandbox.NewWorkspaceWritePolicy()}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	windowsCWD := `C:\Users\codex\project`
	context, err := NewFileSystemSandboxContext(FileSystemSandboxContextOptions{
		PermissionProfile:     profile,
		PermissionProfileJSON: profileJSON,
		CWD:                   windowsCWD,
		WindowsSandboxLevel:   sandbox.WindowsSandboxDisabled,
	})
	if err != nil {
		t.Fatalf("NewFileSystemSandboxContext() error = %v", err)
	}
	if context.WindowsSandboxLevel != string(sandbox.WindowsSandboxUnelevated) {
		t.Fatalf("WindowsSandboxLevel = %q, want %q", context.WindowsSandboxLevel, sandbox.WindowsSandboxUnelevated)
	}

	posixCWD := "/home/codex/project"
	context, err = NewFileSystemSandboxContext(FileSystemSandboxContextOptions{
		PermissionProfile:     profile,
		PermissionProfileJSON: profileJSON,
		CWD:                   posixCWD,
		WindowsSandboxLevel:   sandbox.WindowsSandboxDisabled,
	})
	if err != nil {
		t.Fatalf("NewFileSystemSandboxContext() error = %v", err)
	}
	if context.WindowsSandboxLevel != string(sandbox.WindowsSandboxDisabled) {
		t.Fatalf("WindowsSandboxLevel = %q, want %q", context.WindowsSandboxLevel, sandbox.WindowsSandboxDisabled)
	}
}
