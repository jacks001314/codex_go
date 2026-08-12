package win

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalAppDataRoot(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\codex\AppData\Local`)
	t.Setenv("USERPROFILE", `C:\Users\ignored`)
	if got := LocalAppDataRoot(); got != `C:\Users\codex\AppData\Local` {
		t.Fatalf("LocalAppDataRoot() = %q", got)
	}
	t.Setenv("LOCALAPPDATA", "")
	if got := LocalAppDataRoot(); got != filepath.Join(`C:\Users\ignored`, "AppData", "Local") {
		t.Fatalf("LocalAppDataRoot() fallback = %q", got)
	}
}

func TestEnsureCodexAppRuntimePathsReadableSkipsMissingDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("USERPROFILE", filepath.Join(root, "no-user"))
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable("S-1-1-0", &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 || log.Len() != 0 {
		t.Fatalf("refreshErrors/log = %#v/%q", refreshErrors, log.String())
	}
}

func TestRuntimePathsIncludeAppRootAndPrimaryRuntimeRoot(t *testing.T) {
	got := runtimePaths(`C:\Users\user\AppData\Local`, `C:\Users\user`)
	want := []string{
		`C:\Users\user\AppData\Local\OpenAI\Codex`,
		`C:\Users\user\.cache\codex-runtimes`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimePaths() = %#v, want %#v", got, want)
	}
	if runtimePaths("", `C:\Users\user`) != nil && len(runtimePaths("", `C:\Users\user`)) != 1 {
		t.Fatalf("runtimePaths without LocalAppData = %#v", runtimePaths("", `C:\Users\user`))
	}
	if runtimePaths(`C:\Users\user\AppData\Local`, "") != nil && len(runtimePaths(`C:\Users\user\AppData\Local`, "")) != 1 {
		t.Fatalf("runtimePaths without user profile = %#v", runtimePaths(`C:\Users\user\AppData\Local`, ""))
	}
	if got := runtimePaths("", ""); len(got) != 0 {
		t.Fatalf("runtimePaths() with no roots = %#v", got)
	}
}

func TestRuntimeDirEligible(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", file, err)
	}
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "directory", path: dir, want: true},
		{name: "file", path: file, want: false},
		{name: "missing", path: filepath.Join(root, "missing"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeDirEligible(tc.path); got != tc.want {
				t.Fatalf("runtimeDirEligible(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func makeRuntimeDirs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "OpenAI", "Codex"),
		filepath.Join(root, ".cache", "codex-runtimes"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	return root
}
