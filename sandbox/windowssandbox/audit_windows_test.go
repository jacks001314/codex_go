//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestAuditEveryoneWritableFindsWorldWritableChild(t *testing.T) {
	cwd := t.TempDir()
	child := filepath.Join(cwd, "world-writable")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := EnsureAllowWriteACEs(ACLRequest{Path: child, SID: "S-1-1-0"}); err != nil {
		t.Fatalf("EnsureAllowWriteACEs(Everyone) error = %v", err)
	}
	flagged, err := AuditEveryoneWritable(cwd, map[string]string{"PATH": ""}, "")
	if err != nil {
		t.Fatalf("AuditEveryoneWritable() error = %v", err)
	}
	if !containsCanonical(flagged, child) {
		t.Fatalf("AuditEveryoneWritable() = %#v, want child %q", flagged, child)
	}
}

func TestApplyCapabilityDeniesForWorldWritableAddsReadonlyCapDeny(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	flagged := filepath.Join(cwd, "world-writable")
	if err := os.MkdirAll(flagged, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	profile := coresandbox.ReadOnlyPermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{cwd})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	req := &WorldWritableAuditRequest{
		CodexHome:   codexHome,
		CWD:         cwd,
		Env:         map[string]string{},
		Permissions: permissions,
	}
	if err := applyCapabilityDeniesForWorldWritable(req, []string{flagged}); err != nil {
		t.Fatalf("applyCapabilityDeniesForWorldWritable() error = %v", err)
	}
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilitySIDs() error = %v", err)
	}
	sidBytes, err := SIDBytesFromString(caps.Readonly)
	if err != nil {
		t.Fatalf("SIDBytesFromString(readonly) error = %v", err)
	}
	sd, dacl, err := fileDACL(flagged)
	if err != nil {
		t.Fatalf("fileDACL() error = %v", err)
	}
	if !daclHasDeniedMaskForSID(dacl, sidPointerFromBytes(sidBytes), denyWriteMask) {
		t.Fatalf("readonly capability deny-write ACE not present")
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
}

func TestApplyCapabilityDeniesForWorldWritablePropagatesFailuresLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	cwd := t.TempDir()
	profile := coresandbox.ReadOnlyPermissionProfile()
	permissions, err := ResolvePermissions(&profile, []string{cwd})
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	req := &WorldWritableAuditRequest{
		CodexHome:   codexHome,
		CWD:         cwd,
		Env:         map[string]string{},
		Permissions: permissions,
	}
	// A flagged path that cannot be ACL-ed (does not exist) must surface as an
	// aggregated error instead of being silently logged away.
	missing := filepath.Join(cwd, "does-not-exist")
	err = applyCapabilityDeniesForWorldWritable(req, []string{missing})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("applyCapabilityDeniesForWorldWritable(missing) error = %v, want path-context error", err)
	}
}
