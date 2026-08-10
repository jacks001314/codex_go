package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeCurProjectPathCommonSeparators mirrors Rust f344a80a3b
// (cur_tests.rs resolves_cur_project_names_with_common_separators): encoded
// project names resolve against a bounded set of separator candidates instead
// of recursively walking the directory tree.
func TestDecodeCurProjectPathCommonSeparators(t *testing.T) {
	cases := []struct {
		projectName string
		encodedName string
	}{
		{"project", "project"},
		{"my-project", "my-project"},
		{"my--project", "my-project"},
		{"my project", "my-project"},
		{"my.project", "my-project"},
		{"my..project", "my-project"},
		{"my_project", "my-project"},
	}
	for _, tc := range cases {
		root := t.TempDir()
		project := filepath.Join(root, tc.projectName)
		if err := os.MkdirAll(project, 0o700); err != nil {
			t.Fatal(err)
		}
		encoded := externalCursorPathSlug(root) + "-" + tc.encodedName
		decoded, ok := decodeCurProjectPath(encoded)
		if !ok || filepath.Clean(decoded) != filepath.Clean(project) {
			t.Fatalf("decode(%q) = (%q, %v), want %q", encoded, decoded, ok, project)
		}
	}
}

// TestDecodeCurProjectPathRejectsAmbiguous mirrors Rust f344a80a3b: when
// several separator variants match, the candidate is rejected.
func TestDecodeCurProjectPathRejectsAmbiguous(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"my-project", "my project", "my+project"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	encoded := externalCursorPathSlug(root) + "-my-project"
	if decoded, ok := decodeCurProjectPath(encoded); ok {
		t.Fatalf("decode(%q) = %q, want ambiguous rejection", encoded, decoded)
	}
}

// TestDecodeCurProjectPathRejectsUnsafeComponents mirrors Rust f344a80a3b:
// traversal and absolute components are rejected before probing.
func TestDecodeCurProjectPathRejectsUnsafeComponents(t *testing.T) {
	for _, encoded := range []string{
		"C-..-Users",
		"C-a-.b",
		"C-a-b:c",
		"C-a-b\\c",
		"C-a-b/c",
	} {
		if decoded, ok := decodeCurProjectPath(encoded); ok {
			t.Fatalf("decode(%q) = %q, want rejection", encoded, decoded)
		}
	}
}

func TestDecodeCurWindowsProjectDrive(t *testing.T) {
	if drive, rest, ok := decodeCurWindowsProjectDrive("C--Users-fixture-Cursor"); !ok || drive != "C:\\" || rest != "-Users-fixture-Cursor" {
		t.Fatalf("decode drive C-- = (%q, %q, %v)", drive, rest, ok)
	}
	if drive, rest, ok := decodeCurWindowsProjectDrive("C-Users-fixture-Cursor"); !ok || drive != "C:\\" || rest != "Users-fixture-Cursor" {
		t.Fatalf("decode drive C- = (%q, %q, %v)", drive, rest, ok)
	}
	if _, _, ok := decodeCurWindowsProjectDrive("1-Users-fixture"); ok {
		t.Fatalf("decode drive 1- should be rejected")
	}
}
