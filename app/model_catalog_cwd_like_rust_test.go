package app

import (
	"testing"

	"codex_go/cli"
)

// TestInteractiveModelListLoadOptionsUsesSessionCWDLikeRust covers the
// connected/embedded catalog path: the attached session's working directory
// overrides the launch directory so project-scoped provider/catalog settings
// apply (Rust #55682).
func TestInteractiveModelListLoadOptionsUsesSessionCWDLikeRust(t *testing.T) {
	root := &cli.RootOptions{
		Shared:          cli.SharedOptions{CWD: "/launch", Profile: "work"},
		ConfigOverrides: []string{"model=gpt-5"},
	}
	options := interactiveModelListLoadOptions(root, "/project")
	if options == nil {
		t.Fatal("load options = nil")
	}
	if options.CWD != "/project" {
		t.Fatalf("cwd = %q, want the session directory", options.CWD)
	}
	if options.Profile != "work" || len(options.RawOverrides) != 1 {
		t.Fatalf("launch options lost: %#v", options)
	}

	options = interactiveModelListLoadOptions(root, "   ")
	if options.CWD != "/launch" {
		t.Fatalf("empty session cwd = %q, want the launch directory", options.CWD)
	}

	options = interactiveModelListLoadOptions(nil, "/project")
	if options == nil || options.CWD != "/project" {
		t.Fatalf("nil root options = %#v, want the session directory", options)
	}
}
