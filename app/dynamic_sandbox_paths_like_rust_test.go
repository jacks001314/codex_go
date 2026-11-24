package app

import (
	"reflect"
	"testing"

	"codex_go/appserver"
)

// Rust #62376: a child thread inherits the server's writable-root strings
// without reinterpreting them under the client host's path rules.
func TestDynamicSandboxFromResumePreservesForeignPlatformRootsLikeRust(t *testing.T) {
	policy := map[string]any{
		"type":          "workspaceWrite",
		"writableRoots": []any{`C:\shared`, "/srv/shared"},
	}
	resumed := &appserver.ThreadResumeResponse{Sandbox: policy}

	mode, turnPolicy, err := dynamicSandboxFromResume(resumed)
	if err != nil {
		t.Fatalf("dynamicSandboxFromResume() error = %v", err)
	}
	if mode != "workspace-write" {
		t.Fatalf("sandbox mode = %#v, want workspace-write", mode)
	}
	if !reflect.DeepEqual(turnPolicy, policy) {
		t.Fatalf("turn policy = %#v, want the server policy unchanged", turnPolicy)
	}
}

func TestDynamicSandboxFromResumeRejectsExternalSandboxWithoutProfileLikeRust(t *testing.T) {
	_, _, err := dynamicSandboxFromResume(&appserver.ThreadResumeResponse{
		Sandbox: map[string]any{"type": "externalSandbox"},
	})
	if err == nil || err.Error() != "Cannot inherit an external sandbox without a permission profile" {
		t.Fatalf("external sandbox error = %v", err)
	}
}
