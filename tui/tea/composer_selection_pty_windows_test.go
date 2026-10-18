//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
)

const composerSelectionPTYEnv = "CODEX_TUI_COMPOSER_SELECTION_PTY"

// TestOwnedComposerSelectionPTY exercises editable mouse selection through the
// production renderer: drag + copy keeps the highlight, and typing replaces it.
func TestOwnedComposerSelectionPTY(t *testing.T) {
	if os.Getenv(composerSelectionPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the owned-screen composer selection ConPTY check", composerSelectionPTYEnv)
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
	home := t.TempDir()
	t.Cleanup(func() {
		killCodexProcessesUnderPath(home)
		time.Sleep(500 * time.Millisecond)
	})
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatalf("write isolated auth fixture: %v", err)
	}
	config := "model = \"gpt-5\"\nopenai_base_url = \"http://127.0.0.1:1/v1\"\n"
	config += "[tui]\nalternate_screen = \"always\"\ncopy_on_select = \"always\"\n"
	config += "[projects." + strconv.Quote(root) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write isolated config fixture: %v", err)
	}

	terminal, err := gopty.New()
	if err != nil {
		t.Fatalf("create production ConPTY: %v", err)
	}
	defer terminal.Close()
	if err := terminal.Resize(100, 40); err != nil {
		t.Fatalf("resize production ConPTY: %v", err)
	}
	command := terminal.Command(exe, "--no-daemon")
	command.Dir = root
	command.Env = append(os.Environ(),
		"CODEX_HOME="+home,
		"OPENAI_API_KEY=sk-test",
		"TERM=xterm-256color",
	)
	if err := command.Start(); err != nil {
		t.Fatalf("spawn production ConPTY: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	var output lockedTerminalOutput
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buffer := make([]byte, 4096)
		for {
			read, readErr := terminal.Read(buffer)
			if read > 0 {
				output.Write(buffer[:read])
			}
			if readErr != nil {
				return
			}
		}
	}()
	ready, earlyExit := waitForGoPTYOutputOrExit(&output, "enter send", exited, 10*time.Second)
	if !ready {
		if earlyExit != nil {
			t.Fatalf("TUI exited before the composer appeared: %v output=%q", earlyExit, output.String())
		}
		_ = command.Process.Kill()
		t.Fatalf("composer did not appear through ConPTY; output=%q", output.String())
	}

	_, _ = terminal.Write([]byte("hello world"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(output.String(), "hello world") {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(output.String(), "hello world") {
		_ = command.Process.Kill()
		t.Fatalf("draft did not reach the composer; output=%q", output.String())
	}
	time.Sleep(300 * time.Millisecond)
	beforeDrag := output.String()

	// With the default 100x40 layout the prompt text begins at row 36, column
	// 5 (1-based); SGR coordinates are 1-based.
	_, _ = terminal.Write([]byte("\x1b[<0;5;36M"))
	time.Sleep(50 * time.Millisecond)
	_, _ = terminal.Write([]byte("\x1b[<32;10;36M"))
	time.Sleep(50 * time.Millisecond)
	_, _ = terminal.Write([]byte("\x1b[<0;10;36m"))
	time.Sleep(500 * time.Millisecond)
	afterDrag := output.String()
	suffix := afterDrag
	if len(beforeDrag) <= len(afterDrag) {
		suffix = afterDrag[len(beforeDrag):]
	}
	if !strings.Contains(suffix, "\x1b[7m") {
		t.Fatalf("composer selection was not highlighted: %q", suffix)
	}

	beforeTyping := output.String()
	_, _ = terminal.Write([]byte("X"))
	time.Sleep(500 * time.Millisecond)
	afterTyping := output.String()
	suffix = afterTyping
	if len(beforeTyping) <= len(afterTyping) {
		suffix = afterTyping[len(beforeTyping):]
	}
	if !strings.Contains(suffix, "X world") {
		t.Fatalf("typing did not replace the composer selection: %q", suffix)
	}

	_ = command.Process.Kill()
	conPTY, ok := terminal.(gopty.ConPty)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		if ok {
			_ = conPTY.InputPipe().Close()
		}
		t.Fatalf("TUI did not stop after composer selection check")
	}
	if ok {
		_ = conPTY.InputPipe().Close()
		_ = conPTY.OutputPipe().Close()
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatalf("timed out draining ConPTY output")
	}
}
