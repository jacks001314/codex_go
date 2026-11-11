//go:build windows

package provisionservice

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCanonicalizeProvisioningHomeResolvesJunctionLikeRust mirrors Rust #60708:
// provisioning and desktop registration requests name the physical home even
// when the caller spells it through a directory junction.
func TestCanonicalizeProvisioningHomeResolvesJunctionLikeRust(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	alias := filepath.Join(dir, "alias")
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/j", alias, target).CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
	physical := canonicalizeProvisioningHome(target)
	if got := canonicalizeProvisioningHome(alias); got != physical {
		t.Fatalf("canonicalizeProvisioningHome(alias) = %q, want %q", got, physical)
	}
	if got := canonicalizeProvisioningHome(physical); got != physical {
		t.Fatalf("canonicalizeProvisioningHome(physical) = %q, want no change", got)
	}
}
