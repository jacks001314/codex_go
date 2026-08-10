package tool

import (
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestMaybeParseRemoteApplyPatchDetectsBodies(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{`apply_patch <<'EOF'
*** Begin Patch
*** Update File: a.txt
@@
-old
+new
*** End Patch
EOF`, true},
		{"apply_patch", true},
		{"echo hello", false},
		{"ls -la", false},
		{"apply_patch --help", false},
	}
	for _, tc := range cases {
		if _, got := maybeParseRemoteApplyPatch(tc.command); got != tc.want {
			t.Fatalf("maybeParseRemoteApplyPatch(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestRejectRemoteApplyPatchRestrictedWrite(t *testing.T) {
	executor := &ShellExecutor{}
	profile := &sandbox.PermissionProfile{Disabled: false, SandboxPolicy: sandbox.NewWorkspaceWritePolicy()}
	req := &ShellRequest{
		Command:              []string{"apply_patch", "<<'EOF'"},
		UnifiedExecRemoteURL: "http://127.0.0.1:8080",
		PermissionProfile:    profile,
	}
	if !executor.rejectRemoteApplyPatch(req) {
		t.Fatal("restricted remote apply_patch was not rejected")
	}
	// Unrestricted profile is allowed.
	fullDisk := &sandbox.PermissionProfile{Disabled: false, SandboxPolicy: sandbox.NewDangerFullAccessPolicy()}
	req.PermissionProfile = fullDisk
	if executor.rejectRemoteApplyPatch(req) {
		t.Fatal("full-disk remote apply_patch was rejected")
	}
	// Non-apply-patch remote command is allowed.
	req.PermissionProfile = profile
	req.Command = []string{"echo", "hello"}
	if executor.rejectRemoteApplyPatch(req) {
		t.Fatal("remote echo was rejected")
	}
	// Local command is never rejected here.
	req.Command = []string{"apply_patch", "<<'EOF'"}
	req.UnifiedExecRemoteURL = ""
	if executor.rejectRemoteApplyPatch(req) {
		t.Fatal("local apply_patch was rejected")
	}
}

func TestRejectRemoteApplyPatchRestrictedMessage(t *testing.T) {
	body := "cross-platform remote apply_patch is unavailable until executor-side filesystem sandboxing is supported"
	if !strings.Contains(body, "executor-side filesystem sandboxing") {
		t.Fatalf("message = %q", body)
	}
}
