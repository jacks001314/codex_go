//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const clearHeaderPTYEnv = "CODEX_TUI_CLEAR_PTY"

// TestClearReinsertsHeaderPTY checks the production binary: after /clear the
// compact borderless header must be drawn again (Rust #48562).
func TestClearReinsertsHeaderPTY(t *testing.T) {
	if os.Getenv(clearHeaderPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the /clear header ConPTY check", clearHeaderPTYEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	exe := strings.TrimSpace(os.Getenv("CODEX_GO_EXE"))
	if exe == "" {
		exe = filepath.Join(root, "bin", "codex.exe")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("required executable %s: %v", exe, err)
	}

	output := runCodexSlashPTY(t, exe, root, "gcode", []string{"/clear"})
	clearIndex := strings.LastIndex(output, "\x1b[2J")
	if clearIndex < 0 {
		t.Fatalf("output has no terminal clear sequence:\n%q", output)
	}
	afterClear := output[clearIndex:]
	if !strings.Contains(afterClear, ">_ gcode") {
		t.Fatalf("cleared terminal did not reinsert the borderless header:\n%s", afterClear)
	}
	if strings.Contains(afterClear, "No messages yet.") {
		t.Fatalf("cleared terminal fell back to the empty transcript:\n%s", afterClear)
	}
}
