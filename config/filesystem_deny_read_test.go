package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func denyReadEntries(t *testing.T, permissions map[string]any) []any {
	t.Helper()
	filesystem, _ := permissions["filesystem"].(map[string]any)
	entries, _ := filesystem["deny_read"].([]any)
	return entries
}

func TestResolveFilesystemDenyReadPathsResolvesAndPreservesGlobs(t *testing.T) {
	base := t.TempDir()
	permissions := map[string]any{
		"filesystem": map[string]any{
			"deny_read": []any{
				"relative/secret",
				"also/*.key",
				filepath.Join(base, "abs"),
			},
		},
	}
	if err := resolveFilesystemDenyReadPaths(permissions, base); err != nil {
		t.Fatalf("resolveFilesystemDenyReadPaths: %v", err)
	}
	want := []any{
		filepath.Clean(filepath.Join(base, "relative", "secret")),
		filepath.Clean(filepath.Join(base, "also") + string(filepath.Separator) + "*.key"),
		filepath.Clean(filepath.Join(base, "abs")),
	}
	if got := denyReadEntries(t, permissions); !reflect.DeepEqual(got, want) {
		t.Fatalf("deny_read = %#v, want %#v", got, want)
	}
}

func TestResolveFilesystemDenyReadPathsDeduplicatesInOrder(t *testing.T) {
	base := t.TempDir()
	permissions := map[string]any{
		"filesystem": map[string]any{"deny_read": []any{"a", "b", "a"}},
	}
	if err := resolveFilesystemDenyReadPaths(permissions, base); err != nil {
		t.Fatalf("resolveFilesystemDenyReadPaths: %v", err)
	}
	want := []any{
		filepath.Clean(filepath.Join(base, "a")),
		filepath.Clean(filepath.Join(base, "b")),
	}
	if got := denyReadEntries(t, permissions); !reflect.DeepEqual(got, want) {
		t.Fatalf("deny_read = %#v, want %#v", got, want)
	}
}

func TestResolveFilesystemDenyReadPathsRejectsInvalidEntries(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name        string
		denyRead    any
		baseDir     string
		wantErrText string
	}{
		{"nul byte", []any{"bad\x00path"}, base, "NUL"},
		{"empty", []any{"   "}, base, "must not be empty"},
		{"relative without base", []any{"relative"}, "", "must be absolute"},
		{"non-string entry", []any{42}, base, "must be strings"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			permissions := map[string]any{
				"filesystem": map[string]any{"deny_read": testCase.denyRead},
			}
			err := resolveFilesystemDenyReadPaths(permissions, testCase.baseDir)
			if err == nil {
				t.Fatalf("expected error containing %q", testCase.wantErrText)
			}
		})
	}
}

func TestParseRequirementsTOMLResolvesDenyReadAgainstDocumentDir(t *testing.T) {
	base := t.TempDir()
	document := []byte("[permissions.filesystem]\ndeny_read = [\"relative/secret\", \"also/*.key\"]\n")
	requirements, err := ParseRequirementsTOMLWithBaseDir(document, base)
	if err != nil {
		t.Fatalf("ParseRequirementsTOMLWithBaseDir: %v", err)
	}
	if requirements == nil {
		t.Fatal("requirements = nil")
	}
	want := []any{
		filepath.Clean(filepath.Join(base, "relative", "secret")),
		filepath.Clean(filepath.Join(base, "also") + string(filepath.Separator) + "*.key"),
	}
	if got := denyReadEntries(t, requirements.Permissions); !reflect.DeepEqual(got, want) {
		t.Fatalf("deny_read = %#v, want %#v", got, want)
	}
}

func TestParseRequirementsTOMLRejectsInvalidDenyRead(t *testing.T) {
	document := []byte("[permissions.filesystem]\ndeny_read = [\"bad\x00path\"]\n")
	if _, err := ParseRequirementsTOMLWithBaseDir(document, t.TempDir()); err == nil {
		t.Fatal("expected invalid deny_read to fail requirements parsing")
	}
}
