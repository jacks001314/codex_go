package tool

import (
	"path/filepath"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust #42577 (core/tests/suite/remote_env.rs): a command approved on a
// remote executor reports that executor's working directory in its own path
// grammar, instead of the host path Rust used to derive from it.
func TestRemoteExecCommandKeepsTargetNativeCWDLikeRust(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{
		Shell: &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   "on-request",
			CWD:              `C:\host\workspace`,
			DefaultTimeoutMS: 5000,
		},
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{
			ID:            "remote",
			CWD:           "/srv/project",
			PlatformOS:    "linux",
			ExecServerURL: "ws://127.0.0.1:1",
			Shell:         &Shell{Type: ShellBash, Path: "/bin/sh"},
		}},
	})
	for _, testCase := range []struct {
		name           string
		arguments      string
		wantCWD        string
		wantConvention utils.PathConvention
	}{
		{name: "environment cwd", arguments: `{"cmd":"true","environment_id":"remote"}`, wantCWD: "/srv/project", wantConvention: utils.ConventionPosix},
		{name: "relative workdir", arguments: `{"cmd":"true","environment_id":"remote","workdir":"nested/dir"}`, wantCWD: "/srv/project/nested/dir", wantConvention: utils.ConventionPosix},
		{name: "absolute workdir", arguments: `{"cmd":"true","environment_id":"remote","workdir":"/tmp"}`, wantCWD: "/tmp", wantConvention: utils.ConventionPosix},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			prepared, err := executor.PrepareExecCommand(&Invocation{
				CallID:   "call-remote-cwd",
				ToolName: PlainName(DefaultExecCommandToolName),
				Payload:  Payload{Kind: PayloadFunction, Arguments: testCase.arguments},
			})
			if err != nil {
				t.Fatalf("PrepareExecCommand() error = %v", err)
			}
			if prepared.Request.CWD != testCase.wantCWD {
				t.Fatalf("request cwd = %q, want %q", prepared.Request.CWD, testCase.wantCWD)
			}
			if prepared.Request.PathConvention != testCase.wantConvention {
				t.Fatalf("path convention = %q, want %q", prepared.Request.PathConvention, testCase.wantConvention)
			}
		})
	}

	// A local command still resolves its launch directory against the host.
	hostCWD := t.TempDir()
	local := NewShellExecutor(&ShellExecutorOptions{
		Shell: &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   "on-request",
			CWD:              hostCWD,
			DefaultTimeoutMS: 5000,
		},
	})
	prepared, err := local.PrepareExecCommand(&Invocation{
		CallID:   "call-local-cwd",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"true"}`},
	})
	if err != nil {
		t.Fatalf("local PrepareExecCommand() error = %v", err)
	}
	if prepared.Request.CWD != filepath.Clean(hostCWD) {
		t.Fatalf("local cwd = %q, want %q", prepared.Request.CWD, filepath.Clean(hostCWD))
	}
	if prepared.Request.PathConvention != "" {
		t.Fatalf("local path convention = %q, want the native default", prepared.Request.PathConvention)
	}
}
