//go:build !windows

package win

import (
	"bytes"
	"strings"
	"testing"
)

func TestEnsureCodexAppRuntimePathsReadableRecordsUnsupportedOffWindows(t *testing.T) {
	root := makeRuntimeDirs(t)
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("USERPROFILE", root)
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable("S-1-1-0", &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) == 0 || !strings.Contains(refreshErrors[0], "unsupported") {
		t.Fatalf("refreshErrors = %#v", refreshErrors)
	}
	if !strings.Contains(log.String(), "continuing") {
		t.Fatalf("log = %q", log.String())
	}
}
