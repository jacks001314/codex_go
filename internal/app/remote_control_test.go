package app

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"codex_go/internal/appserverdaemon"
)

func TestRemoteControlStartPlatformBoundary(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"remote-control", "--json", "start"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if runtime.GOOS == "windows" {
		if !errors.Is(err, appserverdaemon.ErrUnsupportedPlatform) {
			t.Fatalf("remote-control start error = %v", err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "managed standalone Codex install not found") {
		t.Fatalf("remote-control start error = %v", err)
	}
}

func TestRemoteControlForegroundUnsupportedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("foreground remote-control uses a Unix socket")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	err := Run(context.Background(), []string{"remote-control", "--json"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, appserverdaemon.ErrUnsupportedPlatform) {
		t.Fatalf("remote-control foreground error = %v", err)
	}
}

func TestRemoteControlPairHuman(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "pair"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control pair returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pairing code:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRemoteControlStopHuman(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "stop"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control stop returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Remote control is not running.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
