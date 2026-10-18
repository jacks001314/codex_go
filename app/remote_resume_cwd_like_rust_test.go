package app

import (
	"strings"
	"testing"

	"codex_go/cli"
)

// TestRemoteTUIResumeParamsResumeDirectoryLikeRust locks Rust's division
// between Embedded and Remote thread params: a local daemon receives the
// resolved resume directory, while an explicit remote workspace only receives
// --cd and otherwise leaves the server-owned saved directory untouched.
func TestRemoteTUIResumeParamsResumeDirectoryLikeRust(t *testing.T) {
	tests := []struct {
		name      string
		root      *cli.RootOptions
		resumeCWD string
		wantCWD   string
		wantErr   string
	}{
		{
			name:      "local daemon forwards resolved session directory",
			root:      &cli.RootOptions{},
			resumeCWD: "/saved/session",
			wantCWD:   "/saved/session",
		},
		{
			name: "local daemon without a resolved directory keeps saved cwd",
			root: &cli.RootOptions{},
		},
		{
			name:      "remote workspace forwards --cd",
			root:      &cli.RootOptions{Remote: "wss://example.com/rpc", Shared: cli.SharedOptions{CWD: "/remote/worktree"}},
			resumeCWD: "/remote/worktree",
			wantCWD:   "/remote/worktree",
		},
		{
			name: "remote workspace keeps server cwd without --cd",
			root: &cli.RootOptions{Remote: "wss://example.com/rpc"},
		},
		{
			name:      "remote current choice requires --cd",
			root:      &cli.RootOptions{Remote: "wss://example.com/rpc"},
			resumeCWD: "/caller/worktree",
			wantErr:   "`tui.resume_cwd = \"current\"` requires `--cd` when using a remote workspace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := remoteTUIResumeParams(tt.root, "thread-resume", tt.resumeCWD)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resume params error = %v", err)
			}
			if params.ThreadID != "thread-resume" {
				t.Fatalf("threadId = %q", params.ThreadID)
			}
			if tt.wantCWD == "" {
				if params.CWD != nil {
					t.Fatalf("cwd = %q, want nil", *params.CWD)
				}
				return
			}
			if params.CWD == nil || *params.CWD != tt.wantCWD {
				t.Fatalf("cwd = %v, want %q", params.CWD, tt.wantCWD)
			}
		})
	}
}
