//go:build windows

package win

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

// TestGrantUserProfileReadAttributesIsNonInheritingLikeRust ports Rust #49690:
// a root-only read-attributes grant must leave an unprotected child's security
// descriptor untouched, even when the profile already has inheritable ACEs.
func TestGrantUserProfileReadAttributesIsNonInheritingLikeRust(t *testing.T) {
	profile := t.TempDir()
	commandCWD := filepath.Join(profile, "workspace")
	child := filepath.Join(profile, "child")
	for _, dir := range []string{commandCWD, child} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("Mkdir(%s) error = %v", dir, err)
		}
	}
	const inheritedSID = "S-1-5-21-10-20-30-41"
	if _, err := windowssandbox.GrantAllowMaskACEsWithInheritance(windowssandbox.ACLRequest{
		Path: profile,
		SID:  inheritedSID,
		Mask: windows.FILE_GENERIC_READ,
	}, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT); err != nil {
		t.Fatalf("seed inheritable ACE error = %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		child,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, nil, nil,
	); err != nil {
		t.Skipf("host does not permit a null DACL: %v", err)
	}
	before := namedDACLString(t, child)
	const sandboxSID = "S-1-5-21-10-20-30-50"
	payload := &setupPayload{UserProfile: profile, CommandCWD: commandCWD}
	grantUserProfileReadAttributes(payload, sandboxSID, io.Discard)
	if after := namedDACLString(t, child); after != before {
		t.Fatalf("non-inheriting grant propagated to child: before=%q after=%q", before, after)
	}
	allowed, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{
		Path: profile,
		SID:  sandboxSID,
		Mask: uint32(windows.FILE_READ_ATTRIBUTES),
	}, true)
	if err != nil {
		t.Fatalf("PathMaskAllows(profile) error = %v", err)
	}
	if !allowed {
		t.Fatal("profile does not allow FILE_READ_ATTRIBUTES after the grant")
	}
}

func TestGrantUserProfileReadAttributesSkipsCommandOutsideProfile(t *testing.T) {
	profile := t.TempDir()
	commandCWD := t.TempDir()
	before := namedDACLString(t, profile)
	payload := &setupPayload{UserProfile: profile, CommandCWD: commandCWD}
	grantUserProfileReadAttributes(payload, "S-1-5-21-10-20-30-50", io.Discard)
	if after := namedDACLString(t, profile); after != before {
		t.Fatalf("grant changed a profile outside the command cwd: before=%q after=%q", before, after)
	}
}

func namedDACLString(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) error = %v", path, err)
	}
	return sd.String()
}
