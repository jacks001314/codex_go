//go:build windows

package win

import (
	"bytes"
	"testing"
)

func TestEnsureCodexAppRuntimePathsReadableGrantsEveryone(t *testing.T) {
	root := makeRuntimeDirs(t)
	t.Setenv("LOCALAPPDATA", root)
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable("S-1-1-0", &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 {
		t.Fatalf("refreshErrors = %#v; log=%s", refreshErrors, log.String())
	}
	if log.Len() == 0 {
		t.Fatalf("expected grant log")
	}
}
