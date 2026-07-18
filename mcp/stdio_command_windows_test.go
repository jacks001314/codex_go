package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewMCPStdioCommandRunsBatchShimWithArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "command with spaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	shim := filepath.Join(dir, "mcp-shim.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho %~1^|%~2\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	output, err := newMCPStdioCommand(shim, "-y", "@gebrai/gebrai").CombinedOutput()
	if err != nil {
		t.Fatalf("batch shim failed: %v\n%s", err, output)
	}
	if got, want := strings.TrimSpace(string(output)), "-y|@gebrai/gebrai"; got != want {
		t.Fatalf("batch shim output = %q, want %q", got, want)
	}
}

func TestWindowsBatchCommandLineQuotesPathAndArguments(t *testing.T) {
	got := windowsBatchCommandLine(`C:\Program Files\nodejs\npx.cmd`, []string{"-y", "@gebrai/gebrai", "two words"})
	want := `/d /s /c ""C:\Program Files\nodejs\npx.cmd" "-y" "@gebrai/gebrai" "two words""`
	if got != want {
		t.Fatalf("windowsBatchCommandLine() = %q, want %q", got, want)
	}
}
