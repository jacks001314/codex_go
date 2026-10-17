package config

import (
	"reflect"
	"testing"

	"codex_go/sandbox"
	"codex_go/utils"
)

func TestFilesystemDenyReadUsesLayerPathContext(t *testing.T) {
	firstContext, err := NewConfigPathContext(
		utils.ConventionWindows,
		"file:///C:/first",
		"file:///C:/Users/first",
	)
	if err != nil {
		t.Fatalf("NewConfigPathContext(first) error = %v", err)
	}
	secondContext, err := NewConfigPathContext(
		utils.ConventionWindows,
		"file:///D:/second",
		"file:///D:/Users/second",
	)
	if err != nil {
		t.Fatalf("NewConfigPathContext(second) error = %v", err)
	}
	first := map[string]any{
		"filesystem": map[string]any{
			"deny_read": []any{"private", "~/secret", "private", `\\server\share\`},
		},
	}
	second := map[string]any{
		"filesystem": map[string]any{
			"deny_read": []any{"private", "~/secret"},
		},
	}
	if err := resolveFilesystemDenyReadPathsWithContext(first, firstContext); err != nil {
		t.Fatalf("resolve first denials: %v", err)
	}
	if err := resolveFilesystemDenyReadPathsWithContext(second, secondContext); err != nil {
		t.Fatalf("resolve second denials: %v", err)
	}
	wantFirst := []any{`C:\first\private`, `C:\Users\first\secret`, `\\server\share\`}
	wantSecond := []any{`D:\second\private`, `D:\Users\second\secret`}
	if got := denyReadEntries(t, first); !reflect.DeepEqual(got, wantFirst) {
		t.Fatalf("first deny_read = %#v, want %#v", got, wantFirst)
	}
	if got := denyReadEntries(t, second); !reflect.DeepEqual(got, wantSecond) {
		t.Fatalf("second deny_read = %#v, want %#v", got, wantSecond)
	}
}

func TestFilesystemDenyReadRejectsUnsafeContextFactsAtomically(t *testing.T) {
	context, err := NewConfigPathContext(
		utils.ConventionWindows,
		"file:///C:/work/sam[1]",
		"file:///C:/Users/sam[1]",
	)
	if err != nil {
		t.Fatalf("NewConfigPathContext() error = %v", err)
	}
	for _, input := range []string{"private/*.key", "~/private/*.key", "private/key"} {
		permissions := map[string]any{
			"filesystem": map[string]any{
				"deny_read": []any{input, "secrets/**\x00"},
			},
		}
		before := denyReadEntries(t, permissions)
		if err := resolveFilesystemDenyReadPathsWithContext(permissions, context); err == nil {
			t.Fatalf("resolveFilesystemDenyReadPathsWithContext(%q) succeeded, want an error", input)
		}
		if got := denyReadEntries(t, permissions); !reflect.DeepEqual(got, before) {
			t.Fatalf("deny_read mutated after failure: got %#v, want %#v", got, before)
		}
	}
}

func TestFilesystemDenyReadRequiresSuppliedHome(t *testing.T) {
	context, err := NewConfigPathContext(utils.ConventionWindows, "file:///C:/base", "")
	if err != nil {
		t.Fatalf("NewConfigPathContext() error = %v", err)
	}
	permissions := map[string]any{
		"filesystem": map[string]any{"deny_read": []any{"~/secret"}},
	}
	if err := resolveFilesystemDenyReadPathsWithContext(permissions, context); err == nil {
		t.Fatal("home-relative denial succeeded without a home directory")
	}
}

func TestFilesystemDenyReadRejectsWindowsAmbiguousGlobs(t *testing.T) {
	context, err := NewConfigPathContext(
		utils.ConventionWindows,
		"file://server/share/base",
		"",
	)
	if err != nil {
		t.Fatalf("NewConfigPathContext() error = %v", err)
	}
	for _, pattern := range []string{
		`\\?\C:\private`,
		`\\*\share\private`,
		`D:*.key`,
		`private\*\file:stream`,
	} {
		if _, err := resolveDenyReadPatternWithContext(pattern, context); err == nil {
			t.Fatalf("resolveDenyReadPatternWithContext(%q) succeeded, want an error", pattern)
		}
	}
}

func TestCompilePermissionProfileResolvesConfiguredRootsWithContext(t *testing.T) {
	context, err := NewConfigPathContext(
		utils.ConventionWindows,
		"file:///C:/work/project",
		"file:///C:/Users/user",
	)
	if err != nil {
		t.Fatalf("NewConfigPathContext() error = %v", err)
	}
	profiles := map[string]any{
		"scoped": map[string]any{
			"workspace_roots": map[string]any{
				`..\workspace`: true,
				`~/shared`:     true,
			},
			"filesystem": map[string]any{
				":workspace_roots": map[string]any{
					"private/*.env": "deny",
				},
			},
		},
	}
	builder, err := compileConfigPermissionProfile(profiles, "scoped", map[string]bool{}, context)
	if err != nil {
		t.Fatalf("compileConfigPermissionProfile() error = %v", err)
	}
	roots, err := builder.effectiveWorkspaceRootsFor(nil, `C:\work\project`)
	if err != nil {
		t.Fatalf("effectiveWorkspaceRootsFor() error = %v", err)
	}
	wantRoots := []string{`C:\work\project`, `C:\work\workspace`, `C:\Users\user\shared`}
	if !reflect.DeepEqual(roots, wantRoots) {
		t.Fatalf("workspace roots = %#v, want %#v", roots, wantRoots)
	}
	entries, err := builder.materializedEntriesForWorkspaceRoots(roots)
	if err != nil {
		t.Fatalf("materializedEntriesForWorkspaceRoots() error = %v", err)
	}
	foundGlob := false
	for _, entry := range entries {
		if entry.Path.Type == "glob_pattern" && entry.Path.Pattern == `C:\work\workspace\private\*.env` {
			foundGlob = true
		}
	}
	if !foundGlob {
		t.Fatalf("materialized entries = %#v, want a workspace-rooted deny glob", entries)
	}
}

func TestUnsafeWorkspaceGlobFailsClosedToRootDeny(t *testing.T) {
	context, err := NewNativeConfigPathContext(t.TempDir())
	if err != nil {
		t.Fatalf("NewNativeConfigPathContext() error = %v", err)
	}
	profiles := map[string]any{
		"scoped": map[string]any{
			"filesystem": map[string]any{
				":workspace_roots": map[string]any{
					"private/*.env": "deny",
				},
			},
		},
	}
	builder, err := compileConfigPermissionProfile(profiles, "scoped", map[string]bool{}, context)
	if err != nil {
		t.Fatalf("compileConfigPermissionProfile() error = %v", err)
	}
	unsafeRoot := context.baseDir.NativePathString()
	if context.Convention() == utils.ConventionWindows {
		unsafeRoot += `\[workspace]`
	} else {
		unsafeRoot += "/[workspace]"
	}
	entries, err := builder.materializedEntriesForWorkspaceRoots([]string{unsafeRoot})
	if err != nil {
		t.Fatalf("materializedEntriesForWorkspaceRoots() error = %v", err)
	}
	want := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: utils.LexicalClean(unsafeRoot, context.Convention())},
		Access: sandbox.FileSystemAccessDeny,
	}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("materialized entries = %#v, want %#v", entries, want)
	}
}
