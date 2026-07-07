//go:build windows

package windowssandbox

import (
	"os"
	"runtime"
	"testing"
)

func TestApplyDenyReadACLsMaterializesMissingPathAndAppliesACE(t *testing.T) {
	path := t.TempDir() + `\missing-secret`
	applied, err := ApplyDenyReadACLs([]string{path}, testCapabilitySID)
	if err != nil {
		t.Fatalf("ApplyDenyReadACLs() error = %v", err)
	}
	if len(applied) == 0 || !containsLexicalPath(applied, path) {
		t.Fatalf("ApplyDenyReadACLs() applied = %#v, want %q", applied, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("materialized deny-read path missing: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("materialized deny-read path is not a directory")
	}

	sidBytes, err := SIDBytesFromString(testCapabilitySID)
	if err != nil {
		t.Fatalf("SIDBytesFromString() error = %v", err)
	}
	sd, dacl, err := fileDACL(path)
	if err != nil {
		t.Fatalf("fileDACL() error = %v", err)
	}
	if !daclHasDeniedMaskForSID(dacl, sidPointerFromBytes(sidBytes), denyReadMask) {
		t.Fatalf("deny-read ACE not present after ApplyDenyReadACLs")
	}
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
}
