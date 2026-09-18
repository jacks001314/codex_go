package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustGuardianPolicyPromptMatchesGo is the shared-fixture differential for
// the Guardian reviewer's base instructions: Rust embeds
// prompts/templates/guardian/{policy,policy_template}.md via include_str! and Go
// vendors the same files (state/templates/guardian/*.md via //go:embed), which
// state.GuardianPolicy / state.GuardianPolicyTemplate render when the model
// catalog provides no auto-review messages.
//
// The Rust blobs are read through `git show` so a Windows autocrlf checkout
// cannot mask a real difference, and the Go copies are compared with CRLF
// normalized to LF for the same reason.
func TestRustGuardianPolicyPromptMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	for _, asset := range []struct {
		rust   string
		goPath string
		marker string
	}{
		{
			rust:   "HEAD:codex-rs/prompts/templates/guardian/policy.md",
			goPath: filepath.Join("..", "state", "templates", "guardian", "policy.md"),
			marker: "## Risk Taxonomy and Allow/Deny Rules",
		},
		{
			rust:   "HEAD:codex-rs/prompts/templates/guardian/policy_template.md",
			goPath: filepath.Join("..", "state", "templates", "guardian", "policy_template.md"),
			marker: "{{ tenant_policy_config }}",
		},
	} {
		want := string(gitOutput(t, rustRepo, "show", asset.rust))
		if !strings.Contains(want, asset.marker) {
			t.Fatalf("Rust guardian asset %s lost %q:\n%s", asset.rust, asset.marker, want)
		}
		data, err := os.ReadFile(asset.goPath)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", asset.goPath, err)
		}
		got := strings.ReplaceAll(string(data), "\r\n", "\n")
		if got != want {
			t.Fatalf("guardian asset drift for %s\nRust: %q\nGo:   %q", asset.rust, want, got)
		}
	}
}
