package win

import (
	"bytes"
	"os"
	"path/filepath"
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
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable("S-1-1-0", &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 || log.Len() != 0 {
		t.Fatalf("refreshErrors/log = %#v/%q", refreshErrors, log.String())
	}
}

func makeRuntimeDirs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "OpenAI", "Codex", "bin"),
		filepath.Join(root, "OpenAI", "Codex", "runtimes"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}
	return root
}
