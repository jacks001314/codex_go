//go:build windows

package windowssandbox

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

// Rust parity: codex-rs/windows-sandbox-rs/src/process.rs `console_flags`
// (#49308). CREATE_NO_WINDOW is requested only for a pipe-backed child in
// NoWindow mode; a child without explicit stdio keeps the parent console.
func TestSandboxConsoleFlagsLikeRust(t *testing.T) {
	cases := []struct {
		hasStdio bool
		mode     ConsoleMode
		want     uint32
	}{
		{true, ConsoleModeNoWindow, uint32(windows.CREATE_NO_WINDOW)},
		{true, ConsoleModeInherit, 0},
		{false, ConsoleModeNoWindow, 0},
		{false, ConsoleModeInherit, 0},
	}
	for _, tc := range cases {
		if got := SandboxConsoleFlags(tc.hasStdio, tc.mode); got != tc.want {
			t.Fatalf("SandboxConsoleFlags(%v, %v) = %#x, want %#x", tc.hasStdio, tc.mode, got, tc.want)
		}
	}
}

// The piped legacy sandbox launch (Rust #49308) must ask Windows for a child
// with no console of its own. The flags handed to CreateProcessAsUserW are
// observed through the package's own failure log.
func TestPipedLegacySandboxSpawnRequestsNoConsoleWindow(t *testing.T) {
	dir := t.TempDir()
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		t.Fatalf("CreatePipe stdin: %v", err)
	}
	defer windows.CloseHandle(inR)
	defer windows.CloseHandle(inW)
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		t.Fatalf("CreatePipe stdout: %v", err)
	}
	defer windows.CloseHandle(outR)
	defer windows.CloseHandle(outW)

	// Deliberately invalid token handle: the launch fails after the creation
	// flags have been selected, and the failure is logged with those flags.
	if _, err := CreateProcessAsUserWithToken(ProcessSpawnRequest{
		Token:       uintptr(1),
		Command:     []string{"cmd.exe", "/c", "exit 0"},
		CWD:         dir,
		Env:         map[string]string{},
		LogsBaseDir: dir,
		Stdio:       &ProcessStdio{Stdin: uintptr(inR), Stdout: uintptr(outW), Stderr: uintptr(outW)},
	}); err == nil {
		t.Fatal("an invalid token unexpectedly produced a process")
	}

	data, err := os.ReadFile(CurrentLogFilePathForBaseDir(dir))
	if err != nil {
		t.Fatalf("read failure log: %v", err)
	}
	match := regexp.MustCompile(`creation_flags=(\d+)`).FindStringSubmatch(string(data))
	if match == nil {
		t.Fatalf("failure log records no creation_flags: %s", data)
	}
	flags, err := strconv.ParseUint(match[1], 10, 32)
	if err != nil {
		t.Fatalf("parse creation_flags: %v", err)
	}
	if flags&uint64(windows.CREATE_NO_WINDOW) == 0 {
		t.Fatalf("creation_flags = %#x, want CREATE_NO_WINDOW for a piped legacy sandbox child", flags)
	}
	if flags&uint64(windows.EXTENDED_STARTUPINFO_PRESENT) == 0 {
		t.Fatalf("creation_flags = %#x, want EXTENDED_STARTUPINFO_PRESENT", flags)
	}
}
