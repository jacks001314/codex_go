package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDoctorProbeHelperSkipsConfigurationLikeRust mirrors Rust #46543: the
// hidden `doctor --probe-filesystem-path` helper resolves one path and exits
// before configuration loading, so a broken config cannot change its status.
func TestDoctorProbeHelperSkipsConfigurationLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[[[broken"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	target := filepath.Join(t.TempDir(), "missing")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"doctor", "--probe-filesystem-path", target}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("probe helper error = %v, want an ExitError", err)
	}
	want := 2
	if runtime.GOOS == "windows" {
		// The helper never touches the filesystem on Windows.
		want = 5
	}
	if exitErr.Code != want {
		t.Fatalf("probe helper exit code = %d, want %d", exitErr.Code, want)
	}
	if !exitErr.Silent {
		t.Fatalf("probe helper exit error = %#v, want silent", exitErr)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("probe helper output stdout=%q stderr=%q, want none", stdout.String(), stderr.String())
	}
}
