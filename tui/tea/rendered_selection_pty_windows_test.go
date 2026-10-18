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

const renderedSelectionPTYEnv = "CODEX_TUI_RENDERED_SELECTION_PTY"

// TestOwnedScreenFooterSelectionPTY checks the explicit alternate-screen mode:
// the app enables mouse reporting and paints a reverse-video footer selection.
func TestOwnedScreenFooterSelectionPTY(t *testing.T) {
	if os.Getenv(renderedSelectionPTYEnv) != "1" {
		t.Skipf("set %s=1 to run the owned-screen selection ConPTY check", renderedSelectionPTYEnv)
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
	config += "[tui]\nalternate_screen = \"always\"\n"
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
			t.Fatalf("TUI exited before the footer appeared: %v output=%q", earlyExit, output.String())
		}
		_ = command.Process.Kill()
		t.Fatalf("footer did not appear through ConPTY; output=%q", output.String())
	}
	_, _ = terminal.Write([]byte("\x1b[<0;1;40M"))
	time.Sleep(50 * time.Millisecond)
	_, _ = terminal.Write([]byte("\x1b[<32;12;40M"))
	time.Sleep(50 * time.Millisecond)
	_, _ = terminal.Write([]byte("\x1b[<0;12;40m"))
	time.Sleep(400 * time.Millisecond)
	_ = command.Process.Kill()
	conPTY, ok := terminal.(gopty.ConPty)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		if ok {
			_ = conPTY.InputPipe().Close()
		}
		t.Fatalf("TUI did not stop after selection capture")
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
	rendered := output.String()
	if !strings.Contains(rendered, "\x1b[?1002h") && !strings.Contains(rendered, "\x1b[?1000h") {
		t.Fatalf("owned-screen mode did not enable mouse reporting: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("footer selection was not highlighted: %q", rendered)
	}
}
