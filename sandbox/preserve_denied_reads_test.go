package sandbox

import (
	"strings"
	"testing"
)

func TestPreserveDeniedReadsRetainsGlobDepthWithoutMutatingInputs(t *testing.T) {
	raw := `{"type":"managed","network":"restricted","file_system":{"type":"restricted","glob_scan_max_depth":2,"entries":[{"path":{"type":"glob_pattern","pattern":"**/*.env"},"access":"deny"}]}}`
	current, err := ParseRuntimePermissionProfileJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, err := RuntimePermissionProfileJSON(*current)
	if err != nil {
		t.Fatal(err)
	}
	for _, requested := range []PermissionProfile{ReadOnlyPermissionProfile(), FullAccessPermissionProfile(), {SandboxPolicy: &SandboxPolicy{Kind: SandboxDangerFullAccess}}, {SandboxPolicy: NewWorkspaceWritePolicy()}} {
		result, err := PreserveDeniedReads(requested, *current)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := RuntimePermissionProfileJSON(*result)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(encoded, `"glob_scan_max_depth":2`) || !strings.Contains(encoded, `"access":"deny"`) {
			t.Fatalf("lost restrictions: %s", encoded)
		}
		again, err := PreserveDeniedReads(*result, *current)
		if err != nil {
			t.Fatal(err)
		}
		twice, _ := RuntimePermissionProfileJSON(*again)
		if twice != encoded {
			t.Fatalf("not idempotent: %s != %s", twice, encoded)
		}
	}
	unchanged, _ := RuntimePermissionProfileJSON(*current)
	if unchanged != before {
		t.Fatalf("input mutated: %s", unchanged)
	}
}
