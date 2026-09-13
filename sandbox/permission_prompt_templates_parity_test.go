package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rustPermissionTemplatesRoot locates the Rust checkout's permissions templates
// (skipping the check when the checkout is unavailable).
func rustPermissionTemplatesRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_ROOT"); env != "" {
		candidates = append(candidates, filepath.Join(env, "prompts", "templates", "permissions"))
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs", "prompts", "templates", "permissions"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs", "prompts", "templates", "permissions"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs", "prompts", "templates", "permissions"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "approval_policy", "never.md")); err == nil {
			return abs
		}
	}
	t.Skip("Rust permissions templates not found; set CODEX_RUST_ROOT")
	return ""
}

// TestPermissionPromptTemplatesMatchRust pins the vendored permission-prompt
// text to codex-rs/prompts/templates/permissions: the approval-policy templates
// are used verbatim, while the sandbox-mode templates are trimmed of trailing
// whitespace like Rust's `Template::parse(<template>.trim_end())`.
func TestPermissionPromptTemplatesMatchRust(t *testing.T) {
	root := rustPermissionTemplatesRoot(t)
	cases := []struct {
		name    string
		path    string
		goValue string
		trimEnd bool
	}{
		{"approval_policy/never.md", filepath.Join(root, "approval_policy", "never.md"), permissionPromptApprovalNever, false},
		{"approval_policy/unless_trusted.md", filepath.Join(root, "approval_policy", "unless_trusted.md"), permissionPromptApprovalUnlessTrusted, false},
		{"approval_policy/on_request.md", filepath.Join(root, "approval_policy", "on_request.md"), permissionPromptApprovalOnRequest, false},
		{"approval_policy/on_request_rule_request_permission.md", filepath.Join(root, "approval_policy", "on_request_rule_request_permission.md"), permissionPromptApprovalOnRequestRuleRequestPermission, false},
		{"sandbox_mode/danger_full_access.md", filepath.Join(root, "sandbox_mode", "danger_full_access.md"), permissionPromptSandboxDangerFullAccess, true},
		{"sandbox_mode/workspace_write.md", filepath.Join(root, "sandbox_mode", "workspace_write.md"), permissionPromptSandboxWorkspaceWrite, true},
		{"sandbox_mode/read_only.md", filepath.Join(root, "sandbox_mode", "read_only.md"), permissionPromptSandboxReadOnly, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", tc.path, err)
			}
			want := strings.ReplaceAll(string(data), "\r\n", "\n")
			if tc.trimEnd {
				want = strings.TrimRight(want, " \t\r\n")
			}
			if tc.goValue != want {
				t.Fatalf("template drift for %s\nRust: %q\nGo:   %q", tc.name, want, tc.goValue)
			}
		})
	}
}
